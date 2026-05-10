import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../theme/design_colors.dart';
import 'steward_overlay_controller.dart';

/// Compact chat surface that lives inside the expanded
/// [StewardOverlay] panel. Connects to the team's general steward
/// (via [stewardOverlayControllerProvider]) and renders a minimal
/// transcript + a single-line input.
///
/// Intentionally **not** a clone of `agent_feed.dart` — that widget
/// is 4500+ lines of full-fidelity transcript rendering. The overlay
/// chat is for short directives ("show me X", "open Y"); when the
/// user wants a deep transcript they can open the steward's session
/// chat from the Sessions tab.
class StewardOverlayChat extends ConsumerStatefulWidget {
  /// Called when the user explicitly closes the chat (e.g. the
  /// header X). Lets the parent collapse the overlay panel.
  final VoidCallback onCloseRequested;

  const StewardOverlayChat({super.key, required this.onCloseRequested});

  @override
  ConsumerState<StewardOverlayChat> createState() => _StewardOverlayChatState();
}

class _StewardOverlayChatState extends ConsumerState<StewardOverlayChat> {
  /// Sends user input via the steward controller. Hoisted out of the
  /// build tree as a stable function reference so `_ChatInput`'s
  /// State doesn't think the callback identity changed across builds.
  Future<void> _sendText(String text) async {
    final controller = ref.read(stewardOverlayControllerProvider.notifier);
    await controller.sendUserText(text);
  }

  @override
  Widget build(BuildContext context) {
    // **Crucial scoping decision** — there is no `ref.watch` here.
    // SSE events flow into `stewardOverlayControllerProvider` at high
    // frequency (every text chunk, every tool_call, every system
    // event); if this State watched the provider directly the whole
    // subtree (including `_ChatInput`'s TextField) would be rebuilt
    // on every event. Even with a stable controller, that triggers
    // Flutter's `_updateRemoteEditingValueIfNeeded` IME poke, which
    // GBoard interprets as a composition reset and rebounds with its
    // cached predictive word — visible bug: deleted text returning.
    //
    // Instead, the rebuild scope is narrowed to a single Consumer
    // around the messages region. The input below sits OUTSIDE that
    // Consumer's invalidation set, so SSE events never traverse the
    // TextField subtree at all. (Belt-and-suspenders: the input also
    // disables predictive composition + autocorrect; see
    // `_ChatInputState.build`.)
    return const Column(
      children: [
        Expanded(child: _MessagesRegion()),
        Divider(height: 1),
        _ChatInputSlot(),
      ],
    );
  }
}

/// Sibling-isolated slot for the chat input. Lives outside the
/// messages-region Consumer so SSE-driven rebuilds don't reach the
/// TextField subtree. Reads `_StewardOverlayChatState._sendText` from
/// the nearest ancestor of that type via the `context`.
class _ChatInputSlot extends StatelessWidget {
  const _ChatInputSlot();

  @override
  Widget build(BuildContext context) {
    // Walk up to find the parent State that owns the send callback.
    // We don't use a Provider/InheritedWidget for this because
    // `_sendText` is a method tearoff on the parent State — stable
    // across builds for the same instance — and a const-stable
    // ValueKey on `_ChatInput` guarantees its own State survives.
    final parent = context.findAncestorStateOfType<_StewardOverlayChatState>();
    if (parent == null) {
      return const SizedBox.shrink();
    }
    return _ChatInput(
      key: const ValueKey('steward-overlay-chat-input'),
      onSend: parent._sendText,
    );
  }
}

/// Messages region — the only widget in the chat panel that watches
/// the high-frequency steward provider. Loading / error / empty / list
/// branches all live here; the input below is unaffected by any of
/// these transitions.
class _MessagesRegion extends ConsumerStatefulWidget {
  const _MessagesRegion();

  @override
  ConsumerState<_MessagesRegion> createState() => _MessagesRegionState();
}

class _MessagesRegionState extends ConsumerState<_MessagesRegion> {
  final _scrollCtrl = ScrollController();

  @override
  void dispose() {
    _scrollCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(stewardOverlayControllerProvider);
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    if (state.error != null && state.agentId == null) {
      return _ErrorView(error: state.error!);
    }
    if (state.agentId == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (state.messages.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'Tell the steward what you want to see.\n'
            'Examples:\n'
            '  • "Show me the steward insights view"\n'
            '  • "Open project X"\n'
            '  • "Take me to activity"\n',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color: muted,
            ),
          ),
        ),
      );
    }
    return ListView.separated(
      controller: _scrollCtrl,
      padding: const EdgeInsets.fromLTRB(12, 12, 12, 12),
      itemCount: state.messages.length,
      separatorBuilder: (_, __) => const SizedBox(height: 8),
      itemBuilder: (_, i) => _MessageBubble(
        msg: state.messages[i],
        muted: muted,
      ),
    );
  }
}

/// Isolated input box for the overlay chat. Owns its own
/// [TextEditingController] + sending flag so that frequent parent
/// rebuilds (driven by SSE events into `stewardOverlayControllerProvider`)
/// don't reach the TextField subtree.
///
/// Two QA bugs from v1.0.466 motivated the extraction:
///   (a) deleting some text and retyping caused the deleted text
///       to come back — symptomatic of the controller being
///       reset/repainted with stale value across rapid rebuilds.
///   (b) tapping a new cursor position then typing made the cursor
///       jump to the end — same root cause: external value
///       reapplication resets selection.
/// Hoisting the controller into a non-Consumer State (no
/// `ref.watch`) breaks the rebuild path; the input only rebuilds
/// when its OWN setState fires.
class _ChatInput extends StatefulWidget {
  /// Called with the trimmed text when the user submits. Should
  /// throw on failure so the input can restore the text for retry.
  final Future<void> Function(String text) onSend;

  const _ChatInput({
    super.key,
    required this.onSend,
  });

  @override
  State<_ChatInput> createState() => _ChatInputState();
}

class _ChatInputState extends State<_ChatInput> {
  final _ctrl = TextEditingController();
  // Owning the focus node here (rather than letting the framework mint
  // a transient one per build) keeps the IME connection stable across
  // parent rebuilds — same reasoning as the dedicated controller. A
  // freshly-minted FocusNode every build can race the IME attach/detach
  // cycle and exacerbate the predictive-restore bug below.
  final _focus = FocusNode();
  bool _sending = false;

  @override
  void dispose() {
    _focus.dispose();
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final text = _ctrl.text.trim();
    if (text.isEmpty) return;
    // Clear BEFORE the await so anything the user types during the
    // network round-trip isn't wiped when the future resolves. On
    // failure we restore the original text so the user can retry.
    _ctrl.clear();
    setState(() => _sending = true);
    try {
      await widget.onSend(text);
    } catch (e) {
      if (mounted) {
        // Only restore if the user hasn't started typing something
        // new — preserving their fresh input is the higher priority.
        if (_ctrl.text.isEmpty) {
          _ctrl.text = text;
        }
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Send failed: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(8, 8, 8, 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Expanded(
            // Predictive-typing flags match the rest of the codebase's
            // deterministic-input pattern (compose_bar direct mode,
            // hub_bootstrap, templates). They're kept as belt-and-
            // suspenders alongside the v1.0.472 rebuild-scope fix.
            //
            // **Do not add `autofillHints: const []`.** An empty
            // autofillHints list is poisoned: on some Android+Gboard
            // combinations it signals AutofillManager that the field
            // is managed by autofill but has no hints, and the IME
            // fails to attach — visible bug in v1.0.472 was "no system
            // keyboard pops up when tapping the input." `null`
            // (the default, achieved by omitting the line entirely) is
            // the correct shape. None of the other inputs in this
            // codebase set autofillHints; we shouldn't either.
            child: TextField(
              controller: _ctrl,
              focusNode: _focus,
              minLines: 1,
              maxLines: 4,
              keyboardType: TextInputType.multiline,
              textInputAction: TextInputAction.newline,
              autocorrect: false,
              enableSuggestions: false,
              decoration: InputDecoration(
                isDense: true,
                hintText: 'Ask the steward…',
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(10),
                ),
                contentPadding: const EdgeInsets.symmetric(
                    horizontal: 12, vertical: 10),
              ),
            ),
          ),
          const SizedBox(width: 6),
          IconButton(
            icon: _sending
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.send, color: DesignColors.primary),
            onPressed: _sending ? null : _send,
          ),
        ],
      ),
    );
  }
}

class _MessageBubble extends StatelessWidget {
  final OverlayChatMessage msg;
  final Color muted;
  const _MessageBubble({required this.msg, required this.muted});

  @override
  Widget build(BuildContext context) {
    final fromUser = msg.role == OverlayChatRole.user;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = fromUser
        ? DesignColors.primary.withValues(alpha: 0.18)
        : (isDark
            ? DesignColors.backgroundDark.withValues(alpha: 0.5)
            : DesignColors.backgroundLight);
    return Align(
      alignment: fromUser ? Alignment.centerRight : Alignment.centerLeft,
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.78,
        ),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: bg,
            borderRadius: BorderRadius.circular(10),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                msg.text,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  color: isDark ? Colors.white : DesignColors.textPrimary,
                ),
              ),
              if (msg.note != null && msg.note!.isNotEmpty) ...[
                const SizedBox(height: 4),
                Text(
                  msg.note!,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: muted,
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  final String error;
  const _ErrorView({required this.error});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline,
                color: Colors.redAccent, size: 28),
            const SizedBox(height: 8),
            Text(
              error,
              textAlign: TextAlign.center,
              style: GoogleFonts.spaceGrotesk(fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}
