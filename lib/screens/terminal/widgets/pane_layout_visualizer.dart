import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../l10n/app_localizations.dart';
import '../../../services/tmux/tmux_commands.dart';
import '../../../services/tmux/tmux_parser.dart';
import '../../../theme/design_colors.dart';
import 'pane_layout_painters.dart';

/// ペインレイアウトをインタラクティブに表示するウィジェット
///
/// 各ペインをタップで選択可能。ペイン番号も表示。
class PaneLayoutVisualizer extends StatefulWidget {
  final List<TmuxPane> panes;
  final String? activePaneId;
  final void Function(String paneId) onPaneSelected;
  final void Function(String paneId, SplitDirection direction)? onSplitRequested;

  const PaneLayoutVisualizer({
    super.key,
    required this.panes,
    this.activePaneId,
    required this.onPaneSelected,
    this.onSplitRequested,
  });

  @override
  State<PaneLayoutVisualizer> createState() => _PaneLayoutVisualizerState();
}

class _PaneLayoutVisualizerState extends State<PaneLayoutVisualizer> {
  /// 分割モードが有効なペインID（nullなら通常表示）
  String? _splitModeActivePaneId;

  @override
  Widget build(BuildContext context) {
    if (widget.panes.isEmpty) return const SizedBox.shrink();

    int maxRight = 0;
    int maxBottom = 0;
    for (final pane in widget.panes) {
      final right = pane.left + pane.width;
      final bottom = pane.top + pane.height;
      if (right > maxRight) maxRight = right;
      if (bottom > maxBottom) maxBottom = bottom;
    }

    if (maxRight == 0 || maxBottom == 0) return const SizedBox.shrink();

    final aspectRatio = maxRight / maxBottom;

    return Container(
      padding: const EdgeInsets.all(16),
      child: AspectRatio(
        aspectRatio: aspectRatio.clamp(0.5, 3.0),
        child: LayoutBuilder(
          builder: (context, constraints) {
            final containerWidth = constraints.maxWidth;
            final containerHeight = constraints.maxHeight;

            final scaleX = containerWidth / maxRight;
            final scaleY = containerHeight / maxBottom;
            const gap = 2.0;

            return Stack(
              children: widget.panes.map((pane) {
                final isActive = pane.id == widget.activePaneId;
                final isSplitMode = _splitModeActivePaneId == pane.id;

                final left = pane.left * scaleX;
                final top = pane.top * scaleY;
                final width = pane.width * scaleX - gap;
                final height = pane.height * scaleY - gap;

                return Positioned(
                  left: left,
                  top: top,
                  width: width,
                  height: height,
                  child: GestureDetector(
                    onTap: () => _handlePaneTap(pane, isActive, width, height),
                    child: AnimatedContainer(
                      duration: const Duration(milliseconds: 200),
                      decoration: BoxDecoration(
                        color: isActive
                            ? DesignColors.primary.withValues(alpha: 0.3)
                            : Colors.black45,
                        borderRadius: BorderRadius.circular(4),
                        border: Border.all(
                          color: isActive
                              ? DesignColors.primary
                              : Colors.white.withValues(alpha: 0.3),
                          width: isActive ? 2 : 1,
                        ),
                      ),
                      child: Center(
                        child: _buildPaneContent(
                          pane: pane,
                          isActive: isActive,
                          isSplitMode: isSplitMode,
                          width: width,
                          height: height,
                        ),
                      ),
                    ),
                  ),
                );
              }).toList(),
            );
          },
        ),
      ),
    );
  }

  /// インライン分割アイコンが収まる最小サイズ
  static const _minInlineWidth = 80.0;
  static const _minInlineHeight = 60.0;

  void _handlePaneTap(TmuxPane pane, bool isActive, double width, double height) {
    if (isActive && widget.onSplitRequested != null) {
      if (width < _minInlineWidth || height < _minInlineHeight) {
        _showSplitDialog(pane);
      } else {
        setState(() {
          _splitModeActivePaneId =
              _splitModeActivePaneId == pane.id ? null : pane.id;
        });
      }
    } else {
      widget.onPaneSelected(pane.id);
    }
  }

  void _showSplitDialog(TmuxPane pane) {
    showDialog(
      context: context,
      builder: (dialogContext) {
        final colorScheme = Theme.of(dialogContext).colorScheme;
        return AlertDialog(
          title: Text(
            AppLocalizations.of(context)!.splitPaneTitle(pane.index),
            style: GoogleFonts.spaceGrotesk(
              fontWeight: FontWeight.w700,
            ),
          ),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              ListTile(
                leading: CustomPaint(
                  size: const Size(24, 24),
                  painter: SplitRightIconPainter(color: colorScheme.primary),
                ),
                title: Text(AppLocalizations.of(context)!.splitRight),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(8),
                ),
                onTap: () {
                  Navigator.pop(dialogContext);
                  widget.onSplitRequested!(pane.id, SplitDirection.horizontal);
                },
              ),
              ListTile(
                leading: CustomPaint(
                  size: const Size(24, 24),
                  painter: SplitDownIconPainter(color: colorScheme.primary),
                ),
                title: Text(AppLocalizations.of(context)!.splitDown),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(8),
                ),
                onTap: () {
                  Navigator.pop(dialogContext);
                  widget.onSplitRequested!(pane.id, SplitDirection.vertical);
                },
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext),
              child: Text(AppLocalizations.of(context)!.buttonCancel),
            ),
          ],
        );
      },
    );
  }

  Widget _buildPaneContent({
    required TmuxPane pane,
    required bool isActive,
    required bool isSplitMode,
    required double width,
    required double height,
  }) {
    if (isActive && isSplitMode) {
      return Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Text(
            '${pane.index}',
            style: GoogleFonts.jetBrainsMono(
              fontSize: width > 60 ? 18 : 14,
              fontWeight: FontWeight.w700,
              color: DesignColors.primary,
            ),
          ),
          const SizedBox(height: 4),
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              _buildSplitButton(
                painter: SplitRightIconPainter(color: DesignColors.primary),
                onTap: () => widget.onSplitRequested!(
                  pane.id,
                  SplitDirection.horizontal,
                ),
              ),
              const SizedBox(width: 8),
              _buildSplitButton(
                painter: SplitDownIconPainter(color: DesignColors.primary),
                onTap: () => widget.onSplitRequested!(
                  pane.id,
                  SplitDirection.vertical,
                ),
              ),
            ],
          ),
        ],
      );
    }

    return Column(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        Text(
          '${pane.index}',
          style: GoogleFonts.jetBrainsMono(
            fontSize: width > 60 ? 18 : 14,
            fontWeight: FontWeight.w700,
            color: isActive
                ? DesignColors.primary
                : Colors.white.withValues(alpha: 0.7),
          ),
        ),
        if (isActive && widget.onSplitRequested != null && width > 60 && height > 40) ...[
          const SizedBox(height: 2),
          Text(
            'Tap to split',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 8,
              color: DesignColors.primary.withValues(alpha: 0.7),
            ),
          ),
        ] else if (width > 80 && height > 50) ...[
          const SizedBox(height: 2),
          Text(
            '${pane.width}x${pane.height}',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 9,
              color: Colors.white.withValues(alpha: 0.5),
            ),
          ),
        ],
      ],
    );
  }

  Widget _buildSplitButton({
    required CustomPainter painter,
    required VoidCallback onTap,
  }) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(6),
        child: Container(
          padding: const EdgeInsets.all(6),
          decoration: BoxDecoration(
            color: DesignColors.primary.withValues(alpha: 0.15),
            borderRadius: BorderRadius.circular(6),
            border: Border.all(
              color: DesignColors.primary.withValues(alpha: 0.4),
            ),
          ),
          child: CustomPaint(
            size: const Size(20, 20),
            painter: painter,
          ),
        ),
      ),
    );
  }
}
