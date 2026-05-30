import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'hub_transport.dart';

/// Content-addressed blob transfer: upload raw bytes (server-side dedup
/// by sha256) and download by sha, with an optional disk-first cache for
/// offline-safe rendering. Wedge W3 of `docs/plans/hub-client-split.md`.
class BlobsApi {
  final HubTransport _t;
  BlobsApi(this._t);

  /// Uploads raw bytes; returns `{sha256, size, mime}`. Dedup is automatic
  /// server-side — same bytes → same sha → no duplicate row. 25 MiB cap.
  Future<Map<String, dynamic>> uploadBlob(
    List<int> bytes, {
    String? mime,
  }) async {
    final req = await _t.open('POST', '/v1/blobs');
    req.headers.contentType = ContentType.parse(mime ?? 'application/octet-stream');
    req.add(bytes);
    final resp = await req.close();
    final out = await _t.readJson(resp);
    return (out as Map).cast<String, dynamic>();
  }

  /// Downloads blob bytes by sha. Caller is responsible for writing to
  /// disk / piping to share_plus; we keep the full payload in memory since
  /// the server caps uploads at 25 MiB anyway.
  ///
  /// When [HubTransport.blobCache] is attached, a successful fetch is
  /// persisted to the content-addressed on-disk cache so subsequent reads
  /// — including ones issued while the hub is unreachable — can be served
  /// via [downloadBlobCached] without a network round-trip.
  Future<List<int>> downloadBlob(String sha) async {
    final req = await _t.open('GET', '/v1/blobs/$sha');
    final resp = await req.close();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      final msg = await resp.transform(utf8.decoder).join();
      throw HubApiError(resp.statusCode, msg);
    }
    final out = <int>[];
    await for (final chunk in resp) {
      out.addAll(chunk);
    }
    final c = _t.blobCache;
    if (c != null) {
      // Fire-and-forget the write so a slow disk doesn't block image
      // rendering. Misses on the next fetch just re-download.
      unawaited(c.put(sha, out));
    }
    return out;
  }

  /// Disk-first variant of [downloadBlob]: returns cached bytes when the
  /// content-addressed cache has them, otherwise falls back to a live
  /// fetch. Transport failures with a cache hit still resolve — that's
  /// the whole point of offline-safe image rendering. Transport failures
  /// with no cached copy rethrow unchanged so callers can render an
  /// error placeholder.
  ///
  /// 4xx responses (blob missing, auth denied) always rethrow; a stale
  /// cache entry is never served against an authoritative negative
  /// response. 5xx and SocketException/TimeoutException/HttpException
  /// fall back to the cache when available.
  Future<List<int>> downloadBlobCached(String sha) async {
    final c = _t.blobCache;
    if (c != null) {
      final hit = await c.get(sha);
      if (hit != null) return hit;
    }
    try {
      return await downloadBlob(sha);
    } catch (e) {
      if (c == null) rethrow;
      if (e is HubApiError && e.status < 500) rethrow;
      if (e is! HubApiError &&
          e is! SocketException &&
          e is! TimeoutException &&
          e is! HttpException) {
        rethrow;
      }
      final hit = await c.get(sha);
      if (hit != null) return hit;
      rethrow;
    }
  }
}
