import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/transcript/event_card.dart';

import '../helpers/test_helpers.dart';

// #374 — the system-agent card's task_* renderers. claude-code 2.1.x
// fires task_progress per tool-use inside background/subagent tasks;
// before the arm existed the card fell through to a raw JSON dump
// (uuid / session_id / tool_use_id / usage) that flooded the mobile
// feed. Unknown task_* subtypes must now degrade to a one-liner too,
// so the next new subtype can't re-flood it.
void main() {
  // Matches Text + SelectableText (the card body uses both).
  Finder containingText(String s) => find.byWidgetPredicate((w) {
        if (w is Text) {
          return (w.data ?? w.textSpan?.toPlainText() ?? '').contains(s);
        }
        if (w is SelectableText) {
          return (w.data ?? w.textSpan?.toPlainText() ?? '').contains(s);
        }
        return false;
      });

  Future<void> pumpCard(WidgetTester tester, Map<String, dynamic> event) {
    return tester.pumpWidget(
      MaterialApp(
        localizationsDelegates: testLocalizationsDelegates,
        supportedLocales: testSupportedLocales,
        home: Scaffold(
          body: SizedBox(width: 420, child: AgentEventCard(event: event)),
        ),
      ),
    );
  }

  Future<void> expand(WidgetTester tester) async {
    // system cards default to collapsed; tapping the header row (the
    // kind label sits inside the toggle InkWell) reveals the body.
    await tester.tap(find.text('system'));
    await tester.pumpAndSettle();
  }

  testWidgets('task_progress renders a one-liner, not the raw frame',
      (tester) async {
    await pumpCard(tester, {
      'kind': 'system',
      'producer': 'agent',
      'payload': {
        'subtype': 'task_progress',
        'task_id': 'brgb0gz57',
        'tool_use_id': 'toolu_9',
        'description': 'investigate stale sessions',
        'last_tool_name': 'Bash',
        'usage': {'total_tokens': 1234},
        'uuid': 'u-9',
        'session_id': 'sess-abc',
      },
    });
    await expand(tester);
    expect(containingText('Task in progress'), findsWidgets);
    expect(containingText('investigate stale sessions'), findsWidgets);
    expect(containingText('Bash'), findsWidgets);
    // The raw-frame noise fields must NOT reach the card body.
    expect(containingText('session_id'), findsNothing);
    expect(containingText('tool_use_id'), findsNothing);
    expect(containingText('uuid'), findsNothing);
  });

  testWidgets('task_progress without description falls back to the label',
      (tester) async {
    await pumpCard(tester, {
      'kind': 'system',
      'producer': 'agent',
      'payload': {'subtype': 'task_progress', 'task_id': 't1'},
    });
    await expand(tester);
    expect(containingText('Task in progress'), findsWidgets);
    expect(containingText('t1'), findsWidgets);
  });

  testWidgets('unknown task_* subtype degrades to a subtype one-liner',
      (tester) async {
    await pumpCard(tester, {
      'kind': 'system',
      'producer': 'agent',
      'payload': {
        'subtype': 'task_completed',
        'task_id': 'x1',
        'uuid': 'u-1',
        'session_id': 'sess-1',
        'usage': {'total_tokens': 5},
      },
    });
    await expand(tester);
    expect(containingText('task_completed'), findsWidgets);
    expect(containingText('x1'), findsWidgets);
    expect(containingText('uuid'), findsNothing);
    expect(containingText('session_id'), findsNothing);
  });

  testWidgets('task_started full frame previews its description, not "{"',
      (tester) async {
    await pumpCard(tester, {
      'kind': 'system',
      'producer': 'agent',
      'payload': {
        'subtype': 'task_started',
        'task_id': 't2',
        'agent': 'researcher',
        'description': 'investigate stale sessions',
      },
    });
    // Still collapsed: the preview line should already read human.
    expect(containingText('investigate stale sessions'), findsWidgets);
    expect(containingText('{'), findsNothing);
  });
}
