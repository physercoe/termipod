import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:dartssh2/dartssh2.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/ssh/work_user_shell.dart';
import 'package:termipod/services/ssh/ssh_client.dart';

class _Session implements SSHSession {
  final out = StreamController<Uint8List>();
  final err = StreamController<Uint8List>();
  final writes = <String>[];
  int closes = 0;
  void Function(String)? onWrite;

  void emit(String value) => out.add(Uint8List.fromList(utf8.encode(value)));
  @override
  Stream<Uint8List> get stdout => out.stream;
  @override
  Stream<Uint8List> get stderr => err.stream;
  @override
  void write(Uint8List bytes) {
    final value = utf8.decode(bytes);
    writes.add(value);
    onWrite?.call(value);
  }

  @override
  void close() => closes++;
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _Transport implements SSHClient {
  _Transport(this.open);
  final Future<SSHSession> open;
  String? command;
  String get nonce =>
      RegExp(r'MP_([a-f0-9]+)_READY').firstMatch(command!)!.group(1)!;

  @override
  Future<SSHSession> execute(
    String value, {
    SSHPtyConfig? pty,
    Map<String, String>? environment,
  }) {
    command = value;
    expect(pty, isNotNull);
    expect(value, startsWith('stty -echo || exit; LC_ALL=C su - '));
    expect(value, isNot(contains('private-password')));
    return open;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _ProcessSession implements SSHSession {
  _ProcessSession(this.process, this.transcript);
  final StringBuffer transcript;
  final Process process;
  @override
  Stream<Uint8List> get stdout => process.stdout.map((bytes) {
    transcript.write(utf8.decode(bytes, allowMalformed: true));
    return Uint8List.fromList(bytes);
  });
  @override
  Stream<Uint8List> get stderr => process.stderr.map(Uint8List.fromList);
  @override
  void write(Uint8List bytes) => process.stdin.add(bytes);
  @override
  void close() => unawaited(process.stdin.close());
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _LoginSession implements SSHSession {
  @override
  Stream<Uint8List> get stdout =>
      Stream.value(Uint8List.fromList(utf8.encode('login-A')));
  @override
  Stream<Uint8List> get stderr => const Stream.empty();
  @override
  int get exitCode => 0;
  @override
  void close() {}
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _PtyTransport implements SSHClient {
  _PtyTransport(this.directory);
  final Directory directory;
  Process? process;
  final transcript = StringBuffer();
  int opens = 0;
  final ended = Completer<void>();
  @override
  bool isClosed = false;
  @override
  Future<void> get authenticated async {}
  @override
  Future<void> get done => ended.future;
  @override
  Future<void> ping() async {}
  @override
  void close() {
    isClosed = true;
    if (!ended.isCompleted) ended.complete();
  }

  @override
  Future<SSHSession> execute(
    String command, {
    SSHPtyConfig? pty,
    Map<String, String>? environment,
  }) async {
    opens++;
    if (pty == null) {
      expect(
        command,
        'printf login-A',
        reason: 'Only explicit general SSH commands run as A',
      );
      return _LoginSession();
    }
    expect(pty, isNotNull, reason: 'Never fall back to an SSH command as A');
    process = await Process.start('python3', [
      '-u',
      '-c',
      r'''
import os, pty, select, subprocess, sys
master, slave = pty.openpty()
child = subprocess.Popen(['/bin/sh', '-c', sys.argv[1]],
    stdin=slave, stdout=slave, stderr=slave,
    env={**os.environ, 'PATH': sys.argv[2] + ':' + os.environ.get('PATH', '')})
os.close(slave)
try:
    while True:
        readable, _, _ = select.select([master, 0], [], [])
        if master in readable:
            try:
                data = os.read(master, 65536)
            except OSError:
                break
            if not data:
                break
            os.write(1, data)
        if 0 in readable:
            data = os.read(0, 65536)
            if not data:
                break
            while data:
                written = os.write(master, data)
                data = data[written:]
finally:
    os.close(master)
    child.kill()
    child.wait()
''',
      command,
      directory.path,
    ]);
    return _ProcessSession(process!, transcript);
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _Socket implements SSHSocket {
  @override
  void destroy() {}
  @override
  Future<void> close() async {}
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

Future<void> _tick() => Future<void>.delayed(Duration.zero);

Future<WorkUserShell> _authenticated(
  _Transport transport,
  _Session session,
) async {
  final shell = WorkUserShell(transport);
  final opening = shell.start(username: 'worker', password: 'private-password');
  await _tick();
  session.emit('Pass');
  session.emit('word: ');
  await _tick();
  expect(session.writes, ['private-password\n']);
  session.emit('\x01MP_${transport.nonce}_READY:worker\x01');
  await opening;
  return shell;
}

void main() {
  test(
    'SSH login remains A while discovery, capture and input use one authenticated B channel',
    () async {
      final directory = await Directory.systemTemp.createTemp(
        'termipod-su-client-',
      );
      final fakeSu = File('${directory.path}/su');
      await fakeSu.writeAsString(r'''#!/usr/bin/env python3
import os, sys
sys.stdout.write('Password: ')
sys.stdout.flush()
if sys.stdin.readline().rstrip('\n') != 'private-password':
    sys.exit(1)
os.execv('/bin/sh', ['/bin/sh', '-c', sys.argv[-1]])
''');
      final fakeTmux = File('${directory.path}/tmux');
      await fakeTmux.writeAsString('#!/bin/sh\nprintf "%s" "\$1"\n');
      await Process.run('chmod', ['+x', fakeSu.path, fakeTmux.path]);
      for (var attempt = 0; attempt < 2; attempt++) {
        final transport = _PtyTransport(directory);
        final client = SshClient(
          socketConnector: (_, _, _) async => _Socket(),
          transportFactory: (_, user, _) {
            expect(user, 'login-A');
            return transport;
          },
        );
        try {
          await client.connect(
            host: 'host',
            port: 22,
            username: 'login-A',
            options: SshConnectOptions(
              password: 'ssh-password',
              workUsername: Platform.environment['USER'],
              workPassword: 'private-password',
              tmuxPath: fakeTmux.path,
            ),
          );
          expect(
            transport.opens,
            0,
            reason: 'SSH authentication is independent of terminal setup',
          );
          await client.prepareTerminal();
          await client.prepareTerminal();
          expect(
            await client.execTerminal('tmux list-sessions'),
            'list-sessions',
          );
          expect(
            await client.execPersistent('tmux capture-pane'),
            'capture-pane',
          );
          expect(
            (await client.execTerminalWithExitCode('tmux send-keys')).exitCode,
            0,
          );
          expect(transport.opens, 1);
          expect(await client.exec('printf login-A'), 'login-A');
          expect(
            transport.transcript.toString(),
            isNot(contains('private-password')),
          );
          await expectLater(
            client.execTerminal(
              'sleep 1',
              timeout: const Duration(milliseconds: 30),
            ),
            throwsA(isA<SshConnectionError>()),
          );
          await expectLater(
            client.execTerminal('tmux send-keys'),
            throwsA(isA<SshConnectionError>()),
          );
          expect(
            transport.opens,
            2,
            reason: 'A dead B channel must not execute as A',
          );
        } finally {
          await client.dispose();
          if (transport.process != null) await transport.process!.exitCode;
        }
      }
      await directory.delete(recursive: true);
    },
    skip: Platform.isWindows,
  );

  test(
    'real PTY suppresses password echo and supports input beyond canonical limits',
    () async {
      final directory = await Directory.systemTemp.createTemp(
        'termipod-su-test-',
      );
      final fakeSu = File('${directory.path}/su');
      await fakeSu.writeAsString(r'''#!/usr/bin/env python3
import os, sys
sys.stdout.write('Password: ')
sys.stdout.flush()
if sys.stdin.readline().rstrip('\n') != 'private-password':
    sys.exit(1)
os.execv('/bin/sh', ['/bin/sh', '-c', sys.argv[-1]])
''');
      await Process.run('chmod', ['+x', fakeSu.path]);
      final transport = _PtyTransport(directory);
      final shell = WorkUserShell(transport);
      try {
        await shell.start(
          username: Platform.environment['USER']!,
          password: 'private-password',
        );
        final longText = '世界' * 8000;
        final result = await shell.run("printf '%s' '$longText'");
        expect(result.stdout, longText);
        expect(result.exitCode, 0);
        expect(
          transport.transcript.toString(),
          isNot(contains('private-password')),
        );
        expect(
          (await shell.run("printf '\\033[31mred\\033[0m\\n'")).stdout,
          '\x1b[31mred\x1b[0m\n',
        );
      } finally {
        await shell.dispose();
        if (transport.process != null) await transport.process!.exitCode;
        await directory.delete(recursive: true);
      }
    },
    skip: Platform.isWindows,
  );

  test(
    'fragmented prompt authenticates once and verifies working identity',
    () async {
      final session = _Session();
      final transport = _Transport(Future.value(session));
      final shell = await _authenticated(transport, session);
      final result = shell.run('tmux list-sessions');
      await _tick();
      session.emit('\x01MP_${transport.nonce}_1_START\x01hello\r\n');
      session.emit('\x01MP_${transport.nonce}_1_END:');
      session.emit('7\x01');
      expect(await result, (stdout: 'hello\r\n', stderr: '', exitCode: 7));
      expect(
        session.writes.where((s) => s.contains('private-password')),
        hasLength(1),
      );
      await shell.dispose();
    },
  );

  test(
    'wrong identity and repeated password prompt fail without retrying',
    () async {
      for (final wrongIdentity in [true, false]) {
        final session = _Session();
        final transport = _Transport(Future.value(session));
        final shell = WorkUserShell(transport);
        final opening = shell.start(
          username: 'worker',
          password: 'private-password',
        );
        final failure = expectLater(
          opening,
          throwsA(isA<WorkUserAuthenticationError>()),
        );
        await _tick();
        session.emit('Password: ');
        await _tick();
        session.emit(
          wrongIdentity
              ? '\x01MP_${transport.nonce}_READY:login\x01'
              : 'Password: ',
        );
        await failure;
        expect(session.writes, ['private-password\n']);
        expect(session.closes, 1);
        await expectLater(shell.run('tmux new-session'), throwsStateError);
      }
    },
  );

  test('cancelled authentication releases channels which open late', () async {
    final late = Completer<SSHSession>();
    final shell = WorkUserShell(_Transport(late.future));
    final opening = shell.start(
      username: 'worker',
      password: 'private-password',
    );
    final failure = expectLater(
      opening,
      throwsA(isA<WorkUserAuthenticationError>()),
    );
    await shell.dispose();
    await failure;
    final session = _Session();
    late.complete(session);
    await _tick();
    expect(session.closes, 1);
    expect(session.writes, isEmpty);
  });

  test(
    'queued timeout never sends a mutation and active timeout closes channel',
    () async {
      final session = _Session();
      final transport = _Transport(Future.value(session));
      final shell = await _authenticated(transport, session);
      final first = shell.run(
        'first',
        timeout: const Duration(milliseconds: 100),
      );
      final firstFailure = expectLater(first, throwsA(isA<TimeoutException>()));
      final second = shell.run(
        'must-not-run',
        timeout: const Duration(milliseconds: 20),
      );
      await expectLater(second, throwsA(isA<TimeoutException>()));
      expect(
        session.writes,
        hasLength(2),
      ); // Password and only the first command.
      await firstFailure;
      expect(session.closes, 1);
      await expectLater(shell.run('retry'), throwsStateError);
    },
  );

  test(
    'framing round trips quotes, multiline Unicode, exit codes and long input through POSIX shell',
    () async {
      final session = _Session();
      final transport = _Transport(Future.value(session));
      final shell = await _authenticated(transport, session);
      final process = await Process.start('/bin/sh', [
        '-c',
        r'while IFS= read -r line; do eval "$line"; done',
      ]);
      final output = process.stdout.listen(
        (bytes) => session.out.add(Uint8List.fromList(bytes)),
      );
      final error = process.stderr.listen(
        (bytes) => session.out.add(Uint8List.fromList(bytes)),
      );
      session.onWrite = (value) => process.stdin.add(utf8.encode(value));
      try {
        final result = await shell.run(
          "printf '%s\\n' 'hello 世界' \"quote'\"; printf second; exit 9",
        );
        expect(result.stdout, "hello 世界\nquote'\nsecond");
        expect(result.exitCode, 9);
        final longText = 'λ' * 8000;
        expect(
          (await shell.run("printf '%s' '$longText'\nprintf tail")).stdout,
          '${longText}tail',
        );
        expect((await shell.run('printf next')).stdout, 'next');
      } finally {
        await shell.dispose();
        process.kill();
        await process.exitCode;
        await output.cancel();
        await error.cancel();
      }
    },
    skip: Platform.isWindows,
  );
}
