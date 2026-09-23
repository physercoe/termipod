import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:dartssh2/dartssh2.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/ssh/local_forward.dart';

void main() {
  test(
    'loopback forward supports concurrent HTTP and large streaming bodies',
    () async {
      final remote = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(() => remote.close(force: true));
      final payload = List.generate(128 * 1024, (i) => i % 256);
      remote.listen((request) async {
        final body = await request.fold<List<int>>(
          [],
          (bytes, chunk) => bytes..addAll(chunk),
        );
        request.response.add(body.isEmpty ? payload : body);
        await request.response.close();
      });
      final forward = await LocalForward.start(
        () => SSHSocket.connect('127.0.0.1', remote.port),
      );
      addTearDown(forward.close);
      expect(forward.address.address, '127.0.0.1');
      final client = HttpClient();
      addTearDown(() => client.close(force: true));
      await Future.wait(
        List.generate(4, (i) async {
          final request = await client.postUrl(
            Uri.parse('http://127.0.0.1:${forward.port}/'),
          );
          request.add(payload);
          final response = await request.close();
          expect(
            await response.fold<List<int>>(
              [],
              (bytes, chunk) => bytes..addAll(chunk),
            ),
            payload,
          );
        }),
      );
    },
  );

  test('WebSocket upgrade remains bidirectional through forwarding', () async {
    final remote = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => remote.close(force: true));
    remote.listen((request) async {
      final ws = await WebSocketTransformer.upgrade(request);
      ws.listen(ws.add);
    });
    final forward = await LocalForward.start(
      () => SSHSocket.connect('127.0.0.1', remote.port),
    );
    addTearDown(forward.close);
    final ws = await WebSocket.connect('ws://127.0.0.1:${forward.port}/');
    final received = ws.first;
    ws.add('hello');
    expect(await received, 'hello');
    await ws.close();
  });

  test('SSE is delivered before the remote response finishes', () async {
    final remote = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => remote.close(force: true));
    final finish = Completer<void>();
    remote.listen((request) async {
      request.response.bufferOutput = false;
      request.response.headers.contentType = ContentType(
        'text',
        'event-stream',
      );
      request.response.write('data: first\n\n');
      await request.response.flush();
      await finish.future;
      await request.response.close();
    });
    final forward = await LocalForward.start(
      () => SSHSocket.connect('127.0.0.1', remote.port),
    );
    addTearDown(forward.close);
    final client = HttpClient();
    addTearDown(() => client.close(force: true));
    final response = await (await client.getUrl(
      Uri.parse('http://127.0.0.1:${forward.port}/'),
    )).close();
    final firstEvent = Completer<String>();
    final ended = Completer<void>();
    response
        .transform(utf8.decoder)
        .listen(
          (event) {
            if (!firstEvent.isCompleted) firstEvent.complete(event);
          },
          onDone: ended.complete,
          onError: ended.completeError,
        );
    expect(
      await firstEvent.future.timeout(const Duration(seconds: 3)),
      'data: first\n\n',
    );
    finish.complete();
    await ended.future;
  });

  test('recovery preserves listener port and drops old streams', () async {
    final remote = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(remote.close);
    remote.listen((s) {
      s.listen(s.add, onDone: s.destroy);
    });
    final forward = await LocalForward.start(
      () => SSHSocket.connect('127.0.0.1', remote.port),
    );
    addTearDown(forward.close);
    final port = forward.port;
    final old = await Socket.connect('127.0.0.1', port);
    final ended = old.drain<void>();
    forward.setAvailable(false);
    await ended;
    forward.setAvailable(true);
    expect(forward.port, port);
    final next = await Socket.connect('127.0.0.1', port);
    next.add([1, 2, 3]);
    expect(await next.first, [1, 2, 3]);
    next.destroy();
  });

  test(
    'stop while channel opens destroys late channel and rejects new sockets',
    () async {
      final gate = Completer<SSHSocket>();
      final accepted = Completer<void>();
      final forward = await LocalForward.start(() {
        accepted.complete();
        return gate.future;
      });
      final port = forward.port;
      final socket = await Socket.connect('127.0.0.1', port);
      final ended = socket.drain<void>();
      await accepted.future;
      await forward.close();
      final channel = _LateChannel();
      gate.complete(channel);
      await ended;
      await Future<void>.delayed(Duration.zero);
      expect(channel.destroyed, isTrue);
      await expectLater(
        Socket.connect('127.0.0.1', port),
        throwsA(isA<SocketException>()),
      );
    },
  );

  test('one refused channel does not stop listener', () async {
    final remote = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(remote.close);
    remote.listen((s) {
      s.add([42]);
      s.close();
    });
    var calls = 0;
    final forward = await LocalForward.start(() {
      if (calls++ == 0) throw StateError('remote service refused');
      return SSHSocket.connect('127.0.0.1', remote.port);
    });
    addTearDown(forward.close);
    final first = await Socket.connect('127.0.0.1', forward.port);
    await first.drain<void>();
    final second = await Socket.connect('127.0.0.1', forward.port);
    expect(await second.first, [42]);
    second.destroy();
  });
}

class _LateChannel implements SSHSocket {
  bool destroyed = false;
  @override
  void destroy() {
    destroyed = true;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
