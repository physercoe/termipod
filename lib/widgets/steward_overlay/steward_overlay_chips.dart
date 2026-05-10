import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/snippet_provider.dart';
import '../../screens/vault/snippets_screen.dart';
import '../../theme/design_colors.dart';
import 'steward_overlay_controller.dart';

/// W3 of the overlay-history-and-snippets plan — a horizontally
/// scrolling row of one-tap quick actions above the chat input.
///
/// Sources:
///   - User snippets in the existing `snippetsProvider` whose
///     `category == 'steward'` come first, in their stored order.
///   - Three built-in defaults always render at the end so the
///     row is non-empty on a cold install. Cosmetically slightly
///     muted so users can tell which they can replace by editing
///     their snippets.
///
/// On tap, the snippet content is dispatched via
/// `StewardOverlayController.sendUserText` — the same path the
/// chat input uses. The transcript will reflect the user's tap as
/// a user-role bubble after the SSE round-trip (W2's Option A).
///
/// The strip lives OUTSIDE the messages-region Consumer (sibling
/// to `_ChatInputSlot`) so it doesn't rebuild on every SSE event.
/// Snippet store changes ARE a legitimate rebuild trigger, but
/// those happen at user-edit cadence — orders of magnitude rarer
/// than SSE traffic.
class StewardOverlayChips extends ConsumerWidget {
  /// Called when a chip's tap should collapse the panel before
  /// navigating (e.g. the "Edit" chip pushes a full-screen route
  /// — leaving the panel expanded would visually cover it). May
  /// be omitted when the chip strip is hosted somewhere that
  /// doesn't have a panel to dismiss.
  final VoidCallback? onCloseRequested;

  const StewardOverlayChips({super.key, this.onCloseRequested});

  /// Built-in defaults shown when the user hasn't authored any
  /// steward-tagged snippets, OR appended after their custom ones
  /// so the row is never empty. `id` prefixes with `_overlay_default_`
  /// so they can't collide with user snippet ids.
  static const List<Snippet> _defaults = [
    Snippet(
      id: '_overlay_default_insights',
      name: 'Show insights',
      content: 'Show me the insights view',
      category: 'steward',
    ),
    Snippet(
      id: '_overlay_default_blocked',
      name: "What's blocked?",
      content: "What's blocked right now?",
      category: 'steward',
    ),
    Snippet(
      id: '_overlay_default_projects',
      name: 'My projects',
      content: 'Open my projects',
      category: 'steward',
    ),
  ];

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final all = ref.watch(snippetsProvider).snippets;
    final userOwn =
        all.where((s) => s.category == 'steward').toList(growable: false);
    final isDark = Theme.of(context).brightness == Brightness.dark;

    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final s in userOwn) ...[
            _SnippetChip(
              label: s.name,
              tooltip: s.content,
              isDefault: false,
              isDark: isDark,
              onTap: () => _send(ref, s.content),
            ),
            const SizedBox(width: 6),
          ],
          for (final s in _defaults) ...[
            _SnippetChip(
              label: s.name,
              tooltip: s.content,
              isDefault: true,
              isDark: isDark,
              onTap: () => _send(ref, s.content),
            ),
            const SizedBox(width: 6),
          ],
          // Manage chip — opens the existing Snippets screen so the
          // user can add / edit / delete steward-tagged snippets. The
          // built-in defaults are read-only; this is the only path to
          // grow the row beyond the three seeded examples.
          _ManageChip(
            isDark: isDark,
            onCloseRequested: onCloseRequested,
          ),
        ],
      ),
    );
  }

  /// Dispatches the snippet body through the steward controller.
  /// Errors are silently swallowed for snippet taps — a snippet
  /// failing to send shouldn't pop a snackbar over the chat panel
  /// (the panel's own error system note already covers stream
  /// failures). If the steward isn't ready yet, the throw is
  /// expected and the user sees no transcript change.
  Future<void> _send(WidgetRef ref, String content) async {
    try {
      await ref
          .read(stewardOverlayControllerProvider.notifier)
          .sendUserText(content);
    } catch (_) {
      // Best-effort: ignore. Steward may not be ready, or stream
      // may have errored — both already surface their own system
      // notes in the transcript.
    }
  }
}

/// Compact "manage" chip — pencil icon at the trailing edge of the
/// chip strip. Tapped, it pushes the snippets manager page so the
/// user can view / edit / add steward-tagged snippets. Routed
/// through the shared overlayNavigatorKeyProvider since the chip
/// strip lives outside the inner Navigator (same reason as the
/// panel header's "Open in new" button), and collapses the overlay
/// panel before pushing — otherwise the panel sits on top of the
/// destination route and the user sees an apparently-broken page.
class _ManageChip extends ConsumerWidget {
  final bool isDark;
  final VoidCallback? onCloseRequested;
  const _ManageChip({required this.isDark, this.onCloseRequested});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Tooltip(
      message: 'Manage snippets',
      waitDuration: const Duration(milliseconds: 600),
      child: ActionChip(
        materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
        visualDensity: VisualDensity.compact,
        backgroundColor: Colors.transparent,
        side: BorderSide(
          color: DesignColors.primary.withValues(alpha: 0.45),
          width: 1,
        ),
        avatar: Icon(
          Icons.edit_outlined,
          size: 14,
          color: DesignColors.primary.withValues(alpha: 0.85),
        ),
        label: Text(
          'Edit',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 12,
            fontWeight: FontWeight.w500,
            color: isDark
                ? Colors.white.withValues(alpha: 0.85)
                : DesignColors.textPrimary,
          ),
        ),
        onPressed: () {
          final nav = ref.read(overlayNavigatorKeyProvider).currentState;
          if (nav == null) return;
          // Collapse the overlay panel BEFORE the push lands so the
          // destination route renders on a clean canvas. Otherwise
          // the StewardOverlay's Stack keeps painting the panel on
          // top of the new route. Plumbed from StewardOverlayChat's
          // onCloseRequested callback (= _ExpandedPanel.onClose).
          onCloseRequested?.call();
          nav.push(
            MaterialPageRoute(builder: (_) => const _SnippetsManagePage()),
          );
        },
      ),
    );
  }
}

/// Hosts the bare-Column [SnippetsScreen] body inside a real route
/// chrome — Scaffold + AppBar + scrollable content. The screen
/// itself was authored as an *embedded* widget for the Vault page
/// (see `screens/vault/vault_screen.dart`); it returns a plain
/// Column, no Material ancestor of its own, no scroll view. Pushing
/// it directly as a MaterialPageRoute child showed the v1.0.479 QA
/// symptoms: yellow "missing Material" double-underlines on every
/// Text, no scroll past the viewport, no AppBar / system-inset
/// padding.
class _SnippetsManagePage extends StatelessWidget {
  const _SnippetsManagePage();

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Snippets'),
      ),
      body: const SafeArea(
        child: SingleChildScrollView(
          padding: EdgeInsets.fromLTRB(16, 12, 16, 24),
          child: SnippetsScreen(),
        ),
      ),
    );
  }
}

class _SnippetChip extends StatelessWidget {
  final String label;
  final String tooltip;
  final bool isDefault;
  final bool isDark;
  final VoidCallback onTap;

  const _SnippetChip({
    required this.label,
    required this.tooltip,
    required this.isDefault,
    required this.isDark,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final baseTextColor = isDark ? Colors.white : DesignColors.textPrimary;
    // Defaults are slightly muted so users can distinguish them
    // from their own snippets at a glance.
    final fg = isDefault ? baseTextColor.withValues(alpha: 0.65) : baseTextColor;
    return Tooltip(
      message: tooltip,
      waitDuration: const Duration(milliseconds: 600),
      child: ActionChip(
        materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
        visualDensity: VisualDensity.compact,
        backgroundColor: DesignColors.primary.withValues(alpha: 0.10),
        side: BorderSide(
          color: DesignColors.primary.withValues(alpha: 0.35),
          width: 1,
        ),
        label: Text(
          label,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 12,
            fontWeight: FontWeight.w500,
            color: fg,
          ),
        ),
        onPressed: onTap,
      ),
    );
  }
}
