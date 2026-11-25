import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/ssh/socks5_socket.dart';

void main() {
  test(
    'SOCKS5 no-auth handshake preserves coalesced post-handshake SSH greeting',
    () async {
      final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(server.close);

      final receivedTarget = Completer<String>();
      server.listen((peer) {
        final buffer = <int>[];
        var stage = 0;
        peer.listen((chunk) {
          buffer.addAll(chunk);
          if (stage == 0 && buffer.length >= 3) {
            expect(buffer.sublist(0, 3), [0x05, 0x01, 0x00]);
            buffer.removeRange(0, 3);
            stage = 1;
            peer.add([0x05, 0x00]);
          }
          if (stage == 1 && buffer.length >= 5) {
            final hostLen = buffer[4];
            final needed = 5 + hostLen + 2;
            if (buffer.length >= needed) {
              final host = ascii.decode(buffer.sublist(5, 5 + hostLen));
              final port = (buffer[5 + hostLen] << 8) | buffer[6 + hostLen];
              receivedTarget.complete('$host:$port');
              buffer.removeRange(0, needed);
              stage = 2;
              // Coalesce the SOCKS5 reply and the tunneled SSH greeting in a
              // single TCP write to prove no trailing bytes are lost.
              peer.add([
                0x05, 0x00, 0x00, 0x01,
                127, 0, 0, 1,
                0x00, 0x16,
                ...ascii.encode('SSH-2.0-OpenSSH_9.6\r\n'),
              ]);
            }
          } else if (stage == 2 && buffer.isNotEmpty) {
            // Echo client bytes back over the established tunnel.
            peer.add(Uint8List.fromList(buffer));
            buffer.clear();
          }
        });
      });

      final socket = await Socks5Socket.connect(
        proxyHost: server.address.address,
        proxyPort: server.port,
        targetHost: 'bastion.internal',
        targetPort: 2222,
      );
      addTearDown(socket.destroy);

      expect(await receivedTarget.future, 'bastion.internal:2222');

      final received = <String>[];
      final greetingReady = Completer<void>();
      final echoReady = Completer<void>();
      final sub = socket.stream.listen((chunk) {
        final text = ascii.decode(chunk);
        received.add(text);
        if (!greetingReady.isCompleted &&
            received.join().contains('SSH-2.0-OpenSSH_9.6\r\n')) {
          greetingReady.complete();
        }
        if (!echoReady.isCompleted && received.join().contains('PING')) {
          echoReady.complete();
        }
      });
      addTearDown(sub.cancel);

      await greetingReady.future.timeout(const Duration(seconds: 2));
      socket.sink.add(ascii.encode('PING'));
      await echoReady.future.timeout(const Duration(seconds: 2));
      expect(received.join(), 'SSH-2.0-OpenSSH_9.6\r\nPING');
    },
  );

  test(
    'SOCKS5 username/password handshake works over a single socket subscription',
    () async {
      final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(server.close);

      String? authUser;
      String? authPass;
      server.listen((peer) {
        final buffer = <int>[];
        var stage = 0;
        peer.listen((chunk) {
          buffer.addAll(chunk);
          if (stage == 0 && buffer.length >= 4) {
            expect(buffer.sublist(0, 4), [0x05, 0x02, 0x00, 0x02]);
            buffer.removeRange(0, 4);
            stage = 1;
            peer.add([0x05, 0x02]);
          }
          if (stage == 1 && buffer.length >= 2) {
            final uLen = buffer[1];
            if (buffer.length >= 2 + uLen + 1) {
              final pLen = buffer[2 + uLen];
              final total = 3 + uLen + pLen;
              if (buffer.length >= total) {
                authUser = ascii.decode(buffer.sublist(2, 2 + uLen));
                authPass = ascii.decode(
                  buffer.sublist(3 + uLen, 3 + uLen + pLen),
                );
                buffer.removeRange(0, total);
                stage = 2;
                peer.add([0x01, 0x00]);
              }
            }
          }
          if (stage == 2 && buffer.length >= 5) {
            final hostLen = buffer[4];
            final total = 5 + hostLen + 2;
            if (buffer.length >= total) {
              buffer.removeRange(0, total);
              stage = 3;
              // Domain-type bound address (ATYP = 0x03)
              final bound = ascii.encode('proxy.local');
              peer.add([
                0x05, 0x00, 0x00, 0x03,
                bound.length, ...bound,
                0x04, 0x38,
              ]);
            }
          }
        });
      });

      final socket = await Socks5Socket.connect(
        proxyHost: server.address.address,
        proxyPort: server.port,
        targetHost: 'target.internal',
        targetPort: 22,
        username: 'alice',
        password: 'secret',
      );
      addTearDown(socket.destroy);

      expect(authUser, 'alice');
      expect(authPass, 'secret');
    },
  );

  test('SOCKS5 handshake fails cleanly on auth rejection and timeout', () async {
    final rejectServer = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(rejectServer.close);
    rejectServer.listen((peer) {
      var stage = 0;
      peer.listen((_) {
        if (stage == 0) {
          stage = 1;
          peer.add([0x05, 0x02]);
        } else if (stage == 1) {
          stage = 2;
          peer.add([0x01, 0x01]);
        }
      });
    });

    await expectLater(
      Socks5Socket.connect(
        proxyHost: rejectServer.address.address,
        proxyPort: rejectServer.port,
        targetHost: 'target.internal',
        targetPort: 22,
        username: 'alice',
        password: 'wrong',
      ),
      throwsA(
        isA<Socks5Exception>().having(
          (e) => e.message,
          'message',
          contains('authentication failed'),
        ),
      ),
    );

    final silentServer = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(silentServer.close);
    silentServer.listen((peer) {
      peer.listen((_) {});
    });

    await expectLater(
      Socks5Socket.connect(
        proxyHost: silentServer.address.address,
        proxyPort: silentServer.port,
        targetHost: 'target.internal',
        targetPort: 22,
        timeout: const Duration(milliseconds: 100),
      ),
      throwsA(isA<TimeoutException>()),
    );
  });
}
