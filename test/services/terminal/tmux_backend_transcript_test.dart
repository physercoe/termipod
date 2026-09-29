import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/ssh/ssh_client.dart';
import 'package:termipod/services/terminal/tmux_backend.dart';

class _TranscriptClient extends SshClient {
  String command = 'codex';
  bool alternate = true;
  String mode = '';
  int historySize = 500;
  int page = 0;
  Completer<String>? pendingPoll;
  Completer<String>? pendingInput;
  final captures = <String>[];
  final inputs = <String>[];

  @override
  bool get isConnected => true;

  @override
  Future<String> execPersistent(String command, {Duration? timeout}) async {
    captures.add(command);
    if (pendingPoll != null) return pendingPoll!.future;
    final lines = List.generate(40, (i) => 'page $page row $i').join('\n');
    return '$lines\n${TmuxBackend.pollMetaDelimiter}\n'
        '0,39,80,40,$historySize,${alternate ? 1 : 0},${this.command}\n$mode\n';
  }

  @override
  Future<String> exec(String command, {Duration? timeout}) async {
    inputs.add(command);
    if (command.endsWith('PPage"')) page--;
    if (command.endsWith('NPage"')) page++;
    if (command.endsWith('C-End"')) page = 0;
    return pendingInput == null ? '' : await pendingInput!.future;
  }
}

void main() {
  late _TranscriptClient client;
  late TmuxBackend backend;
  String? target;

  setUp(() {
    target = '%1';
    client = _TranscriptClient();
    backend = TmuxBackend(
      sshClient: client,
      getCurrentTarget: () => target,
      scrollbackLines: 250,
    );
  });
  tearDown(() => backend.dispose());

  void testBackend(String name, Future<void> Function(WidgetTester) body) {
    testWidgets(name, (tester) async {
      try {
        await body(tester);
      } finally {
        // Stop scheduling before Flutter checks pending fake-async timers.
        backend.pausePolling();
      }
    });
  }

  Future<void> poll(WidgetTester tester) async {
    backend.boostRefresh();
    await tester.pump(const Duration(milliseconds: 100));
  }

  testBackend(
    'fullscreen history pages remotely instead of inflating capture',
    (tester) async {
      await poll(tester);
      expect(backend.supportsTranscriptPaging, isTrue);
      expect(backend.scrollbackSize, 0);
      expect(backend.currentContent.split('\n'), hasLength(40));
      expect(await backend.extendScrollback(250), 0);
      expect(backend.scrollbackLines, 250);
      for (var i = 0; i < 8; i++) {
        await backend.pageTranscript(older: true);
        await poll(tester);
      }
      expect(backend.currentContent, startsWith('page -8 row 0'));
      expect(client.captures, everyElement(isNot(contains('-S'))));
      await backend.pageTranscript(older: false);
      await poll(tester);
      expect(backend.currentContent, startsWith('page -7 row 0'));
      await backend.jumpToLatestTranscript();
      await poll(tester);
      expect(backend.currentContent, startsWith('page 0 row 0'));
      expect(client.inputs.last, contains('"send-keys -t %1 C-End"'));
      expect(client.inputs.last, startsWith('tmux if-shell -F -t %1'));
    },
  );

  testBackend(
    'ordinary shell and inline Codex still capture configured history',
    (tester) async {
      client.alternate = false;
      await poll(tester);
      await poll(tester);
      expect(client.captures.last, contains('-S -250'));
      expect(backend.supportsTranscriptPaging, isFalse);
      expect(backend.scrollbackSize, 500);
      await backend.pageTranscript(older: true);
      expect(client.inputs, isEmpty);
      expect(await backend.extendScrollback(250), 250);
    },
  );

  testBackend(
    'editors, shells, missing commands and copy-mode never receive transcript keys',
    (tester) async {
      for (final command in ['vim', 'bash', 'node', '']) {
        client.command = command;
        await poll(tester);
        expect(backend.supportsTranscriptPaging, isFalse);
        await backend.pageTranscript(older: true);
      }
      client.command = 'codex';
      client.mode = 'copy-mode';
      await poll(tester);
      expect(backend.supportsTranscriptPaging, isFalse);
      await backend.jumpToLatestTranscript();
      expect(client.inputs, isEmpty);
    },
  );

  testBackend('metadata changes notify even when screen text is identical', (
    tester,
  ) async {
    var updates = 0;
    final sub = backend.contentUpdates.listen((_) => updates++);
    addTearDown(sub.cancel);
    client.command = 'vim';
    await poll(tester);
    expect(updates, 1);
    client.command = 'codex';
    await poll(tester);
    expect(updates, 2);
    expect(backend.supportsTranscriptPaging, isTrue);
    client.mode = 'copy-mode';
    await poll(tester);
    expect(updates, 3);
    expect(backend.supportsTranscriptPaging, isFalse);
  });

  testBackend('pane switches and reconnects require fresh metadata', (
    tester,
  ) async {
    await poll(tester);
    target = '%2';
    await backend.pageTranscript(older: true);
    expect(client.inputs, isEmpty);
    await poll(tester);
    await backend.pageTranscript(older: true);
    expect(client.inputs.single, contains('"send-keys -t %2 PPage"'));
    expect(client.inputs.single, startsWith('tmux if-shell -F -t %2'));
    await backend.rebindSshClient(client);
    expect(backend.supportsTranscriptPaging, isFalse);
    await backend.pageTranscript(older: true);
    expect(client.inputs, hasLength(1));
  });

  testBackend('late capture from previous pane cannot enable paging', (
    tester,
  ) async {
    client.pendingPoll = Completer<String>();
    await poll(tester);
    target = '%2';
    client.pendingPoll!.complete(
      'old\n${TmuxBackend.pollMetaDelimiter}\n0,0,80,40,0,1,codex\n',
    );
    await tester.pump();
    expect(backend.currentContent, isEmpty);
    expect(backend.supportsTranscriptPaging, isFalse);
  });

  testBackend('slow input does not queue obsolete swipes', (tester) async {
    await poll(tester);
    client.pendingInput = Completer<String>();
    final first = backend.pageTranscript(older: true);
    await backend.pageTranscript(older: true);
    await backend.pageTranscript(older: false);
    expect(client.inputs, hasLength(1));
    client.pendingInput!.complete('');
    await first;
  });
}
