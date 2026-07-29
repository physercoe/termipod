import 'package:flutter/material.dart';
import 'package:flutter_markdown_plus/flutter_markdown_plus.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/transcript/event_card.dart';
import 'package:termipod/widgets/transcript/streaming_markdown.dart';

import '../helpers/test_helpers.dart';

// Transcript P5 B2 — the streaming block cache.
//
// The whole claim of B2 is "a chunk costs one tail block instead of the whole
// message". That is only true if settled blocks are handed back as the SAME
// widget instance, because that is the one thing Flutter skips
// (`Element.updateChild` returns early on `child.widget == newWidget`). So the
// tests below count builds — asserting the split happened would prove nothing
// about whether any work was actually saved.

/// Drives a StreamingMarkdownBody whose text can grow, keeping one State alive
/// across chunks exactly as the feed does.
class _Harness extends StatefulWidget {
  final List<String> built;
  const _Harness({required this.built});

  @override
  State<_Harness> createState() => _HarnessState();
}

class _HarnessState extends State<_Harness> {
  String _text = '';
  Object _styleKey = 'dark|false';

  void grow(String text) => setState(() => _text = text);
  void restyle(Object key) => setState(() => _styleKey = key);

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      home: Scaffold(
        body: StreamingMarkdownBody(
          text: _text,
          styleKey: _styleKey,
          minChars: 0,
          buildBlock: (block) {
            widget.built.add(block);
            return Text(block);
          },
        ),
      ),
    );
  }
}

/// Pumps the harness and discards the build from its initial empty text, so a
/// test asserts only on the chunks it drives.
Future<_HarnessState> _boot(WidgetTester tester, List<String> built) async {
  await tester.pumpWidget(_Harness(built: built));
  built.clear();
  return tester.state<_HarnessState>(find.byType(_Harness));
}

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

/// A message long enough to clear the production split threshold.
String _longMessage(String tail) {
  final buf = StringBuffer();
  for (var i = 0; i < 30; i++) {
    buf.write('Paragraph number $i, with enough words to matter.\n\n');
  }
  buf.write(tail);
  return buf.toString();
}

void main() {
  testWidgets('a settled block is built once, however many chunks follow',
      (tester) async {
    final built = <String>[];
    final state = await _boot(tester, built);

    state.grow('Alpha.\n\nBeta.\n\ntail one');
    await tester.pump();
    expect(built, ['Alpha.\n\n', 'Beta.\n\n', 'tail one']);

    built.clear();
    state.grow('Alpha.\n\nBeta.\n\ntail one two');
    await tester.pump();
    // Only the tail — the two settled blocks came back from the cache.
    expect(built, ['tail one two']);

    built.clear();
    state.grow('Alpha.\n\nBeta.\n\ntail one two three');
    await tester.pump();
    expect(built, ['tail one two three']);
  });

  testWidgets('a completed tail is parsed once more, then never again',
      (tester) async {
    final built = <String>[];
    final state = await _boot(tester, built);

    state.grow('Alpha.\n\ntail');
    await tester.pump();
    built.clear();

    // The tail closes and a new one opens.
    state.grow('Alpha.\n\ntail.\n\nnext');
    await tester.pump();
    expect(built, ['tail.\n\n', 'next']);

    built.clear();
    state.grow('Alpha.\n\ntail.\n\nnext growing');
    await tester.pump();
    expect(built, ['next growing']);
  });

  testWidgets('the cache is dropped when the render style changes',
      (tester) async {
    // Otherwise a theme flip mid-turn would leave settled blocks in the old
    // colours — cached widgets carry their styling with them.
    final built = <String>[];
    final state = await _boot(tester, built);

    state.grow('Alpha.\n\nBeta.\n\ntail');
    await tester.pump();
    built.clear();

    state.restyle('light|false');
    await tester.pump();
    expect(built, ['Alpha.\n\n', 'Beta.\n\n', 'tail']);
  });

  testWidgets('text with no safe boundary renders as one body', (tester) async {
    final built = <String>[];
    final state = await _boot(tester, built);

    // A list is atomic — no cut is safe anywhere in it.
    const src = '- alpha\n\n- beta\n\n- gamma';
    state.grow(src);
    await tester.pump();
    expect(built, [src]);
  });

  group('in the real card', () {
    Future<void> pump(WidgetTester tester, Map<String, dynamic> event) async {
      await tester.pumpWidget(
        MaterialApp(
          localizationsDelegates: testLocalizationsDelegates,
          supportedLocales: testSupportedLocales,
          home: Scaffold(
            body: SizedBox(
              width: 420,
              child: SingleChildScrollView(child: AgentEventCard(event: event)),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
    }

    testWidgets('a long partial renders as several bodies', (tester) async {
      await pump(tester, _textEvent(_longMessage('still writing'), partial: true));
      expect(find.byType(MarkdownBody), findsWidgets);
      expect(tester.widgetList(find.byType(MarkdownBody)).length, greaterThan(1));
    });

    testWidgets('the completed message renders as exactly one body',
        (tester) async {
      // The identity guarantee: a finished message is laid out by a single
      // MarkdownBody, exactly as it was before B2 — there is no seam to verify.
      await pump(tester, _textEvent(_longMessage('done')));
      expect(tester.widgetList(find.byType(MarkdownBody)).length, 1);
    });

    testWidgets('a short partial is not split', (tester) async {
      await pump(tester, _textEvent('Hi.\n\nThere.', partial: true));
      expect(tester.widgetList(find.byType(MarkdownBody)).length, 1);
    });
  });
}
