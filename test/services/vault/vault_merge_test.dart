import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/vault/vault_merge.dart';

Map<String, dynamic> _connection(String id, String updatedAt) => {
      'id': id,
      'name': 'host-$id',
      'host': '192.0.2.$id',
      'port': 22,
      'username': 'test',
      'createdAt': '2026-08-01T00:00:00.000Z',
      'updatedAt': updatedAt,
    };

Map<String, dynamic> _bundle(List<Map<String, dynamic>> connections) => {
      'connections': connections,
      'sshKeys': {
        'meta': <dynamic>[],
        'privateKeys': <String, String>{},
        'passphrases': <String, String>{},
      },
      'passwords': <String, String>{},
    };

class _Conflict implements Exception {}

void main() {
  test('merge retains local-only and Hub-only connections', () {
    final local = _bundle([
      _connection('1', '2026-08-01T00:00:00.000Z'),
      _connection('2', '2026-08-01T00:00:00.000Z'),
    ]);
    final remote = _bundle([
      _connection('1', '2026-08-01T00:00:00.000Z'),
      _connection('3', '2026-08-01T00:00:00.000Z'),
    ]);

    final merged = mergeMobileVaultBundles(local, remote).bundle;
    expect(
      (merged['connections'] as List).map((item) => (item as Map)['id']),
      ['1', '2', '3'],
    );
  });

  test('newer same-ID metadata and its password win together', () {
    final local = _bundle([
      _connection('1', '2026-08-01T00:00:00.000Z')..['name'] = 'local',
    ])..['passwords'] = {'1': 'local-secret'};
    final remote = _bundle([
      _connection('1', '2026-08-02T00:00:00.000Z')..['name'] = 'remote',
    ])..['passwords'] = {'1': 'remote-secret'};

    final merged = mergeMobileVaultBundles(local, remote).bundle;
    expect(((merged['connections'] as List).single as Map)['name'], 'remote');
    expect((merged['passwords'] as Map)['1'], 'remote-secret');
  });

  test('mobile round trip preserves desktop-only and future Hub fields', () {
    final local = _bundle([_connection('1', '2026-08-01T00:00:00.000Z')]);
    final remote = _bundle([_connection('1', '2026-08-01T00:00:00.000Z')])
      ..['items'] = [
        {'id': 'item-1', 'title': 'Registry'},
      ]
      ..['itemSecrets'] = {
        'item-1': {'password': 'desktop-secret'},
      }
      ..['app'] = {
        'config': {'sync_provider': 'webdav'},
        'secrets': {'sync_password': 'desktop-app-secret'},
      }
      ..['futureSection'] = {'format': 2};

    final merged = mergeMobileVaultBundles(local, remote).bundle;
    expect(merged['items'], remote['items']);
    expect(merged['itemSecrets'], remote['itemSecrets']);
    expect(merged['app'], remote['app']);
    expect(merged['futureSection'], remote['futureSection']);
  });

  test('merge preserves malformed local records instead of dropping data', () {
    final malformed = <String, dynamic>{'name': 'legacy host without an id'};
    final local = _bundle([malformed]);

    final merged = mergeMobileVaultBundles(local, _bundle([])).bundle;

    expect(merged['connections'], [malformed]);
  });

  test('host pin conflicts use the Hub-authored key', () {
    final local = _bundle([])
      ..['pinnedHostKeys'] = {
        'shared.example:22': 'local-key',
        'local.example:22': 'local-only-key',
      };
    final remote = _bundle([])
      ..['pinnedHostKeys'] = {
        'shared.example:22': 'remote-key',
        'remote.example:22': 'remote-only-key',
      };

    final merged = mergeMobileVaultBundles(local, remote).bundle;

    expect(merged['pinnedHostKeys'], {
      'shared.example:22': 'remote-key',
      'local.example:22': 'local-only-key',
      'remote.example:22': 'remote-only-key',
    });
  });

  test('409 retry pulls, merges, and seals again instead of retrying stale ciphertext', () async {
    var version = 1;
    var remote = _bundle([
      _connection('1', '2026-08-01T00:00:00.000Z'),
    ]);
    final local = _bundle([
      _connection('2', '2026-08-01T00:00:00.000Z'),
    ]);
    final pushed = <String>[];
    final bases = <int>[];

    final result = await pushMergedMobileVault(
      pull: () async => VaultRemoteSnapshot(
        ciphertext: jsonEncode(remote),
        version: version,
      ),
      open: (ciphertext) async =>
          (jsonDecode(ciphertext) as Map).cast<String, dynamic>(),
      readLocal: () async => local,
      seal: (bundle) async => jsonEncode(bundle),
      push: (ciphertext, baseVersion) async {
        pushed.add(ciphertext);
        bases.add(baseVersion);
        if (pushed.length == 1) {
          remote = _bundle([
            _connection('1', '2026-08-01T00:00:00.000Z'),
            _connection('3', '2026-08-02T00:00:00.000Z'),
          ]);
          version = 2;
          throw _Conflict();
        }
        remote = (jsonDecode(ciphertext) as Map).cast<String, dynamic>();
        version++;
      },
      isConflict: (error) => error is _Conflict,
    );

    expect(bases, [1, 2]);
    expect(pushed[1], isNot(pushed[0]));
    expect(
      (result.bundle['connections'] as List)
          .map((item) => (item as Map)['id']),
      ['2', '1', '3'],
    );
    expect(
      (remote['connections'] as List).map((item) => (item as Map)['id']),
      ['2', '1', '3'],
    );
  });
}
