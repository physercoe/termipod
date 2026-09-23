import 'dart:async';
import 'dart:io';
import 'dart:typed_data';
import 'package:dartssh2/dartssh2.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/ssh/persistent_shell.dart';

class _Session implements SSHSession {
  final out = StreamController<Uint8List>();
  final err = StreamController<Uint8List>();
  int writes = 0;
  int closes = 0;
  @override
  Stream<Uint8List> get stdout => out.stream;
  @override
  Stream<Uint8List> get stderr => err.stream;
  @override
  void write(Uint8List data) {
    writes++;
  }

  @override
  void close() {
    closes++;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _Transport implements SSHClient {
  _Transport(this.open);
  final Future<SSHSession> open;
  @override
  Future<SSHSession> execute(
    String command, {
    SSHPtyConfig? pty,
    Map<String, String>? environment,
  }) {
    expect(command, '/bin/sh');
    expect(
      pty,
      isNull,
      reason: 'polling must not start an interactive login shell',
    );
    return open;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _ProcessSession implements SSHSession {
  _ProcessSession(this.process);
  final Process process;
  @override
  Stream<Uint8List> get stdout => process.stdout.map(Uint8List.fromList);
  @override
  Stream<Uint8List> get stderr => process.stderr.map(Uint8List.fromList);
  @override
  void write(Uint8List data) => process.stdin.add(data);
  @override
  void close() {
    process.kill();
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

void main() {
  test(
    'markers round-trip through a real non-interactive POSIX shell',
    () async {
      final process = await Process.start('/bin/sh', []);
      final shell = PersistentShell(
        _Transport(Future.value(_ProcessSession(process))),
      );
      try {
        await shell.start();
        expect(await shell.exec('printf first'), 'first');
        expect(await shell.exec(r"printf '\001META\001\n'"), '\x01META\x01');
        expect(await shell.exec('printf second'), 'second');
      } finally {
        await shell.dispose();
        await process.exitCode;
      }
    },
    skip: Platform.isWindows,
  );

  test(
    'timeout invalidates the shell so late output cannot satisfy a new command',
    () async {
      final session = _Session();
      final shell = PersistentShell(_Transport(Future.value(session)));
      final started = shell.start();
      await started;
      expect(
        session.writes,
        0,
        reason: 'no sleeps or interactive shell initialization',
      );
      final failure = expectLater(
        shell.exec('mutation', timeout: const Duration(milliseconds: 50)),
        throwsA(
          isA<PersistentShellError>().having(
            (e) => e.mayHaveExecuted,
            'mayHaveExecuted',
            isTrue,
          ),
        ),
      );
      await failure;
      expect(session.closes, 1);
      expect(shell.isStarted, isFalse);
      final rejected = expectLater(
        shell.exec('another'),
        throwsA(isA<PersistentShellError>()),
      );
      await rejected;
      expect(session.writes, 1);
    },
  );

  test('late shell creation is cleaned up after timeout', () async {
    final opening = Completer<SSHSession>();
    final session = _Session();
    final shell = PersistentShell(_Transport(opening.future));
    final failure = expectLater(
      shell.start(timeout: const Duration(milliseconds: 50)),
      throwsA(isA<PersistentShellError>()),
    );
    await failure;
    opening.complete(session);
    await Future<void>.delayed(Duration.zero);
    expect(session.closes, 1);
    expect(shell.isStarted, isFalse);
  });
}
