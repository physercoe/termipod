import 'package:flutter/material.dart';

import '../../../l10n/app_localizations.dart';
import '../../../services/tmux/tmux_parser.dart';
import '../../../theme/design_colors.dart';

/// リサイズ対象ペインをグラフィカルに選択するダイアログ
class ResizePaneChooserDialog extends StatefulWidget {
  final List<TmuxPane> panes;
  final String? activePaneId;
  final void Function(TmuxPane selectedPane) onResize;

  const ResizePaneChooserDialog({
    super.key,
    required this.panes,
    this.activePaneId,
    required this.onResize,
  });

  @override
  State<ResizePaneChooserDialog> createState() =>
      _ResizePaneChooserDialogState();
}

class _ResizePaneChooserDialogState extends State<ResizePaneChooserDialog> {
  late String? _selectedPaneId;

  @override
  void initState() {
    super.initState();
    _selectedPaneId = widget.activePaneId;
  }

  TmuxPane? get _selectedPane {
    if (_selectedPaneId == null) return null;
    try {
      return widget.panes.firstWhere((p) => p.id == _selectedPaneId);
    } catch (_) {
      return null;
    }
  }

  @override
  Widget build(BuildContext context) {
    final selected = _selectedPane;

    return AlertDialog(
      backgroundColor: DesignColors.surfaceDark,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      title: Text(
        AppLocalizations.of(context)!.resizePaneTitle,
        style: const TextStyle(color: DesignColors.textPrimary),
      ),
      content: SizedBox(
        width: MediaQuery.of(context).size.width * 0.8,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _buildSelectablePaneGrid(),
              const SizedBox(height: 12),
              if (selected != null)
                Text(
                  AppLocalizations.of(context)!.paneSelectionInfo(
                      selected.index, selected.width, selected.height),
                  style: const TextStyle(
                    fontSize: 13,
                    color: DesignColors.textSecondary,
                  ),
                )
              else
                Text(
                  AppLocalizations.of(context)!.selectWindowPrompt,
                  style: const TextStyle(
                    fontSize: 13,
                    color: DesignColors.textSecondary,
                  ),
                ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text(AppLocalizations.of(context)!.buttonCancel),
        ),
        FilledButton(
          onPressed: selected != null ? () => widget.onResize(selected) : null,
          style: FilledButton.styleFrom(
            backgroundColor: DesignColors.primary,
          ),
          child: Text(AppLocalizations.of(context)!.buttonResize),
        ),
      ],
    );
  }

  Widget _buildSelectablePaneGrid() {
    if (widget.panes.isEmpty) return const SizedBox.shrink();

    int maxRight = 0;
    int maxBottom = 0;
    for (final pane in widget.panes) {
      final right = pane.left + pane.width;
      final bottom = pane.top + pane.height;
      if (right > maxRight) maxRight = right;
      if (bottom > maxBottom) maxBottom = bottom;
    }
    if (maxRight == 0) maxRight = 1;
    if (maxBottom == 0) maxBottom = 1;

    return Container(
      height: 150,
      clipBehavior: Clip.hardEdge,
      decoration: BoxDecoration(
        color: DesignColors.canvasDark,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: DesignColors.borderDark),
      ),
      child: LayoutBuilder(
        builder: (context, constraints) {
          const pad = 4.0;
          final areaW = constraints.maxWidth - pad * 2;
          final areaH = constraints.maxHeight - pad * 2;
          final scaleX = areaW / maxRight;
          final scaleY = areaH / maxBottom;

          return Padding(
            padding: const EdgeInsets.all(pad),
            child: Stack(
              children: [
                SizedBox(width: areaW, height: areaH),
                ...widget.panes.map((pane) {
                  final isSelected = pane.id == _selectedPaneId;
                  final left = pane.left * scaleX;
                  final top = pane.top * scaleY;
                  final width =
                      (pane.width * scaleX).clamp(20.0, areaW - left);
                  final height =
                      (pane.height * scaleY).clamp(14.0, areaH - top);

                  return Positioned(
                    left: left,
                    top: top,
                    width: width,
                    height: height,
                    child: GestureDetector(
                      onTap: () => setState(() => _selectedPaneId = pane.id),
                      child: AnimatedContainer(
                        duration: const Duration(milliseconds: 150),
                        decoration: BoxDecoration(
                          color: isSelected
                              ? DesignColors.primary.withValues(alpha: 0.25)
                              : DesignColors.surfaceDark,
                          borderRadius: BorderRadius.circular(4),
                          border: Border.all(
                            color: isSelected
                                ? DesignColors.primary
                                : DesignColors.borderDark,
                            width: isSelected ? 2 : 1,
                          ),
                        ),
                        alignment: Alignment.center,
                        child: FittedBox(
                          fit: BoxFit.scaleDown,
                          child: Padding(
                            padding:
                                const EdgeInsets.symmetric(horizontal: 2),
                            child: Text(
                              '${pane.index}\n${pane.width}x${pane.height}',
                              textAlign: TextAlign.center,
                              style: TextStyle(
                                fontSize: 10,
                                color: isSelected
                                    ? DesignColors.primary
                                    : DesignColors.textSecondary,
                              ),
                            ),
                          ),
                        ),
                      ),
                    ),
                  );
                }),
              ],
            ),
          );
        },
      ),
    );
  }
}

/// リサイズ対象ウィンドウをグラフィカルに選択するダイアログ
class ResizeWindowChooserDialog extends StatefulWidget {
  final List<TmuxWindow> windows;
  final int? activeWindowIndex;
  final void Function(TmuxWindow selectedWindow) onResize;

  const ResizeWindowChooserDialog({
    super.key,
    required this.windows,
    this.activeWindowIndex,
    required this.onResize,
  });

  @override
  State<ResizeWindowChooserDialog> createState() =>
      _ResizeWindowChooserDialogState();
}

class _ResizeWindowChooserDialogState
    extends State<ResizeWindowChooserDialog> {
  late int? _selectedWindowIndex;

  @override
  void initState() {
    super.initState();
    _selectedWindowIndex = widget.activeWindowIndex;
  }

  TmuxWindow? get _selectedWindow {
    if (_selectedWindowIndex == null) return null;
    try {
      return widget.windows
          .firstWhere((w) => w.index == _selectedWindowIndex);
    } catch (_) {
      return null;
    }
  }

  @override
  Widget build(BuildContext context) {
    final selected = _selectedWindow;

    return AlertDialog(
      backgroundColor: DesignColors.surfaceDark,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      title: Text(
        AppLocalizations.of(context)!.resizeWindowTitle,
        style: const TextStyle(color: DesignColors.textPrimary),
      ),
      content: SizedBox(
        width: MediaQuery.of(context).size.width * 0.8,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              ...widget.windows.map((window) {
                final isSelected = window.index == _selectedWindowIndex;
                return Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: _buildWindowCard(window, isSelected),
                );
              }),
              const SizedBox(height: 4),
              if (selected != null) ...[
                Text(
                  'Selected: ${selected.name} (${_windowSizeString(selected)})',
                  style: const TextStyle(
                    fontSize: 13,
                    color: DesignColors.textSecondary,
                  ),
                ),
              ] else
                Text(
                  AppLocalizations.of(context)!.selectWindowPrompt,
                  style: const TextStyle(
                    fontSize: 13,
                    color: DesignColors.textSecondary,
                  ),
                ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text(AppLocalizations.of(context)!.buttonCancel),
        ),
        FilledButton(
          onPressed:
              selected != null ? () => widget.onResize(selected) : null,
          style: FilledButton.styleFrom(
            backgroundColor: DesignColors.primary,
          ),
          child: Text(AppLocalizations.of(context)!.buttonResize),
        ),
      ],
    );
  }

  String _windowSizeString(TmuxWindow window) {
    final panes = window.panes;
    if (panes.isEmpty) return '?x?';
    final cols =
        panes.map((p) => p.left + p.width).reduce((a, b) => a > b ? a : b);
    final rows =
        panes.map((p) => p.top + p.height).reduce((a, b) => a > b ? a : b);
    return '${cols}x$rows';
  }

  Widget _buildWindowCard(TmuxWindow window, bool isSelected) {
    final panes = window.panes;
    return GestureDetector(
      onTap: () => setState(() => _selectedWindowIndex = window.index),
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 150),
        decoration: BoxDecoration(
          color: DesignColors.canvasDark,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isSelected ? DesignColors.primary : DesignColors.borderDark,
            width: isSelected ? 2 : 1,
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Container(
              width: double.infinity,
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
              decoration: BoxDecoration(
                color: isSelected
                    ? DesignColors.primary.withValues(alpha: 0.15)
                    : DesignColors.surfaceDark,
                borderRadius: const BorderRadius.only(
                  topLeft: Radius.circular(7),
                  topRight: Radius.circular(7),
                ),
              ),
              child: Text(
                '${window.name}  ${_windowSizeString(window)}',
                style: TextStyle(
                  fontSize: 12,
                  color: isSelected
                      ? DesignColors.primary
                      : DesignColors.textPrimary,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            if (panes.isNotEmpty)
              SizedBox(
                height: 60,
                child: _buildPaneLayoutPreview(panes),
              ),
          ],
        ),
      ),
    );
  }

  Widget _buildPaneLayoutPreview(List<TmuxPane> panes) {
    int maxRight = 0;
    int maxBottom = 0;
    for (final p in panes) {
      final right = p.left + p.width;
      final bottom = p.top + p.height;
      if (right > maxRight) maxRight = right;
      if (bottom > maxBottom) maxBottom = bottom;
    }
    if (maxRight == 0) maxRight = 1;
    if (maxBottom == 0) maxBottom = 1;

    return LayoutBuilder(
      builder: (context, constraints) {
        final areaW = constraints.maxWidth - 8;
        final areaH = constraints.maxHeight - 8;

        return Padding(
          padding: const EdgeInsets.all(4),
          child: Stack(
            children: [
              SizedBox(width: areaW, height: areaH),
              ...panes.map((pane) {
                final left = (pane.left / maxRight) * areaW;
                final top = (pane.top / maxBottom) * areaH;
                final width = (pane.width / maxRight) * areaW;
                final height = (pane.height / maxBottom) * areaH;

                return Positioned(
                  left: left,
                  top: top,
                  width: width.clamp(16.0, areaW),
                  height: height.clamp(10.0, areaH),
                  child: Container(
                    decoration: BoxDecoration(
                      borderRadius: BorderRadius.circular(2),
                      border: Border.all(
                        color: DesignColors.borderDark,
                        width: 1,
                      ),
                    ),
                  ),
                );
              }),
            ],
          ),
        );
      },
    );
  }
}
