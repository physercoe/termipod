import 'package:flutter/material.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_math_fork/flutter_math.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/transcript/event_card.dart';

import '../helpers/test_helpers.dart';

// Transcript P5 B1 — streaming markdown stops re-parsing the world.
//
// A partial carries the FULL accumulated text and replaces its chain entry in
// place (collapseStreamingPartials), so the heavy markdown treatment re-runs
// over the whole message on every chunk: highlight.js re-tokenizes every
// completed fenced block, flutter_math re-lays-out every formula, on the UI
// isolate. Over a long turn that is O(n²).
//
// These pin the gate in BOTH directions. Asserting only that a partial skips
// the work would pass just as well if the builders were removed outright, so
// each case has its completed-message twin proving the final render is
// unchanged — that is the whole contract: colour and formulas arrive when the
// message finishes, and a finished message looks exactly as it always did.

Map<String, dynamic> _textEvent(String text, {bool? partial}) => {
      'id': 'e1',
      'seq': 1,
      'kind': 'text',
      'producer': 'agent',
      'ts': '2026-07-29T10:00:00Z',
      'payload': {
        'text': text,
        if (partial != null) 'partial': partial,
      },
    };

const _fenced = 'Here is code:\n\n```dart\nvoid main() {}\n```\n';
const _math = r'Einstein said $E = mc^2$ once.';

/// Matches Text + SelectableText, plain or rich (the card body uses both).
Finder containingText(String s) => find.byWidgetPredicate((w) {
      if (w is Text) {
        return (w.data ?? w.textSpan?.toPlainText() ?? '').contains(s);
      }
      if (w is SelectableText) {
        return (w.data ?? w.textSpan?.toPlainText() ?? '').contains(s);
      }
      return false;
    });

Future<void> _pump(WidgetTester tester, Map<String, dynamic> event) async {
  await tester.pumpWidget(
    MaterialApp(
      localizationsDelegates: testLocalizationsDelegates,
      supportedLocales: testSupportedLocales,
      home: Scaffold(
        body: SizedBox(width: 420, child: AgentEventCard(event: event)),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('a streaming chunk does not run highlight.js over its code',
      (tester) async {
    await _pump(tester, _textEvent(_fenced, partial: true));
    expect(find.byType(HighlightView), findsNothing);
    // The block is still there and still readable — it just isn't tokenized.
    // `selectable: true` renders through SelectableText.rich, so the content is
    // in `textSpan`, not `data` (same predicate transcript_system_card_test
    // uses — reading only `data` finds nothing).
    expect(containingText('void main'), findsWidgets);
  });

  testWidgets('the completed message highlights exactly as before',
      (tester) async {
    await _pump(tester, _textEvent(_fenced));
    expect(find.byType(HighlightView), findsOneWidget);
  });

  testWidgets('an explicit partial:false is treated as complete',
      (tester) async {
    // The flag is only ever true on a chunk, but a mapper that stamps it false
    // on the final frame must not lose the highlighting.
    await _pump(tester, _textEvent(_fenced, partial: false));
    expect(find.byType(HighlightView), findsOneWidget);
  });

  testWidgets('a streaming chunk does not lay out math', (tester) async {
    await _pump(tester, _textEvent(_math, partial: true));
    expect(find.byType(Math), findsNothing);
  });

  testWidgets('the completed message lays out math as before', (tester) async {
    await _pump(tester, _textEvent(_math));
    expect(find.byType(Math), findsOneWidget);
  });

  testWidgets('a thought chunk is gated the same way as text', (tester) async {
    final ev = _textEvent(_fenced, partial: true)..['kind'] = 'thought';
    await _pump(tester, ev);
    expect(find.byType(HighlightView), findsNothing);
  });
}
