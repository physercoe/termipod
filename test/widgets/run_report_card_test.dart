import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/widgets/run_report_card.dart';

// P1 (agent-run-analysis-mode): the foldable run-report dashboard renders
// the session/agent digest. These pin the summary line, the expand/collapse
// behavior, and the error-stat jump callback so the analysis surface stays
// honest as the digest shape evolves.

Map<String, dynamic> _digest({int errors = 3}) => {
      'event_count': 11,
      'turn_count': 2,
      'duration_ms': 372000, // 6m12s
      'cost_usd': 0.12,
      'error_count': errors,
      'tool_total': 20,
      'tool_failed': 2,
      'outcome': 'done',
      'last_ts': '2026-06-01T12:00:00Z',
      'errors': {
        'tool_error': {'count': 2, 'sample_seqs': [9, 14]},
        'failed_turn': {'count': 1, 'sample_seqs': [11]},
      },
      'by_model': {
        'claude-x': {'in': 1200, 'out': 340},
      },
      'latency': {'p50_ms': 2000, 'p95_ms': 5000, 'samples': 2},
    };

Future<void> _pump(WidgetTester t, Widget child) => t.pumpWidget(
      MaterialApp(home: Scaffold(body: SingleChildScrollView(child: child))),
    );

void main() {
  group('RunReportCard', () {
    testWidgets('summary line shows outcome · turns · duration · cost',
        (t) async {
      await _pump(t, RunReportCard(digest: _digest()));
      expect(find.textContaining('done'), findsWidgets);
      expect(find.textContaining('2 turns'), findsOneWidget);
      // "6m12s" appears in both the summary line and the Duration stat;
      // "$0.12" in the summary and the Cost stat — both legitimately twice.
      expect(find.textContaining('6m12s'), findsWidgets);
      expect(find.textContaining(r'$0.12'), findsWidgets);
    });

    testWidgets('error pill appears when there are errors', (t) async {
      await _pump(t, RunReportCard(digest: _digest(errors: 3)));
      expect(find.textContaining('⚠ 3'), findsOneWidget);
    });

    testWidgets('no error pill when clean', (t) async {
      final d = _digest(errors: 0)..['errors'] = <String, dynamic>{};
      await _pump(t, RunReportCard(digest: d));
      expect(find.textContaining('⚠'), findsNothing);
    });

    testWidgets('expanded body shows the stat grid; collapses on tap',
        (t) async {
      await _pump(t, RunReportCard(digest: _digest()));
      // Expanded by default → stat labels visible.
      expect(find.text('Turns'), findsOneWidget);
      expect(find.text('Errors'), findsOneWidget);
      expect(find.text('Models'), findsOneWidget);

      // Tapping the header collapses the body.
      await t.tap(find.byIcon(Icons.expand_less));
      await t.pumpAndSettle();
      expect(find.text('Turns'), findsNothing);
    });

    testWidgets('empty digest reads "No activity yet"', (t) async {
      await _pump(t, const RunReportCard(digest: {'event_count': 0}));
      expect(find.text('No activity yet'), findsOneWidget);
    });

    testWidgets('tapping Errors stat seeks to the first error seq',
        (t) async {
      int? jumped;
      await _pump(
        t,
        RunReportCard(digest: _digest(), onJumpToSeq: (s) => jumped = s),
      );
      await t.tap(find.text('Errors'));
      await t.pumpAndSettle();
      // Lowest sample seq across error classes (9, 14, 11) is 9.
      expect(jumped, 9);
    });
  });
}
