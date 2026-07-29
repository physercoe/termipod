import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/widgets/transcript/streaming_blocks.dart';

// Boundary rules for the streaming block splitter (transcript P5 B2).
//
// This is the file that decides where a still-streaming message may be cut
// into independently-parsed pieces, so a wrong rule here is a *rendering* bug —
// which is exactly why the rules live in pure Dart. The negative cases matter
// more than the positive ones: every one of them is a place where splitting
// would change what the markdown means.

/// Splitting is length-gated in production; tests drive the rules directly.
List<String> split(String s) => splitStreamingBlocks(s, minChars: 0);

/// The invariant that makes everything else safe to reason about.
void expectLossless(String src, List<String> blocks) {
  expect(blocks.join(), src, reason: 'blocks must reassemble to the input');
}

void main() {
  group('splits between top-level blocks', () {
    test('two paragraphs split into a settled block and a tail', () {
      const src = 'First paragraph.\n\nSecond paragraph still growing';
      final b = split(src);
      expect(b.length, 2);
      expect(b.first, 'First paragraph.\n\n');
      expect(b.last, 'Second paragraph still growing');
      expectLossless(src, b);
    });

    test('a closed fence is a settled block; the prose after it is the tail',
        () {
      const src = 'Intro line.\n\n```dart\nvoid main() {}\n```\n\nAnd then';
      final b = split(src);
      expect(b.length, 3);
      expect(b[1], contains('void main'));
      expect(b.last, 'And then');
      expectLossless(src, b);
    });

    test('a heading and its paragraph settle independently', () {
      const src = '# Title\n\nBody text.\n\nMore body arriving';
      final b = split(src);
      expect(b.length, 3);
      expect(b.first, '# Title\n\n');
      expectLossless(src, b);
    });

    test('a run of blank lines collapses to one cut', () {
      const src = 'One.\n\n\n\nTwo arriving';
      final b = split(src);
      expect(b.length, 2);
      expect(b.first, 'One.\n\n\n\n');
      expectLossless(src, b);
    });

    test('CRLF input reassembles byte for byte', () {
      const src = 'First.\r\n\r\nSecond arriving';
      final b = split(src);
      expect(b.length, 2);
      expectLossless(src, b);
    });
  });

  group('never splits where it would change the meaning', () {
    test('an unclosed fence pins the split before it', () {
      // The tail is still being written INSIDE the fence; cutting anywhere
      // after the opener would leave it unterminated on one side.
      const src = 'Prose.\n\n```dart\nvoid main() {\n\n  // still typing';
      final b = split(src);
      expect(b.length, 2);
      expect(b.first, 'Prose.\n\n');
      expect(b.last, startsWith('```dart'));
      expectLossless(src, b);
    });

    test('a blank line inside a fence is not a boundary', () {
      const src = 'A.\n\n```\ncode\n\nmore code\n```\n\nB arriving';
      final b = split(src);
      expect(b.length, 3);
      expect(b[1], contains('more code'));
      expectLossless(src, b);
    });

    test('a loose ordered list stays whole — a second <ol> would restart at 1',
        () {
      const src = '1. first\n\n2. second\n\n3. third arriving';
      expect(split(src), [src]);
    });

    test('a loose unordered list stays whole', () {
      const src = '- alpha\n\n- beta\n\n- gamma arriving';
      expect(split(src), [src]);
    });

    test('no split immediately after a list ends', () {
      const src = '- alpha\n- beta\n\nA following paragraph arriving';
      expect(split(src), [src]);
    });

    test('a block quote stays whole', () {
      const src = '> quoted line\n\n> still quoted arriving';
      expect(split(src), [src]);
    });

    test('an indented code block survives its blank lines', () {
      const src = '    code line 1\n\n    code line 2 arriving';
      expect(split(src), [src]);
    });

    test('a link reference definition disables splitting entirely', () {
      // The definition and its use can land in different blocks, and a block
      // parsed alone renders the reference as literal text.
      const src = 'See [docs].\n\n[docs]: https://example.com\n\nMore arriving';
      expect(split(src), [src]);
    });

    test('a tilde fence is tracked like a backtick fence', () {
      const src = 'A.\n\n~~~\ncode\n\nmore\n~~~\n\nB arriving';
      final b = split(src);
      expect(b.length, 3);
      expect(b[1], contains('more'));
      expectLossless(src, b);
    });

    test('a backtick fence is not closed by a shorter run', () {
      const src = 'A.\n\n````\ncode\n```\nstill inside\n\nmore arriving';
      final b = split(src);
      // Only the cut before the (still open) fence survives.
      expect(b.length, 2);
      expect(b.last, startsWith('````'));
      expectLossless(src, b);
    });
  });

  group('degrades to a single block rather than guessing', () {
    test('short text is not worth splitting', () {
      const src = 'One.\n\nTwo.';
      expect(splitStreamingBlocks(src), [src]);
      expect(src.length < kStreamingSplitMinChars, isTrue);
    });

    test('text with no blank line at all', () {
      expect(split('a single growing line'), ['a single growing line']);
    });

    test('leading and trailing blanks are not boundaries', () {
      const src = '\n\nonly one paragraph\n\n';
      expect(split(src), [src]);
    });

    test('empty input', () {
      expect(split(''), ['']);
    });
  });

  test('a long realistic message splits and stays lossless', () {
    final buf = StringBuffer();
    for (var i = 0; i < 40; i++) {
      buf.write('Paragraph number $i with some words in it.\n\n');
      if (i % 7 == 0) buf.write('```dart\nvar x$i = $i;\n```\n\n');
      if (i % 5 == 0) buf.write('- a list item\n- another\n\n');
    }
    buf.write('and the tail still being written');
    final src = buf.toString();
    final blocks = splitStreamingBlocks(src);
    expect(blocks.length, greaterThan(10));
    expectLossless(src, blocks);
    expect(blocks.last, 'and the tail still being written');
    // Every settled block ends at a blank line; only the tail may not.
    for (final b in blocks.take(blocks.length - 1)) {
      expect(b.endsWith('\n'), isTrue, reason: 'block should end at a line break: $b');
    }
  });

  test('growing text keeps its settled prefix byte-identical', () {
    // The cache is keyed on block content, so a settled block must be the SAME
    // string on the next chunk or every entry misses.
    const base = 'Alpha paragraph.\n\nBeta paragraph.\n\n';
    final first = splitStreamingBlocks('${base}gamma', minChars: 0);
    final later = splitStreamingBlocks('${base}gamma delta epsilon', minChars: 0);
    expect(first.length, 3);
    expect(later.length, 3);
    expect(later[0], first[0]);
    expect(later[1], first[1]);
    expect(later[2], isNot(first[2]));
  });
}
