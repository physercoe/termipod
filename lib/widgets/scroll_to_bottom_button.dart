import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../theme/design_colors.dart';

/// Button to scroll to bottom of terminal content.
///
/// Positioned above the ESC/TAB bar. Listens to a ScrollController to
/// auto-show when not at bottom and auto-hide when at bottom.
class ScrollToBottomButton extends StatefulWidget {
  final VoidCallback onPressed;
  final ScrollController? scrollController;

  const ScrollToBottomButton({
    super.key,
    required this.onPressed,
    this.scrollController,
  });

  @override
  State<ScrollToBottomButton> createState() => ScrollToBottomButtonState();
}

class ScrollToBottomButtonState extends State<ScrollToBottomButton> {
  bool _visible = false;

  /// Threshold (pixels from bottom) to consider "at bottom"
  static const double _atBottomThreshold = 20.0;

  @override
  void initState() {
    super.initState();
    widget.scrollController?.addListener(_onScrollChanged);
  }

  @override
  void didUpdateWidget(ScrollToBottomButton oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.scrollController != widget.scrollController) {
      oldWidget.scrollController?.removeListener(_onScrollChanged);
      widget.scrollController?.addListener(_onScrollChanged);
    }
  }

  @override
  void dispose() {
    widget.scrollController?.removeListener(_onScrollChanged);
    super.dispose();
  }

  /// Auto-show/hide based on scroll position
  void _onScrollChanged() {
    if (!mounted) return;
    final controller = widget.scrollController;
    if (controller == null || !controller.hasClients) return;

    final position = controller.position;
    final isAtBottom =
        position.pixels >= position.maxScrollExtent - _atBottomThreshold;

    if (isAtBottom && _visible) {
      setState(() => _visible = false);
    } else if (!isAtBottom && !_visible) {
      setState(() => _visible = true);
    }
  }

  /// Programmatically show the button (e.g. after resize)
  void show() {
    if (!mounted) return;
    if (!_visible) {
      setState(() => _visible = true);
    }
  }

  /// Programmatically hide the button
  void hide() {
    if (!mounted) return;
    if (_visible) {
      setState(() => _visible = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    if (!_visible) return const SizedBox.shrink();

    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    final bgColor =
        isDark ? DesignColors.keyBackground : DesignColors.keyBackgroundLight;
    final borderColor = colorScheme.outline.withValues(alpha: 0.3);
    final iconColor = colorScheme.onSurface.withValues(alpha: 0.8);

    return GestureDetector(
      onTap: () {
        HapticFeedback.lightImpact();
        widget.onPressed();
      },
      child: Container(
        width: 36,
        height: 36,
        decoration: BoxDecoration(
          color: bgColor,
          borderRadius: BorderRadius.circular(18),
          border: Border.all(color: borderColor),
          boxShadow: [
            BoxShadow(
              color: Colors.black.withValues(alpha: isDark ? 0.4 : 0.15),
              blurRadius: 4,
              offset: const Offset(0, 2),
            ),
          ],
        ),
        child: Icon(
          Icons.keyboard_double_arrow_down,
          size: 18,
          color: iconColor,
        ),
      ),
    );
  }
}
