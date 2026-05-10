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
  final _inputCtrl = TextEditingController();
  final _scrollCtrl = ScrollController();
  bool _sending = false;

  @override
  void dispose() {
    _inputCtrl.dispose();
    _scrollCtrl.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final text = _inputCtrl.text.trim();
    if (text.isEmpty) return;
    final controller = ref.read(stewardOverlayControllerProvider.notifier);
    setState(() => _sending = true);
    try {
      await controller.sendUserText(text);
      _inputCtrl.clear();
    } catch (e) {
      if (mounted) {
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

    return Column(
      children: [
        Expanded(
          child: state.messages.isEmpty
              ? Center(
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
                )
              : ListView.separated(
                  controller: _scrollCtrl,
                  padding: const EdgeInsets.fromLTRB(12, 12, 12, 12),
                  itemCount: state.messages.length,
                  separatorBuilder: (_, __) => const SizedBox(height: 8),
                  itemBuilder: (_, i) => _MessageBubble(
                    msg: state.messages[i],
                    muted: muted,
                  ),
                ),
        ),
        const Divider(height: 1),
        _Composer(
          controller: _inputCtrl,
          sending: _sending,
          onSend: _send,
        ),
      ],
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

class _Composer extends StatelessWidget {
  final TextEditingController controller;
  final bool sending;
  final VoidCallback onSend;
  const _Composer({
    required this.controller,
    required this.sending,
    required this.onSend,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(8, 8, 8, 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Expanded(
            child: TextField(
              controller: controller,
              minLines: 1,
              maxLines: 4,
              decoration: InputDecoration(
                isDense: true,
                hintText: 'Ask the steward…',
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(10),
                ),
                contentPadding: const EdgeInsets.symmetric(
                    horizontal: 12, vertical: 10),
              ),
              onSubmitted: (_) => onSend(),
            ),
          ),
          const SizedBox(width: 6),
          IconButton(
            icon: sending
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.send, color: DesignColors.primary),
            onPressed: sending ? null : onSend,
          ),
        ],
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
