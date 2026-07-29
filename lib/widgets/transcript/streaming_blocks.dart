/// Splits accumulated streaming markdown into cacheable blocks
/// (transcript P5 B2 — the markstream stable-prefix idea, in Dart).
///
/// A partial carries the FULL accumulated text, so every chunk re-parses and
/// re-lays-out the whole message. B1 removed the highlight/math term; what is
/// left is still proportional to total length, on the UI isolate, per chunk.
/// Splitting the text at **completed block boundaries** lets the renderer cache
/// the blocks that can no longer change and re-parse only the open tail.
///
/// Pure Dart on purpose — no Flutter import — so the boundary rules are
/// directly unit-testable. Getting them wrong is a *rendering* bug, and this
/// file is where that risk is concentrated.
///
/// ## What counts as a safe boundary
///
/// Only a blank line at the top level, and only where independently parsing the
/// two sides cannot change what either one means. That rules out a lot:
///
///  * **inside a fenced block** — the fence would be unterminated on one side;
///  * **inside a list** (either neighbour is a list item) — two `<ol>`s restart
///    the numbering, which is visibly wrong, and a loose list legitimately
///    contains blank lines;
///  * **inside a block quote** — same reasoning;
///  * **inside an indented code block** — it survives blank lines, so a split
///    would cut one code block into two;
///  * **anywhere in a document carrying a link reference definition** — the
///    definition and its use can land in different blocks, and a block parsed
///    on its own would render the reference as literal text. Rare in agent
///    output, cheap to rule out wholesale.
///
/// The result is that lists, quotes and code stay atomic and splits land
/// between top-level paragraphs, headings, tables and completed fences — which
/// is where the long messages actually are.
library;

/// Below this length a message is not worth splitting: several `MarkdownBody`
/// widgets in a `Column` cost more than one parse of a short string, and the
/// block seams are a (small) rendering risk for no gain. Long assistant turns —
/// the ones that hurt — are far above it.
const int kStreamingSplitMinChars = 1200;

/// A link reference definition (`[label]: /url`) at the start of a line.
final RegExp _linkRefDef = RegExp(r'^ {0,3}\[[^\]]+\]:\s', multiLine: true);

/// `- item`, `* item`, `+ item`, `1. item`, `1) item`.
final RegExp _listItem = RegExp(r'^([-*+][ \t])|^(\d{1,9}[.)][ \t])');

/// Splits [text] into blocks: every element but the last is a **completed**
/// block whose rendering can no longer change as more text arrives, and the
/// last is the open tail.
///
/// Guarantees, both pinned by tests:
///  * the concatenation of the result is exactly [text] — nothing is dropped
///    or reordered;
///  * a text with no safe boundary comes back as a single element, so the
///    caller falls back to rendering it whole.
///
/// [minChars] exists for tests; production callers use the default.
List<String> splitStreamingBlocks(String text, {int minChars = kStreamingSplitMinChars}) {
  if (text.length < minChars) return [text];
  // See the class doc: a reference definition anywhere disables splitting.
  if (_linkRefDef.hasMatch(text)) return [text];

  final lines = _splitLinesKeepingTerminators(text);
  if (lines.length < 3) return [text];

  // Pass 1 — classify every line, tracking fenced-code state. A fence swallows
  // everything until its matching close, so blank lines inside it are not
  // boundaries and its content is never mistaken for a list or a quote.
  final blank = List<bool>.filled(lines.length, false);
  final fenced = List<bool>.filled(lines.length, false);
  final indent = List<int>.filled(lines.length, 0);
  final body = List<String>.filled(lines.length, '');
  var inFence = false;
  var fenceChar = '';
  var fenceLen = 0;
  for (var i = 0; i < lines.length; i++) {
    final line = _stripEol(lines[i]);
    final trimmed = line.trimLeft();
    indent[i] = line.length - trimmed.length;
    body[i] = trimmed;
    blank[i] = trimmed.isEmpty;
    fenced[i] = inFence;
    if (inFence) {
      if (_closesFence(trimmed, fenceChar, fenceLen)) inFence = false;
      continue;
    }
    // An opening fence must be at most 3 spaces in, else it is indented code.
    if (indent[i] < 4) {
      final open = _opensFence(trimmed);
      if (open != null) {
        inFence = true;
        fenceChar = open.$1;
        fenceLen = open.$2;
        fenced[i] = true; // the opening line belongs to the fence
      }
    }
  }
  // An unterminated fence pins the split before its opening line: everything
  // from there on is still growing.
  final tailFenceStart = inFence ? _lastFenceOpen(fenced, blank) : -1;

  // Pass 2 — collect the blank lines that are safe to break after.
  final cuts = <int>[];
  for (var i = 0; i < lines.length; i++) {
    if (!blank[i] || fenced[i]) continue;
    if (tailFenceStart >= 0 && i > tailFenceStart) break;
    final prev = _prevContent(i, blank, fenced);
    final next = _nextContent(i, blank, fenced);
    if (prev < 0 || next < 0) continue; // leading/trailing blanks
    if (_isAtomic(body[prev], indent[prev]) || _isAtomic(body[next], indent[next])) continue;
    // Collapse a run of blank lines to one cut, after the last of them.
    if (cuts.isNotEmpty && cuts.last == i - 1 && blank[i - 1]) {
      cuts[cuts.length - 1] = i;
    } else {
      cuts.add(i);
    }
  }
  if (cuts.isEmpty) return [text];

  final blocks = <String>[];
  var start = 0;
  for (final cut in cuts) {
    blocks.add(lines.sublist(start, cut + 1).join());
    start = cut + 1;
  }
  if (start < lines.length) {
    blocks.add(lines.sublist(start).join());
  }
  // Every cut had content after it, so a tail always exists; keep the
  // invariant explicit rather than assumed.
  return blocks.length < 2 ? [text] : blocks;
}

/// A line that makes its neighbourhood unsplittable: a list item, a block
/// quote, or an indented-code line (which survives blank lines, so a split
/// would cut one code block in half).
bool _isAtomic(String trimmed, int indent) =>
    indent >= 4 || trimmed.startsWith('>') || _listItem.hasMatch(trimmed);

/// Index of the last non-blank, non-fenced line before [i]; -1 if none.
int _prevContent(int i, List<bool> blank, List<bool> fenced) {
  for (var j = i - 1; j >= 0; j--) {
    if (!blank[j]) return j;
  }
  return -1;
}

/// Index of the next non-blank line after [i]; -1 if none.
int _nextContent(int i, List<bool> blank, List<bool> fenced) {
  for (var j = i + 1; j < blank.length; j++) {
    if (!blank[j]) return j;
  }
  return -1;
}

/// The line index where the still-open fence began, so nothing after it is
/// treated as settled.
int _lastFenceOpen(List<bool> fenced, List<bool> blank) {
  var i = fenced.length - 1;
  while (i >= 0 && fenced[i]) {
    i--;
  }
  return i + 1;
}

/// Returns (marker, length) when [trimmed] opens a fence, else null. A fence is
/// 3+ backticks or tildes; a backtick fence's info string may not contain a
/// backtick (CommonMark).
(String, int)? _opensFence(String trimmed) {
  for (final ch in const ['`', '~']) {
    if (!trimmed.startsWith(ch * 3)) continue;
    var n = 0;
    while (n < trimmed.length && trimmed[n] == ch) {
      n++;
    }
    final info = trimmed.substring(n);
    if (ch == '`' && info.contains('`')) continue;
    return (ch, n);
  }
  return null;
}

/// A closing fence is the same marker, at least as long, and carries no info
/// string.
bool _closesFence(String trimmed, String ch, int len) {
  if (!trimmed.startsWith(ch * len)) return false;
  var n = 0;
  while (n < trimmed.length && trimmed[n] == ch) {
    n++;
  }
  return n >= len && trimmed.substring(n).trim().isEmpty;
}

/// Splits into lines that still carry their `\n` / `\r\n`, so joining the
/// result reproduces the input byte for byte.
List<String> _splitLinesKeepingTerminators(String text) {
  final out = <String>[];
  var start = 0;
  for (var i = 0; i < text.length; i++) {
    if (text.codeUnitAt(i) == 0x0A) {
      out.add(text.substring(start, i + 1));
      start = i + 1;
    }
  }
  if (start < text.length) out.add(text.substring(start));
  return out;
}

String _stripEol(String line) {
  var end = line.length;
  if (end > 0 && line.codeUnitAt(end - 1) == 0x0A) end--;
  if (end > 0 && line.codeUnitAt(end - 1) == 0x0D) end--;
  return line.substring(0, end);
}
