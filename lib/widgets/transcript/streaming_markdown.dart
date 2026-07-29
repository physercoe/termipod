import 'package:flutter/material.dart';

import 'streaming_blocks.dart';

/// Renders a still-streaming markdown message as cached blocks
/// (transcript P5 B2).
///
/// ## Why this is a widget and not a helper
///
/// Memoizing "blocks" only pays if Flutter can skip their subtrees, and it
/// skips exactly one thing: a child whose widget instance is **identical** to
/// the one it already has (`Element.updateChild` returns early on
/// `child.widget == newWidget`, and `Widget` does not override `==`). So the
/// cache has to hold **built widget instances**, and it has to survive across
/// chunks — which means it lives in a `State`.
///
/// It survives because the streaming card keeps its position in the feed list:
/// `collapseStreamingPartials` replaces the chain entry in place, so the card
/// is reconciled by position and its `Element` — and this one, nested inside it
/// — are reused chunk to chunk. Prepending older history would shift it, but a
/// message is only ever streaming at the tail.
///
/// ## What is and isn't cached
///
/// Every block but the last is complete and can no longer change, so it is
/// built once and thereafter returned as the same instance — Flutter walks
/// straight past it. The **tail** is rebuilt every chunk, which is the point:
/// the per-chunk cost stops being "the whole message" and becomes "the block
/// currently being written".
///
/// When the tail completes it becomes the second-to-last block, so it is parsed
/// once more (as a stable block) and then never again.
///
/// ## Known, accepted
///
/// Text selection does not span block boundaries while a message is streaming.
/// The completed message renders as a single body (see `_markdownBody`), so
/// this affects only the in-flight frame.
class StreamingMarkdownBody extends StatefulWidget {
  /// The full accumulated text of the partial.
  final String text;

  /// Anything that changes how a block renders (theme brightness, thought vs
  /// text). A change invalidates the cache — otherwise a theme switch mid-turn
  /// would leave already-built blocks in the old colours.
  final Object styleKey;

  /// Builds one block. Called on a cache miss and for the tail.
  final Widget Function(String block) buildBlock;

  /// Test seam: the minimum length at which splitting is worth it.
  final int minChars;

  const StreamingMarkdownBody({
    super.key,
    required this.text,
    required this.styleKey,
    required this.buildBlock,
    this.minChars = kStreamingSplitMinChars,
  });

  @override
  State<StreamingMarkdownBody> createState() => _StreamingMarkdownBodyState();
}

/// Bounds the cache so a pathological message can't grow it without limit. A
/// message with this many completed blocks has long since stopped being the
/// thing we are optimizing for; dropping the cache costs one re-parse.
const int _maxCachedBlocks = 256;

class _StreamingMarkdownBodyState extends State<StreamingMarkdownBody> {
  final Map<String, Widget> _cache = {};

  @override
  void didUpdateWidget(covariant StreamingMarkdownBody old) {
    super.didUpdateWidget(old);
    if (old.styleKey != widget.styleKey) _cache.clear();
  }

  @override
  Widget build(BuildContext context) {
    final blocks = splitStreamingBlocks(widget.text, minChars: widget.minChars);
    // No safe boundary (short message, an open fence, a list all the way down):
    // render whole, exactly as before B2.
    if (blocks.length < 2) return widget.buildBlock(widget.text);

    if (_cache.length > _maxCachedBlocks) _cache.clear();
    final children = <Widget>[];
    for (var i = 0; i < blocks.length; i++) {
      final block = blocks[i];
      if (i == blocks.length - 1) {
        // The tail is still growing — never cache it, or the next chunk would
        // render a stale prefix of itself.
        children.add(widget.buildBlock(block));
      } else {
        children.add(_cache.putIfAbsent(block, () => widget.buildBlock(block)));
      }
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: children,
    );
  }
}
