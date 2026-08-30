import 'dart:convert';

/// The opaque Hub record needed by the client-side merge loop.
class VaultRemoteSnapshot {
  const VaultRemoteSnapshot({required this.ciphertext, required this.version});

  final String ciphertext;
  final int version;
}

class VaultMergeResult {
  const VaultMergeResult(this.bundle);

  final Map<String, dynamic> bundle;
}

typedef PullVaultSnapshot = Future<VaultRemoteSnapshot?> Function();
typedef OpenVaultSnapshot = Future<Map<String, dynamic>> Function(String ciphertext);
typedef ReadLocalVault = Future<Map<String, dynamic>> Function();
typedef SealVaultSnapshot = Future<String> Function(Map<String, dynamic> bundle);
typedef PushVaultSnapshot = Future<void> Function(String ciphertext, int baseVersion);

Map<String, dynamic> _emptyBundle() => <String, dynamic>{
      'connections': <dynamic>[],
      'sshKeys': <String, dynamic>{
        'meta': <dynamic>[],
        'privateKeys': <String, String>{},
        'passphrases': <String, String>{},
      },
      'passwords': <String, String>{},
    };

String _canonical(dynamic value) {
  if (value is List) return '[${value.map(_canonical).join(',')}]';
  if (value is Map) {
    final entries = value.entries.toList()
      ..sort((a, b) => a.key.toString().compareTo(b.key.toString()));
    return '{${entries.map((e) => '${jsonEncode(e.key.toString())}:${_canonical(e.value)}').join(',')}}';
  }
  return jsonEncode(value);
}

DateTime? _timestamp(Map<String, dynamic> item) {
  for (final key in const ['updatedAt', 'createdAt']) {
    final value = item[key];
    if (value is String) {
      final parsed = DateTime.tryParse(value);
      if (parsed != null) return parsed;
    }
  }
  return null;
}

class _EntityMerge {
  const _EntityMerge(this.items, this.winners);

  final List<Map<String, dynamic>> items;
  final Map<String, bool> winners; // true = remote, false = local
}

_EntityMerge _mergeEntities({
  required List<Map<String, dynamic>> local,
  required List<Map<String, dynamic>> remote,
  required dynamic Function(Map<String, dynamic> item, bool remote) compare,
}) {
  final remoteById = <String, Map<String, dynamic>>{
    for (final item in remote) if (item['id'] is String) item['id'] as String: item,
  };
  final localIds = <String>{};
  final winners = <String, bool>{};
  final items = <Map<String, dynamic>>[];

  for (final localItem in local) {
    final id = localItem['id'];
    // An older or future record without a usable ID cannot participate in
    // reconciliation, but it is still local user data and must not be lost.
    if (id is! String) {
      items.add(localItem);
      continue;
    }
    localIds.add(id);
    final remoteItem = remoteById[id];
    if (remoteItem == null) {
      winners[id] = false;
      items.add(localItem);
      continue;
    }
    if (_canonical(compare(localItem, false)) == _canonical(compare(remoteItem, true))) {
      winners[id] = false;
      items.add(localItem);
      continue;
    }
    final localTime = _timestamp(localItem);
    final remoteTime = _timestamp(remoteItem);
    final useRemote = localTime != null && remoteTime != null && remoteTime.isAfter(localTime);
    winners[id] = useRemote;
    items.add(useRemote ? remoteItem : localItem);
  }

  for (final remoteItem in remote) {
    final id = remoteItem['id'];
    if (id is! String || localIds.contains(id)) continue;
    winners[id] = true;
    items.add(remoteItem);
  }
  return _EntityMerge(items, winners);
}

Map<String, String> _stringMap(dynamic value) {
  if (value is! Map) return <String, String>{};
  return <String, String>{
    for (final entry in value.entries)
      if (entry.value is String) entry.key.toString(): entry.value as String,
  };
}

List<Map<String, dynamic>> _entityList(dynamic value) {
  if (value is! List) return <Map<String, dynamic>>[];
  return value.whereType<Map>().map((item) => item.cast<String, dynamic>()).toList();
}

Map<String, String> _mergeSecrets(
  Map<String, String> local,
  Map<String, String> remote,
  Map<String, bool> winners,
  String Function(String key) owner,
) {
  final out = <String, String>{};
  for (final key in {...local.keys, ...remote.keys}) {
    final useRemote = winners[owner(key)] ?? false;
    final preferred = useRemote ? remote[key] : local[key];
    final fallback = useRemote ? local[key] : remote[key];
    final value = preferred ?? fallback;
    if (value != null) out[key] = value;
  }
  return out;
}

/// Merge the fields mobile understands while preserving every unknown top-level
/// field from the Hub snapshot. Absence is never interpreted as deletion.
VaultMergeResult mergeMobileVaultBundles(
  Map<String, dynamic> local,
  Map<String, dynamic> remote,
) {
  final localPasswords = _stringMap(local['passwords']);
  final remotePasswords = _stringMap(remote['passwords']);
  final connections = _mergeEntities(
    local: _entityList(local['connections']),
    remote: _entityList(remote['connections']),
    compare: (item, isRemote) {
      final passwords = isRemote ? remotePasswords : localPasswords;
      final id = item['id'] as String? ?? '';
      return <String, dynamic>{
        'meta': item,
        'password': passwords[id],
        'jumpPassword': passwords['${id}_jump'],
      };
    },
  );

  final localSsh = local['sshKeys'] is Map
      ? (local['sshKeys'] as Map).cast<String, dynamic>()
      : <String, dynamic>{};
  final remoteSsh = remote['sshKeys'] is Map
      ? (remote['sshKeys'] as Map).cast<String, dynamic>()
      : <String, dynamic>{};
  final localPrivate = _stringMap(localSsh['privateKeys']);
  final remotePrivate = _stringMap(remoteSsh['privateKeys']);
  final localPassphrases = _stringMap(localSsh['passphrases']);
  final remotePassphrases = _stringMap(remoteSsh['passphrases']);
  final keys = _mergeEntities(
    local: _entityList(localSsh['meta']),
    remote: _entityList(remoteSsh['meta']),
    compare: (item, isRemote) {
      final id = item['id'] as String? ?? '';
      return <String, dynamic>{
        'meta': item,
        'privateKey': (isRemote ? remotePrivate : localPrivate)[id],
        'passphrase': (isRemote ? remotePassphrases : localPassphrases)[id],
      };
    },
  );

  // Start with the remote map so desktop-only and future fields survive a
  // mobile round trip even though this client cannot interpret them.
  final merged = <String, dynamic>{...remote};
  merged['connections'] = connections.items;
  merged['passwords'] = _mergeSecrets(
    localPasswords,
    remotePasswords,
    connections.winners,
    (key) => key.endsWith('_jump') ? key.substring(0, key.length - 5) : key,
  );
  merged['sshKeys'] = <String, dynamic>{
    ...remoteSsh,
    'meta': keys.items,
    'privateKeys': _mergeSecrets(localPrivate, remotePrivate, keys.winners, (key) => key),
    'passphrases': _mergeSecrets(localPassphrases, remotePassphrases, keys.winners, (key) => key),
  };

  final localPins = _stringMap(local['pinnedHostKeys']);
  final remotePins = _stringMap(remote['pinnedHostKeys']);
  if (localPins.isNotEmpty || remotePins.isNotEmpty) {
    // Never replace a locally trusted host key without an explicit trust
    // decision. Mobile has no conflict UI, so retain the local pin on a
    // conflict (matching desktop) while still accepting Hub-only pins.
    merged['pinnedHostKeys'] = <String, String>{...remotePins, ...localPins};
  }
  return VaultMergeResult(merged);
}

/// Pull, decrypt, merge, re-seal, and CAS-push. A 409 retry starts over from a
/// fresh pull and fresh merge; stale ciphertext is never submitted twice.
Future<VaultMergeResult> pushMergedMobileVault({
  required PullVaultSnapshot pull,
  required OpenVaultSnapshot open,
  required ReadLocalVault readLocal,
  required SealVaultSnapshot seal,
  required PushVaultSnapshot push,
  required bool Function(Object error) isConflict,
  int maxAttempts = 2,
}) async {
  if (maxAttempts < 1) throw ArgumentError.value(maxAttempts, 'maxAttempts');
  for (var attempt = 0; attempt < maxAttempts; attempt++) {
    final snapshot = await pull();
    final remote = snapshot == null ? _emptyBundle() : await open(snapshot.ciphertext);
    final result = mergeMobileVaultBundles(await readLocal(), remote);
    final ciphertext = await seal(result.bundle);
    try {
      await push(ciphertext, snapshot?.version ?? 0);
      return result;
    } catch (error) {
      if (!isConflict(error) || attempt + 1 >= maxAttempts) rethrow;
    }
  }
  throw StateError('unreachable vault sync loop');
}
