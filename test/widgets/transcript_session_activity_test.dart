// Agent-transcript-redesign §6 P2 state-dock tests (decision §7.5).
//
//   (a) `sessionActivityFromEvents` — the pure derivation behind the dock:
//       todos from the NEWEST plan snapshot (done/total), shell/sub-agent
//       name matching (bare / case / MCP-suffix), per-call status from the
//       SAME toolCallDisplayStatus lineage the P1 cards use, and the
//       running-first + cap-20 list ordering.
//   (b) chip visibility rules — Tasks/Sub-agents only while running > 0,
//       Todos whenever a plan event exists.
//   (c) lens invariance — the derivation reads only state-bearing kinds
//       from the FULL list, so dropping non-state kinds (what a lens does)
//       cannot move the counts.
//   (d) widget tests — chip tap opens the modal sheet, lists render, the
//       segmented switcher swaps kinds, barrier tap closes.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/l10n/app_localizations.dart';
import 'package:termipod/theme/app_theme.dart';
import 'package:termipod/widgets/app_chip.dart';
import 'package:termipod/widgets/transcript/fold_maps.dart';
import 'package:termipod/widgets/transcript/session_activity.dart';
import 'package:termipod/widgets/transcript/session_activity_dock.dart';

Map<String, dynamic> _ev(String kind, {Map<String, dynamic>? payload}) => {
  'kind': kind,
  'payload': payload ?? const <String, dynamic>{},
};

Map<String, dynamic> _call(
  String id, {
  String name = 'Bash',
  String? status,
  Map<String, dynamic>? input,
}) => _ev(
  'tool_call',
  payload: {
    'id': id,
    'name': name,
    if (status != null) 'status': status,
    if (input != null) 'input': input,
  },
);

Map<String, dynamic> _result(String id, {bool isError = false}) =>
    _ev('tool_result', payload: {'tool_use_id': id, 'is_error': isError});

Map<String, dynamic> _update(String id, String status) =>
    _ev('tool_call_update', payload: {'toolCallId': id, 'status': status});

Map<String, dynamic> _plan(List<Map<String, String>> entries) =>
    _ev('plan', payload: {'entries': entries});

SessionActivity _activity(List<Map<String, dynamic>> events) =>
    sessionActivityFromEvents(events, FoldMaps.fromEvents(events));

void main() {
  group('sessionActivityFromEvents — todos (newest plan snapshot wins)', () {
    test('two plan events: the newer snapshot replaces the older outright', () {
      final a = _activity([
        _plan([
          {'content': 'step 1', 'status': 'pending'},
        ]),
        _plan([
          {'content': 'step 1', 'status': 'completed'},
          {'content': 'step 2', 'status': 'in_progress'},
        ]),
      ]);
      expect(a.todos, isNotNull);
      expect(a.todos!.total, 2);
      expect(a.todos!.done, 1);
      expect(a.todos!.items.map((t) => t.status), ['completed', 'in_progress']);
    });

    test('done/total counting across all three statuses', () {
      final a = _activity([
        _plan([
          {'content': 'a', 'status': 'completed'},
          {'content': 'b', 'status': 'in_progress'},
          {'content': 'c', 'status': 'pending'},
          {'content': 'd', 'status': 'completed'},
        ]),
      ]);
      expect(a.todos!.done, 2);
      expect(a.todos!.total, 4);
      expect(a.todos!.items.map((t) => t.content), ['a', 'b', 'c', 'd']);
    });

    test('no plan event → todos null (chip hides); empty entries → 0/0 '
        'but the chip still shows', () {
      expect(_activity(const []).todos, isNull);
      expect(_activity([_call('s1', status: 'pending')]).todos, isNull);

      final empty = _activity([_plan(const [])]);
      expect(empty.todos, isNotNull);
      expect(empty.todos!.done, 0);
      expect(empty.todos!.total, 0);
    });
  });

  group('sessionActivityFromEvents — name matching', () {
    String shell = 'shellRunning', sub = 'subagentRunning';

    Map<String, int> countsFor(List<String> names) {
      final a = _activity([
        for (var i = 0; i < names.length; i++)
          _call('t$i', name: names[i], status: 'pending'),
      ]);
      return {shell: a.shellRunning, sub: a.subagentRunning};
    }

    test('bare names, any case', () {
      expect(countsFor(['Bash']), {shell: 1, sub: 0});
      expect(countsFor(['bash']), {shell: 1, sub: 0});
      expect(countsFor(['BASH']), {shell: 1, sub: 0});
      expect(countsFor(['Agent']), {shell: 0, sub: 1});
      expect(countsFor(['task']), {shell: 0, sub: 1});
      expect(countsFor(['TASK']), {shell: 0, sub: 1});
    });

    test('the full locked shell set matches', () {
      expect(
        countsFor([
          'bash',
          'shell',
          'exec',
          'exec_command',
          'run_shell_command',
          'execute_command',
        ]),
        {shell: 6, sub: 0},
      );
    });

    test('mcp__<server>__<name> suffix form matches by tool leaf', () {
      expect(countsFor(['mcp__tools__Bash']), {shell: 1, sub: 0});
      expect(countsFor(['mcp__server__exec_command']), {shell: 1, sub: 0});
      expect(countsFor(['mcp__x__task']), {shell: 0, sub: 1});
      expect(countsFor(['MCP__X__AGENT']), {shell: 0, sub: 1});
    });

    test('non-matching tools are excluded — no substring/prefix bleed', () {
      expect(
        countsFor([
          'Read',
          'Grep',
          'TodoWrite',
          'BashOutput', // contains the leaf but isn't it
          'Taskforce', // ditto
          'mcp__bash', // degenerate MCP form without a server segment
        ]),
        {shell: 0, sub: 0},
      );
    });
  });

  group('sessionActivityFromEvents — status via the fold lineage', () {
    test('a paired result resolves done / error when no status exists', () {
      final done = _activity([_call('s1'), _result('s1')]);
      expect(done.shellCalls.single.status, SessionTaskStatus.done);
      expect(done.shellRunning, 0);

      final failed = _activity([_call('s1'), _result('s1', isError: true)]);
      expect(failed.shellCalls.single.status, SessionTaskStatus.error);
      expect(failed.shellRunning, 0);
    });

    test('log-tail claude-code calls pair via tool_use_id (no id key)', () {
      // The local-log-tail claude mapper writes a tool_call's id as
      // tool_use_id with no 'id' key (fold_maps.callToolIdOf); the result
      // keys on the same value. Without the shared helper the dock spins
      // "running" forever on watched local sessions.
      final a = _activity([
        {
          'kind': 'tool_call',
          'payload': {
            'tool_use_id': 'lt1',
            'name': 'Bash',
            'input': {'command': 'make'},
          },
        },
        {
          'kind': 'tool_result',
          'payload': {'tool_use_id': 'lt1', 'is_error': false},
        },
      ]);
      expect(a.shellCalls.single.status, SessionTaskStatus.done);
      expect(a.shellRunning, 0);
    });

    test('an update status wins over the creation-frame status', () {
      final running = _activity([
        _call('s1', status: 'pending'),
        _update('s1', 'in_progress'),
      ]);
      expect(running.shellCalls.single.status, SessionTaskStatus.running);

      final done = _activity([
        _call('s1', status: 'pending'),
        _update('s1', 'completed'),
      ]);
      expect(done.shellCalls.single.status, SessionTaskStatus.done);

      final failed = _activity([
        _call('s1', status: 'in_progress'),
        _update('s1', 'failed'),
      ]);
      expect(failed.shellCalls.single.status, SessionTaskStatus.error);
    });

    test('no lineage at all reads running (pending)', () {
      final a = _activity([_call('s1')]);
      expect(a.shellCalls.single.status, SessionTaskStatus.running);
      expect(a.shellRunning, 1);
    });

    test('sub-agent calls resolve through the same derivation', () {
      final a = _activity([
        _call('a1', name: 'Agent', status: 'pending'),
        _call('a2', name: 'Task'),
        _result('a2'),
      ]);
      expect(a.subagentRunning, 1);
      expect(a.subagentCalls.map((c) => c.status), [
        SessionTaskStatus.running,
        SessionTaskStatus.done,
      ]);
    });
  });

  group('sessionActivityFromEvents — ordering + cap', () {
    test('running first (event order), then terminal newest-first', () {
      final a = _activity([
        _call('s1'), // done (older)
        _result('s1'),
        _call('s2'), // done (newer)
        _result('s2'),
        _call('s3', status: 'in_progress'), // running
      ]);
      expect(a.shellCalls.map((c) => c.id), ['s3', 's2', 's1']);
    });

    test('the list caps at 20; the running count does not', () {
      final a = _activity([
        for (var i = 0; i < 25; i++) _call('s$i', status: 'pending'),
      ]);
      expect(a.shellRunning, 25);
      expect(a.shellCalls, hasLength(kSessionActivityCallCap));
    });
  });

  group('chip visibility rules', () {
    test('empty session hides everything', () {
      final a = _activity(const []);
      expect(a.showTasks, isFalse);
      expect(a.showSubagents, isFalse);
      expect(a.showTodos, isFalse);
      expect(a.isEmpty, isTrue);
    });

    test('Tasks chip iff shellRunning > 0 — a finished task earns none', () {
      final running = _activity([_call('s1', status: 'in_progress')]);
      expect(running.showTasks, isTrue);
      expect(running.showSubagents, isFalse);

      final done = _activity([_call('s1'), _result('s1')]);
      expect(done.showTasks, isFalse);
      expect(done.isEmpty, isTrue);
    });

    test('Sub-agents chip iff subagentRunning > 0', () {
      final running = _activity([_call('a1', name: 'Task')]);
      expect(running.showSubagents, isTrue);

      final done = _activity([_call('a1', name: 'Task'), _result('a1')]);
      expect(done.showSubagents, isFalse);
    });

    test('Todos chip iff a plan event exists', () {
      final a = _activity([_plan(const [])]);
      expect(a.showTodos, isTrue);
      expect(a.isEmpty, isFalse);
    });
  });

  group('lens invariance', () {
    test('dropping non-state kinds cannot move the counts', () {
      // The full session list — what LiveFeed passes (_events, NOT the
      // lens-filtered list).
      final full = [
        _ev('text', payload: {'text': 'working…'}),
        _call('s1', status: 'in_progress', input: {'command': 'npm test'}),
        _call('s2'),
        _result('s2'),
        _call('a1', name: 'mcp__x__task'),
        _ev('completion', payload: {'subtype': 'done'}),
        _plan([
          {'content': 'a', 'status': 'completed'},
          {'content': 'b', 'status': 'pending'},
        ]),
        _ev('thought', payload: {'text': 'hmm'}),
      ];
      // A "lensed" window keeps only some kinds. The derivation reads only
      // state-bearing kinds (tool_call + plan, lineage via FoldMaps), so
      // any subset that keeps them derives the SAME state — this is why a
      // lens change can't move the chips as long as the input is the full
      // list.
      final stateBearingOnly = [
        for (final e in full)
          if (const {
            'tool_call',
            'tool_result',
            'plan',
          }.contains((e['kind'] ?? '').toString()))
            e,
      ];
      final a = _activity(full);
      final b = _activity(stateBearingOnly);
      expect(b.shellRunning, a.shellRunning);
      expect(b.subagentRunning, a.subagentRunning);
      expect(b.shellCalls.map((c) => c.id), a.shellCalls.map((c) => c.id));
      expect(
        b.subagentCalls.map((c) => c.id),
        a.subagentCalls.map((c) => c.id),
      );
      expect(b.todos!.done, a.todos!.done);
      expect(b.todos!.total, a.todos!.total);
      expect(
        b.todos!.items.map((t) => t.content),
        a.todos!.items.map((t) => t.content),
      );
    });
  });

  group('SessionActivityStrip + sheet (widget)', () {
    // One running shell call, one running sub-agent, a 2/3 plan → all
    // three chips visible.
    final events = [
      _call('s1', status: 'in_progress', input: {'command': 'npm test'}),
      _call(
        'a1',
        name: 'Task',
        status: 'pending',
        input: {'description': 'scout the repo'},
      ),
      _plan([
        {'content': 'write the reducer', 'status': 'completed'},
        {'content': 'wire the sheet', 'status': 'completed'},
        {'content': 'run the tests', 'status': 'pending'},
      ]),
    ];

    Future<void> pump(WidgetTester t, SessionActivity activity) => t.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark,
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        home: Scaffold(body: SessionActivityStrip(activity: activity)),
      ),
    );

    // The running rows carry an always-animating CircularProgressIndicator
    // (the shared status glyph), so pumpAndSettle never settles — step the
    // clock past the modal route animation instead.
    Future<void> settle(WidgetTester t) async {
      await t.pump();
      await t.pump(const Duration(milliseconds: 400));
    }

    testWidgets('chips render per the visibility rules; empty hides all', (
      t,
    ) async {
      await pump(t, _activity(events));
      expect(find.text('Tasks (1)'), findsOneWidget);
      expect(find.text('Sub-agents (1)'), findsOneWidget);
      expect(find.text('Todos (2/3)'), findsOneWidget);

      await pump(t, _activity(const []));
      expect(find.byType(AppStatusChip), findsNothing);
    });

    testWidgets('chip tap opens the sheet; switcher swaps kinds; barrier '
        'closes', (t) async {
      await pump(t, _activity(events));

      // Tap the Tasks chip → sheet opens on the Tasks list.
      await t.tap(find.text('Tasks (1)'));
      await settle(t);
      // Segmented switcher (exact labels — the chips carry counts, so no
      // collision) + the running shell row.
      expect(find.text('Tasks'), findsOneWidget);
      expect(find.text('Sub-agents'), findsOneWidget);
      expect(find.text('Todos'), findsOneWidget);
      expect(find.text('Bash'), findsOneWidget);
      expect(find.text('npm test'), findsOneWidget);
      expect(find.byType(CircularProgressIndicator), findsOneWidget);

      // Switcher → Sub-agents: the sub-agent row replaces the shell row.
      await t.tap(find.text('Sub-agents'));
      await settle(t);
      expect(find.text('Task'), findsOneWidget);
      expect(find.text('scout the repo'), findsOneWidget);
      expect(find.text('npm test'), findsNothing);

      // Switcher → Todos: the plan snapshot rows (shared glyphs: done
      // check ×2, hollow pending ×1).
      await t.tap(find.text('Todos'));
      await settle(t);
      expect(find.text('write the reducer'), findsOneWidget);
      expect(find.text('run the tests'), findsOneWidget);
      expect(find.byIcon(Icons.check_circle_outline), findsNWidgets(2));
      expect(find.byIcon(Icons.radio_button_unchecked), findsOneWidget);

      // Barrier tap dismisses the modal; the strip (and lens) stay put.
      await t.tapAt(const Offset(400, 40));
      await settle(t);
      expect(find.text('write the reducer'), findsNothing);
      expect(find.text('Tasks (1)'), findsOneWidget);
    });

    testWidgets('opening from the Todos chip lands on the Todos segment', (
      t,
    ) async {
      await pump(t, _activity(events));
      await t.tap(find.text('Todos (2/3)'));
      await settle(t);
      expect(find.text('write the reducer'), findsOneWidget);
      expect(find.text('npm test'), findsNothing);
    });
  });
}
