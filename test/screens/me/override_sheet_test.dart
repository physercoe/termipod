import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/me/widgets/override_sheet.dart';

import '../../helpers/test_helpers.dart';

// ADR-030 W20 — modal bottom sheet for principal-override.
//
// These tests cover the sheet's UI invariants — what the principal
// sees, what's required, what cancels — without exercising the
// hub.decide round-trip (covered by the hub-side W9 override path
// tests). The decide call would fail in this test environment
// (no hub connection); we only verify the sheet's affordances.

Map<String, dynamic> _row() => {
      'id': 'att-w20-test',
      'kind': 'propose',
      'change_kind': 'deliverable.set_state',
      'assigned_tier': 'project-steward',
      'escalation_state': 'escalated_principal',
      'change_spec': {
        'from_state': 'draft',
        'to_state': 'ratified',
      },
      'target_ref': {'deliverable_id': 'del-w20'},
      'summary': 'Propose deliverable.set_state — review done',
    };

Widget _harness(Future<bool> Function() onTap) {
  return ProviderScope(
    child: MaterialApp(
      localizationsDelegates: testLocalizationsDelegates,
      supportedLocales: testSupportedLocales,
      home: Scaffold(
        body: Builder(
          builder: (context) => TextButton(
            onPressed: () async {
              await onTap();
            },
            child: const Text('Open'),
          ),
        ),
      ),
    ),
  );
}

void main() {
  group('showOverrideSheet — UI contract', () {
    testWidgets('shows change_kind, addressee, reason field, Override button',
        (tester) async {
      late BuildContext capturedContext;
      await tester.pumpWidget(_harness(() async {
        return showOverrideSheet(capturedContext, attention: _row());
      }));
      // Reach into the harness to grab a real context for the sheet.
      capturedContext = tester.element(find.text('Open'));
      await tester.tap(find.text('Open'));
      await tester.pumpAndSettle();

      expect(find.text('Override decision'), findsOneWidget);
      expect(find.textContaining('change_kind: deliverable.set_state'),
          findsOneWidget);
      expect(find.textContaining('project-steward'), findsOneWidget);
      // The required-reason field.
      expect(find.text('Reason (required)'), findsOneWidget);
      expect(find.widgetWithIcon(FilledButton, Icons.gavel), findsOneWidget);
      expect(find.text('Cancel'), findsOneWidget);
    });

    testWidgets('Cancel returns false and closes sheet', (tester) async {
      late BuildContext capturedContext;
      bool? lastResult;
      await tester.pumpWidget(_harness(() async {
        final r = await showOverrideSheet(capturedContext, attention: _row());
        lastResult = r;
        return r;
      }));
      capturedContext = tester.element(find.text('Open'));
      await tester.tap(find.text('Open'));
      await tester.pumpAndSettle();

      await tester.tap(find.text('Cancel'));
      await tester.pumpAndSettle();

      expect(lastResult, isFalse);
      expect(find.text('Override decision'), findsNothing);
    });

    testWidgets('empty reason on Override → inline error, stays open',
        (tester) async {
      late BuildContext capturedContext;
      await tester.pumpWidget(_harness(() async {
        return showOverrideSheet(capturedContext, attention: _row());
      }));
      capturedContext = tester.element(find.text('Open'));
      await tester.tap(find.text('Open'));
      await tester.pumpAndSettle();

      // Tap Override with no reason typed.
      await tester.tap(find.widgetWithIcon(FilledButton, Icons.gavel));
      await tester.pumpAndSettle();

      expect(find.textContaining('reason is required'), findsOneWidget);
      // Sheet still visible.
      expect(find.text('Override decision'), findsOneWidget);
    });

    testWidgets('change_spec preview includes from_state and to_state',
        (tester) async {
      late BuildContext capturedContext;
      await tester.pumpWidget(_harness(() async {
        return showOverrideSheet(capturedContext, attention: _row());
      }));
      capturedContext = tester.element(find.text('Open'));
      await tester.tap(find.text('Open'));
      await tester.pumpAndSettle();

      // Compact JSON renders the from/to in one line.
      expect(find.textContaining('from_state'), findsOneWidget);
      expect(find.textContaining('to_state'), findsOneWidget);
    });
  });
}
