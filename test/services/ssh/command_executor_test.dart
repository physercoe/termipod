import 'dart:async';
import 'dart:typed_data';

import 'package:dartssh2/dartssh2.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/ssh/command_executor.dart';

class _Session implements SSHSession {
  final out = StreamController<Uint8List>();
  final err = StreamController<Uint8List>();
  int closes = 0;
  @override
  Stream<Uint8List> get stdout => out.stream;
  @override
  Stream<Uint8List> get stderr => err.stream;
  @override
  int get exitCode => 0;
  @override
  void close() {
    closes++;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

void main() {
  testWidgets('output timeout closes channel and releases the command queue', (
    tester,
  ) async {
    final first = _Session();
    final second = _Session();
    var calls = 0;
    final executor = CommandExecutor(
      (_) async => calls++ == 0 ? first : second,
    );
    final failure = expectLater(
      executor.run('slow', timeout: const Duration(seconds: 1)),
      throwsA(isA<TimeoutException>()),
    );
    await tester.pump();
    await tester.pump(const Duration(seconds: 2));
    await failure;
    expect(first.closes, 1);
    final next = executor.run('fast');
    await tester.pump();
    second.out.add(Uint8List.fromList([111, 107]));
    unawaited(second.out.close());
    unawaited(second.err.close());
    await tester.pump();
    expect((await next).stdout, 'ok');
    expect(second.closes, 1);
  });

  testWidgets(
    'queue deadline prevents a timed-out command from being sent later',
    (tester) async {
      final first = _Session();
      final commands = <String>[];
      final executor = CommandExecutor((command) async {
        commands.add(command);
        return first;
      });
      final holding = executor.run('first');
      await tester.pump();
      final failure = expectLater(
        executor.run('must-not-run', timeout: const Duration(seconds: 1)),
        throwsA(isA<TimeoutException>()),
      );
      await tester.pump(const Duration(seconds: 2));
      await failure;
      unawaited(first.out.close());
      unawaited(first.err.close());
      await tester.pump();
      await holding;
      expect(commands, ['first']);
    },
  );

  testWidgets('late channel is closed after opening times out', (tester) async {
    final gate = Completer<SSHSession>();
    final session = _Session();
    final executor = CommandExecutor((_) => gate.future);
    final failure = expectLater(
      executor.run('slow-open', timeout: const Duration(seconds: 1)),
      throwsA(isA<TimeoutException>()),
    );
    await tester.pump(const Duration(seconds: 2));
    await failure;
    gate.complete(session);
    await tester.pump();
    expect(session.closes, 1);
  });

  testWidgets('stream errors close the channel too', (tester) async {
    final session = _Session();
    final executor = CommandExecutor((_) async => session);
    final failure = expectLater(executor.run('bad-output'), throwsStateError);
    await tester.pump();
    session.out.addError(StateError('lost'));
    unawaited(session.out.close());
    unawaited(session.err.close());
    await tester.pump();
    await failure;
    expect(session.closes, 1);
  });
}
