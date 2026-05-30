import 'dart:convert';
import 'dart:io';

import 'hub_transport.dart';

/// Server-Sent Events channel streaming. The hub pushes `data: {...}`
/// frames separated by blank lines, with `: ping` keepalive comments we
/// drop; this client parses each frame to a JSON map. Wedge W4 of
/// `docs/plans/hub-client-split.md`.
class EventsApi {
  final HubTransport _t;
  EventsApi(this._t);

  /// Streams events for one channel as parsed JSON objects. The hub sends
  /// `data: {...}` frames separated by blank lines, plus `: ping` comments
  /// every 15s that we drop.
  ///
  /// Close the returned subscription to tear down the underlying socket —
  /// the generator cancels [HttpClientResponse.listen] cleanly when Flutter
  /// cancels the stream.
  Stream<Map<String, dynamic>> streamEvents(
    String projectId,
    String channelId, {
    String? since,
  }) =>
      _streamPath(
        '/v1/teams/${_t.cfg.teamId}/projects/$projectId/channels/$channelId/stream',
        since: since,
      );

  Stream<Map<String, dynamic>> streamTeamEvents(
    String channelId, {
    String? since,
  }) =>
      _streamPath(
        '/v1/teams/${_t.cfg.teamId}/channels/$channelId/stream',
        since: since,
      );

  /// SSE tail of the agent's event queue. Subscribes before replaying
  /// backfill from [sinceSeq] so no live event is missed in the gap.
  /// `sessionId` filters the live + backfilled events server-side to one
  /// session.
  Stream<Map<String, dynamic>> streamAgentEvents(
    String agentId, {
    int? sinceSeq,
    String? sessionId,
  }) {
    final extra = <String, String>{};
    if (sessionId != null && sessionId.isNotEmpty) {
      extra['session'] = sessionId;
    }
    return _streamPath(
      '/v1/teams/${_t.cfg.teamId}/agents/$agentId/stream',
      since: sinceSeq == null ? null : '$sinceSeq',
      query: extra.isEmpty ? null : extra,
    );
  }

  Stream<Map<String, dynamic>> _streamPath(
    String path, {
    String? since,
    Map<String, String>? query,
  }) async* {
    final merged = <String, String>{};
    if (since != null) merged['since'] = since;
    if (query != null) merged.addAll(query);
    final req = await _t.open(
      'GET',
      path,
      query: merged.isEmpty ? null : merged,
    );
    req.headers.set(HttpHeaders.acceptHeader, 'text/event-stream');
    req.headers.set(HttpHeaders.cacheControlHeader, 'no-cache');
    final resp = await req.close();
    if (resp.statusCode != 200) {
      final body = await resp.transform(utf8.decoder).join();
      throw HubApiError(resp.statusCode, body);
    }

    final buffer = StringBuffer();
    await for (final chunk
        in resp.transform(utf8.decoder).transform(const LineSplitter())) {
      if (chunk.isEmpty) {
        final frame = buffer.toString();
        buffer.clear();
        final payload = _extractData(frame);
        if (payload == null) continue;
        try {
          final decoded = jsonDecode(payload);
          if (decoded is Map) {
            yield decoded.cast<String, dynamic>();
          }
        } catch (_) {
          // One malformed frame shouldn't kill the stream.
        }
        continue;
      }
      if (chunk.startsWith(':')) continue; // SSE comment / keepalive
      buffer.writeln(chunk);
    }
  }

  /// Pulls `data:` payloads out of a multi-line SSE frame. Comments (`:`
  /// prefix) and unknown fields are ignored per the WHATWG spec.
  String? _extractData(String frame) {
    final lines = frame.split('\n');
    final data = StringBuffer();
    for (final line in lines) {
      if (line.isEmpty || line.startsWith(':')) continue;
      if (line.startsWith('data:')) {
        final v = line.substring(5);
        data.writeln(v.startsWith(' ') ? v.substring(1) : v);
      }
    }
    if (data.isEmpty) return null;
    var out = data.toString();
    if (out.endsWith('\n')) out = out.substring(0, out.length - 1);
    return out;
  }
}
