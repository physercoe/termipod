// Markdown rendering helpers extracted from agent_feed.dart so the
// transcript file stops growing every time we tune syntax-highlight or
// LaTeX rules. All consumers live inside the agent feed today; this
// module is the single home for code-fence highlighting, LaTeX inline
// syntaxes, math element rendering, and the multiline-math
// preprocessor that flatten-fixes well-formed `$$...$$` and `\[...\]`
// regions before flutter_markdown sees them.
//
// Names dropped their underscore prefix on extraction (this codebase
// does not use Dart `part`-of files; symbols moving across files have
// to be public).
import 'package:flutter/material.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_math_fork/flutter_math.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:markdown/markdown.dart' as md;
import 'package:url_launcher/url_launcher.dart';

import '../theme/design_colors.dart';

/// MarkdownElementBuilder that swaps `<pre><code class="language-X">` blocks
/// out for a syntax-highlighted view. Inline `<code>` (no class attribute)
/// returns null so flutter_markdown falls back to its own monochrome
/// styleSheet rendering — we only want the heavy treatment on fenced
/// blocks where the language is declared. Fenced blocks without a
/// language (just ``` ```) get a plaintext highlight (no colors), which
/// still picks up the themed background + padding so the block visually
/// stands out from prose.
class HighlightedCodeBuilder extends MarkdownElementBuilder {
  final bool isDark;
  HighlightedCodeBuilder({required this.isDark});

  @override
  Widget? visitElementAfter(md.Element element, TextStyle? preferredStyle) {
    final classAttr = element.attributes['class'] ?? '';
    // Inline `<code>` has no class — let the base styleSheet handle it.
    // Fenced blocks always get a class even with no language ("language-").
    if (!classAttr.startsWith('language-')) return null;
    var language = classAttr.substring('language-'.length).trim();
    // flutter_highlight expects a known id or 'plaintext'; an unknown id
    // will raise. Map common aliases and fall back to plaintext for
    // anything we don't recognize.
    language = _normalizeLanguage(language);
    final theme = isDark ? atomOneDarkTheme : atomOneLightTheme;
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 4),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(4),
        border: Border.all(
          color: isDark
              ? DesignColors.borderDark
              : DesignColors.borderLight,
        ),
      ),
      clipBehavior: Clip.hardEdge,
      child: HighlightView(
        element.textContent,
        language: language,
        theme: theme,
        padding: const EdgeInsets.all(8),
        textStyle: GoogleFonts.jetBrainsMono(fontSize: 11, height: 1.35),
      ),
    );
  }

  // highlight.js language ids we keep as-is (the package ships these).
  // Aliases a user might type in a fence get rerouted here so we don't
  // throw "no language with that id" at runtime. Unknowns drop to
  // plaintext (still themed/padded, just not colored).
  static String _normalizeLanguage(String raw) {
    if (raw.isEmpty) return 'plaintext';
    final l = raw.toLowerCase();
    const aliases = {
      'sh': 'bash',
      'shell': 'bash',
      'zsh': 'bash',
      'console': 'bash',
      'js': 'javascript',
      'ts': 'typescript',
      'jsx': 'javascript',
      'tsx': 'typescript',
      'py': 'python',
      'rb': 'ruby',
      'rs': 'rust',
      'kt': 'kotlin',
      'cs': 'cs',
      'h': 'cpp',
      'hpp': 'cpp',
      'cc': 'cpp',
      'cxx': 'cpp',
      'c++': 'cpp',
      'objc': 'objectivec',
      'yml': 'yaml',
      'md': 'markdown',
      'tex': 'latex',
      'plain': 'plaintext',
      'text': 'plaintext',
    };
    return aliases[l] ?? l;
  }
}

// LaTeX math support. Two delimiter conventions, both common in LLM
// output:
//
//   1. arXiv/Pandoc dollar style:   $...$  (inline)   $$...$$ (display)
//   2. LaTeX bracket style:         \(...\) (inline)  \[...\] (display)
//
// All three single-line variants are inline syntaxes; only \[...\]
// also has a block flavor since LLMs frequently emit it as
//
//   \[
//   <expr possibly with \\ row breaks>
//   \]
//
// — and inline syntaxes can't span newlines.

/// Launch a markdown URL through the system browser. We don't try to
/// validate or whitelist schemes — that's the OS's job and operators
/// have legitimate uses for ssh:, mailto:, etc. A SnackBar surfaces any
/// launch failure so a broken href doesn't silently swallow the tap.
Future<void> openMarkdownLink(BuildContext ctx, String? href) async {
  if (href == null || href.isEmpty) return;
  final uri = Uri.tryParse(href);
  if (uri == null) return;
  try {
    final ok = await launchUrl(uri, mode: LaunchMode.externalApplication);
    if (!ok && ctx.mounted) {
      ScaffoldMessenger.of(ctx).showSnackBar(
        SnackBar(content: Text('Could not open $href')),
      );
    }
  } catch (e) {
    if (ctx.mounted) {
      ScaffoldMessenger.of(ctx).showSnackBar(
        SnackBar(content: Text('Open failed: $e')),
      );
    }
  }
}

/// MathBlockInlineSyntax matches `$$...$$` (single-line). Listed BEFORE
/// the inline `$...$` rule so the parser can claim both delimiters
/// before the $-rule eats the leading pair.
class MathBlockInlineSyntax extends md.InlineSyntax {
  MathBlockInlineSyntax() : super(r'\$\$([^\$\n]+?)\$\$');
  @override
  bool onMatch(md.InlineParser parser, Match match) {
    final tex = match[1] ?? '';
    parser.addNode(md.Element.text('mathblock', tex));
    return true;
  }
}

/// MathInlineSyntax matches `$...$` on a single line, requiring non-`$`
/// content. Avoids triggering on bare `$5` / `$20` currency
/// references (those would need a closing `$` to match).
class MathInlineSyntax extends md.InlineSyntax {
  MathInlineSyntax() : super(r'\$([^\$\n]+?)\$');
  @override
  bool onMatch(md.InlineParser parser, Match match) {
    final tex = match[1] ?? '';
    parser.addNode(md.Element.text('math', tex));
    return true;
  }
}

/// LatexBracketDisplayInlineSyntax matches single-line `\[ ... \]`.
/// Multi-line `\[...\]` is collapsed to `$$...$$` by
/// [normalizeMultilineMath] before this syntax sees the input.
class LatexBracketDisplayInlineSyntax extends md.InlineSyntax {
  LatexBracketDisplayInlineSyntax() : super(r'\\\[([^\n]+?)\\\]');
  @override
  bool onMatch(md.InlineParser parser, Match match) {
    final tex = match[1] ?? '';
    parser.addNode(md.Element.text('mathblock', tex));
    return true;
  }
}

/// LatexBracketInlineSyntax matches `\( ... \)` on a single line.
class LatexBracketInlineSyntax extends md.InlineSyntax {
  LatexBracketInlineSyntax() : super(r'\\\(([^\n]+?)\\\)');
  @override
  bool onMatch(md.InlineParser parser, Match match) {
    final tex = match[1] ?? '';
    parser.addNode(md.Element.text('math', tex));
    return true;
  }
}

/// Collapse well-formed multi-line math regions into single-line forms
/// the inline syntaxes already handle:
///
///     $$\n<expr>\n$$           →  $$<expr-flattened>$$
///     \[\n<expr>\n\]           →  $$<expr-flattened>$$
///
/// Internal newlines become spaces — TeX treats both identically, so
/// this preserves matrix `\\` row breaks and other structural commands.
///
/// Why preprocess instead of registering a BlockSyntax: a greedy block
/// parser silently swallows everything after a stray `$$` when no close
/// follows (codex transcripts contain shell `$$`, prompt strings, etc.)
/// — the visible symptom is "transcript renders blank." This function
/// only rewrites a region when it finds a matching close; unbalanced
/// delimiters fall through unchanged and render as plain text.
///
/// Fenced code blocks (``` … ```) are skipped so command examples stay
/// untouched; non-greedy matching keeps two adjacent regions distinct.
String normalizeMultilineMath(String input) {
  if (input.isEmpty) return input;
  // Split on fenced code blocks; even-indexed slices are body text we
  // rewrite, odd-indexed slices are code we leave verbatim. The
  // fence itself goes back into the odd slice.
  final fence = RegExp(r'(```[\s\S]*?```|~~~[\s\S]*?~~~)', multiLine: true);
  final parts = <String>[];
  int cursor = 0;
  for (final m in fence.allMatches(input)) {
    parts.add(input.substring(cursor, m.start));
    parts.add(m.group(0)!);
    cursor = m.end;
  }
  parts.add(input.substring(cursor));

  // Patterns are non-greedy; the (?=\s*\n|$) anchors require the
  // delimiters to sit on their own line so we don't claim inline
  // sequences the inline syntaxes already handle.
  final dollarBlock = RegExp(
    r'(^|\n)\$\$\s*\n([\s\S]+?)\n\s*\$\$(?=\s*\n|\s*$)',
    multiLine: true,
  );
  final bracketBlock = RegExp(
    r'(^|\n)\\\[\s*\n([\s\S]+?)\n\s*\\\](?=\s*\n|\s*$)',
    multiLine: true,
  );

  String flatten(String body) =>
      body.replaceAll(RegExp(r'\s*\n\s*'), ' ').trim();

  for (var i = 0; i < parts.length; i += 2) {
    var s = parts[i];
    s = s.replaceAllMapped(dollarBlock, (m) {
      return '${m.group(1)}\$\$${flatten(m.group(2)!)}\$\$';
    });
    s = s.replaceAllMapped(bracketBlock, (m) {
      return '${m.group(1)}\$\$${flatten(m.group(2)!)}\$\$';
    });
    parts[i] = s;
  }
  return parts.join();
}

/// MathBuilder renders a flutter_math_fork `Math.tex` widget for the
/// element's text. `display` toggles inline (uses `MathStyle.text`) vs
/// block (uses `MathStyle.display`, larger and centered).
///
/// Errors fall back to the raw TeX wrapped in `$...$` as inline mono —
/// keeps malformed math visible rather than silently dropped, so the
/// principal can spot LLM-generated typos.
class MathBuilder extends MarkdownElementBuilder {
  final bool isDark;
  final bool display;
  MathBuilder({required this.isDark, required this.display});

  @override
  Widget? visitElementAfter(md.Element element, TextStyle? preferredStyle) {
    final tex = element.textContent;
    final color = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    final base = GoogleFonts.spaceGrotesk(
      fontSize: display ? 15 : 13,
      color: color,
    );
    final widget = Math.tex(
      tex,
      textStyle: base,
      mathStyle: display ? MathStyle.display : MathStyle.text,
      onErrorFallback: (e) => Text(
        '\$$tex\$',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color: DesignColors.error,
        ),
      ),
    );
    if (!display) return widget;
    // Display-math: center on its own line with a touch of vertical
    // breathing room so the bigger glyphs don't run into surrounding
    // paragraphs.
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Center(child: widget),
    );
  }
}
