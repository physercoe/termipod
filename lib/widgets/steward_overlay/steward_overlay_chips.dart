import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../models/snippet_presets.dart';
import '../../providers/snippet_provider.dart';
import '../../screens/vault/snippets_screen.dart';
import '../../theme/design_colors.dart';
import 'steward_overlay_controller.dart';

/// SnippetPresets profile key for the overlay starter chips.
const String _stewardProfileId = 'steward';

/// W3 of the overlay-history-and-snippets plan — a horizontally
/// scrolling row of one-tap quick actions above the chat input.
///
/// Sources, in render order:
///   1. The user's own `category == 'steward'` snippets (most-specific
///      first).
///   2. Preset starter chips from `SnippetPresets.forProfile('steward')`,
///      with user `presetOverrides` applied and `deletedPresetIds`
///      removed. Renders muted so users can tell them apart from their
///      own snippets at a glance. The starter set is editable via the
///      Edit chip and resetable via swipe-to-reset on the manage page.
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
  const StewardOverlayChips({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(snippetsProvider);
    final userOwn = state.snippets
        .where((s) => s.category == 'steward')
        .toList(growable: false);
    final presets = [
      for (final p in SnippetPresets.forProfile(_stewardProfileId))
        if (!state.deletedPresetIds.contains(p.id))
          state.presetOverrides[p.id] ?? p,
    ];
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
          for (final s in presets) ...[
            _SnippetChip(
              label: s.name,
              tooltip: s.content,
              isDefault: true,
              isDark: isDark,
              onTap: () => _send(ref, s.content),
            ),
            const SizedBox(width: 6),
          ],
          // Manage chip — opens the manage page so the user can add /
          // edit / delete / reset their steward-tagged snippets and
          // starter presets.
          _ManageChip(isDark: isDark),
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
/// panel header's "Open in new" button).
///
/// **The panel deliberately stays open across this push.** Per
/// ADR-023 D1 the overlay is persistent across all routes; the
/// user can drag/resize/dim the panel themselves, and explicit
/// dismissal lives on the X / puck. Auto-collapse here would
/// also surprise the user — `mobile.navigate`-driven pushes
/// keep the panel open, so a chip-driven push should too.
/// (The `_openFullSession` header button is the documented
/// exception: it opens the steward's full session transcript,
/// which IS the same conversation as the panel — leaving both
/// open would be redundant.)
class _ManageChip extends ConsumerWidget {
  final bool isDark;
  const _ManageChip({required this.isDark});

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
///
/// The AppBar exposes an Add action. `SnippetsScreen` itself doesn't
/// carry one (it's a body widget designed for the Vault page, whose
/// section header owns the Add button). Entering from the overlay we
/// pre-fill the new snippet's category as `steward` so the result
/// surfaces on the chip strip without an extra category-picker hop.
class _SnippetsManagePage extends ConsumerWidget {
  const _SnippetsManagePage();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Snippets'),
        actions: [
          IconButton(
            icon: const Icon(Icons.add),
            tooltip: 'Add steward snippet',
            onPressed: () => _addSnippet(context, ref),
          ),
        ],
      ),
      body: const SafeArea(
        child: SingleChildScrollView(
          padding: EdgeInsets.fromLTRB(16, 12, 16, 24),
          child: SnippetsScreen(),
        ),
      ),
    );
  }

  void _addSnippet(BuildContext context, WidgetRef ref) {
    showDialog(
      context: context,
      builder: (ctx) => SnippetEditDialog(
        initialCategory: _stewardProfileId,
        onSave: (name, content, category, variables) {
          ref.read(snippetsProvider.notifier).addSnippet(
                name: name,
                content: content,
                category: category,
                variables: variables,
              );
        },
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
