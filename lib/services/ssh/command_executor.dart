import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dartssh2/dartssh2.dart';

typedef CommandResult = ({String stdout, String stderr, int? exitCode});

/// Serializes command channels with a deadline covering queueing, channel
/// creation and output. A timed-out command must never execute later in queue.
class CommandExecutor {
  CommandExecutor(this._open);
  final Future<SSHSession> Function(String command) _open;
  Completer<void>? _lock;

  Future<CommandResult> run(
    String command, {
    Duration timeout = const Duration(seconds: 15),
  }) async {
    final clock = Stopwatch()..start();
    Duration remaining() {
      final left = timeout - clock.elapsed;
      if (left <= Duration.zero)
        throw TimeoutException('SSH command timed out');
      return left;
    }

    while (_lock != null) {
      await _lock!.future.timeout(remaining());
    }
    remaining();
    final lock = Completer<void>();
    _lock = lock;
    SSHSession? session;
    StreamSubscription<Uint8List>? stdoutSub;
    StreamSubscription<Uint8List>? stderrSub;
    try {
      final opening = _open(command);
      session = await opening.timeout(
        remaining(),
        onTimeout: () {
          // Future.timeout does not cancel channel creation. Close a channel
          // that arrives after its caller has stopped waiting.
          unawaited(
            opening.then((late) => late.close(), onError: (Object _) {}),
          );
          throw TimeoutException('SSH channel open timed out');
        },
      );
      final stdout = BytesBuilder(copy: false);
      final stderr = BytesBuilder(copy: false);
      final outDone = Completer<void>();
      final errDone = Completer<void>();
      stdoutSub = session.stdout.listen(
        stdout.add,
        onDone: () {
          if (!outDone.isCompleted) outDone.complete();
        },
        onError: (Object e, StackTrace s) {
          if (!outDone.isCompleted) outDone.completeError(e, s);
        },
      );
      stderrSub = session.stderr.listen(
        stderr.add,
        onDone: () {
          if (!errDone.isCompleted) errDone.complete();
        },
        onError: (Object e, StackTrace s) {
          if (!errDone.isCompleted) errDone.completeError(e, s);
        },
      );
      await Future.wait([
        outDone.future,
        errDone.future,
      ], eagerError: true).timeout(remaining());
      return (
        stdout: utf8.decode(stdout.takeBytes(), allowMalformed: true),
        stderr: utf8.decode(stderr.takeBytes(), allowMalformed: true),
        exitCode: session.exitCode,
      );
    } finally {
      session?.close();
      // Cancellation must not hold the command queue hostage to a remote peer.
      if (stdoutSub != null) unawaited(stdoutSub.cancel());
      if (stderrSub != null) unawaited(stderrSub.cancel());
      _lock = null;
      lock.complete();
    }
  }
}
