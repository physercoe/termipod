import 'package:flutter/material.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/transcript/event_card.dart';

import '../helpers/test_helpers.dart';

// Transcript P5 B3 — the two behaviours the migration to
// flutter_markdown_plus had to preserve (plan §4).
//
// The plan listed both as "verify on device". They are cheaper and more durable
// as assertions: a renderer swap that quietly changed either would otherwise be
// caught only by someone noticing a duplicated link label months later. Both
// are properties of the RENDERER, not of our code, so they are equally a
// regression net for the next version bump.

/// Every rendered character on screen, in one string.
String renderedText(WidgetTester tester) {
  final buf = StringBuffer();
  for (final w in tester.allWidgets) {
    if (w is SelectableText) {
      buf.write(w.data ?? w.textSpan?.toPlainText() ?? '');
    } else if (w is Text) {
      buf.write(w.data ?? w.textSpan?.toPlainText() ?? '');
    }
  }
  return buf.toString();
}

int occurrences(String haystack, String needle) {
  var n = 0;
  var i = haystack.indexOf(needle);
  while (i >= 0) {
    n++;
    i = haystack.indexOf(needle, i + needle.length);
  }
  return n;
}

Map<String, dynamic> textEvent(String text) => {
      'id': 'e1',
      'seq': 1,
      'kind': 'text',
      'producer': 'agent',
      'ts': '2026-07-29T10:00:00Z',
      'payload': {'text': text},
    };

Future<void> pumpCard(WidgetTester tester, String markdown) async {
  await tester.pumpWidget(
    MaterialApp(
      localizationsDelegates: testLocalizationsDelegates,
      supportedLocales: testSupportedLocales,
      home: Scaffold(
        body: SizedBox(
          width: 420,
          child: SingleChildScrollView(child: AgentEventCard(event: textEvent(markdown))),
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('a link label renders once, not twice', (tester) async {
    // We deliberately do NOT register an `a` element builder: the renderer
    // appends a builder's widget AFTER its own styled inline span, so a custom
    // one draws the label twice (once coloured+underlined, once tappable).
    // That workaround is only correct while the append semantics hold — if a
    // future version stops appending, the link would silently lose its style
    // instead of doubling, and this test is what notices.
    await pumpCard(tester, 'See [the docs](https://example.com) for more.');
    final text = renderedText(tester);
    expect(occurrences(text, 'the docs'), 1,
        reason: 'link label duplicated — the `a` builder-append assumption changed');
    expect(text, contains('See '));
    expect(text, contains(' for more.'));
  });

  testWidgets('inline code falls through to the styleSheet, not the highlighter',
      (tester) async {
    // HighlightedCodeBuilder returns null unless the element carries a
    // `language-` class, which only fenced blocks get. Inline code must reach
    // the styleSheet's mono `code` style instead — if the renderer started
    // tagging inline code with a class, every `foo` in prose would become a
    // full highlight.js block.
    await pumpCard(tester, 'Call `resolveToolAnchor` before folding.');
    expect(find.byType(HighlightView), findsNothing);
    expect(renderedText(tester), contains('resolveToolAnchor'));
  });

  testWidgets('a fenced block with a language still reaches the highlighter',
      (tester) async {
    // The positive control for the test above: without it, "no HighlightView"
    // would pass just as well if the builder had stopped being wired at all.
    await pumpCard(tester, 'Code:\n\n```dart\nvoid main() {}\n```\n');
    expect(find.byType(HighlightView), findsOneWidget);
  });

  testWidgets('a fenced block with no language is still themed', (tester) async {
    // "language-" with an empty id maps to plaintext — the block keeps its
    // background and padding rather than falling back to bare prose.
    await pumpCard(tester, 'Code:\n\n```\nplain text block\n```\n');
    expect(find.byType(HighlightView), findsOneWidget);
  });
}
