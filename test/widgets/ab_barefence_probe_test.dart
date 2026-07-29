import 'package:flutter/material.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/transcript/event_card.dart';

import '../helpers/test_helpers.dart';

// TEMPORARY A/B probe (not for merge): does a fenced block with NO language
// reach HighlightedCodeBuilder under the CURRENT (flutter_markdown) renderer?
// markdown_builders.dart claims "Fenced blocks always get a class even with no
// language" — this establishes whether that is true today, so the B3 migration
// result can be read as a regression or as a stale comment.
void main() {
  testWidgets('bare fence under the current renderer', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        localizationsDelegates: testLocalizationsDelegates,
        supportedLocales: testSupportedLocales,
        home: Scaffold(
          body: SizedBox(
            width: 420,
            child: SingleChildScrollView(
              child: AgentEventCard(event: const {
                'id': 'e1',
                'seq': 1,
                'kind': 'text',
                'producer': 'agent',
                'ts': '2026-07-29T10:00:00Z',
                'payload': {'text': 'Code:\n\n```\nplain text block\n```\n'},
              }),
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.byType(HighlightView), findsOneWidget);
  });
}
