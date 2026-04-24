import 'dart:async';
import 'dart:collection';
import 'dart:io';

import 'hub_client.dart' show HubApiError;
import 'hub_snapshot_cache.dart';

/// A response body paired with the age of the snapshot it was served from.
/// [staleSince] is null when the body came from a fresh network call, and
/// non-null when the network failed and we fell back to the cache — the
/// UI shows "Offline · last updated X" in that case.
class CachedResponse<T> {
  final T body;
  final DateTime? staleSince;
  const CachedResponse(this.body, this.staleSince);

  bool get isStale => staleSince != null;
}

/// Wraps a hub fetch with a read-through cache. On success the body is
/// stored and returned fresh; on transport failure or 5xx the cache is
/// consulted and — if a row exists — returned with its fetchedAt timestamp.
/// 4xx responses rethrow unmodified because they're authoritative (auth
/// denied, not found) and cached data must not be replayed for them.
///
/// [decode] converts the raw JSON [Object] stored in the cache back into
/// [T]. Callers that fetch `List<Map<String,dynamic>>` typically pass
/// `(body) => [for (final r in body as List) (r as Map).cast()]`.
Future<CachedResponse<T>> readThrough<T>({
  required HubSnapshotCache? cache,
  required String hubKey,
  required String endpoint,
  required Future<T> Function() fetch,
  required T Function(Object cachedBody) decode,
}) async {
  try {
    final data = await fetch();
    if (cache != null) {
      await cache.put(hubKey, endpoint, data as Object);
    }
    return CachedResponse<T>(data, null);
  } catch (e) {
    if (!_isOfflineFailure(e)) rethrow;
    if (cache == null) rethrow;
    final snap = await cache.get(hubKey, endpoint);
    if (snap == null) rethrow;
    return CachedResponse<T>(decode(snap.body), snap.fetchedAt);
  }
}

bool _isOfflineFailure(Object e) {
  if (e is SocketException) return true;
  if (e is TimeoutException) return true;
  if (e is HttpException) return true;
  if (e is HubApiError && e.status >= 500) return true;
  return false;
}

/// Build a deterministic cache key from a path + query map. Queries are
/// sorted alphabetically so `?a=1&b=2` and `?b=2&a=1` collide on the same
/// row. Returns the path alone when [query] is null or empty.
String buildEndpointKey(String path, [Map<String, String>? query]) {
  if (query == null || query.isEmpty) return path;
  final sorted = SplayTreeMap<String, String>.from(query);
  final qs = sorted.entries.map((e) => '${e.key}=${e.value}').join('&');
  return '$path?$qs';
}
