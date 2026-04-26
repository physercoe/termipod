import 'package:flutter/material.dart';
import 'package:termipod/l10n/app_localizations.dart';
import 'package:termipod/services/tmux/tmux_parser.dart';
import 'package:termipod/theme/design_colors.dart';
import 'package:termipod/widgets/active_list_tile.dart';

/// tmuxセッション用ListTile
class TmuxSessionTile extends StatelessWidget {
  final TmuxSession session;
  final bool isActive;
  final VoidCallback? onTap;
  final VoidCallback? onRename;
  final Widget? trailing;

  const TmuxSessionTile({
    super.key,
    required this.session,
    required this.isActive,
    this.onTap,
    this.onRename,
    this.trailing,
  });

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    // Caller-provided trailing (eg. expand/collapse arrow in SessionTree)
    // wins; otherwise show a kebab when a rename action is wired up.
    Widget? effectiveTrailing = trailing;
    if (effectiveTrailing == null && onRename != null) {
      effectiveTrailing = PopupMenuButton<String>(
        icon: Icon(Icons.more_vert,
            size: 20,
            color: colorScheme.onSurface.withValues(alpha: 0.6)),
        padding: EdgeInsets.zero,
        itemBuilder: (_) => const [
          PopupMenuItem(value: 'rename', child: Text('Rename session')),
        ],
        onSelected: (v) {
          if (v == 'rename') onRename?.call();
        },
      );
    }
    return ActiveListTile(
      isActive: isActive,
      leading: Icon(
        Icons.folder,
        color: ActiveListTile.iconColor(context, isActive: isActive),
      ),
      title: session.name,
      subtitle: '${session.windowCount} windows',
      trailing: effectiveTrailing,
      onTap: onTap,
    );
  }
}

/// tmuxペイン用ListTile
class TmuxPaneTile extends StatelessWidget {
  final TmuxPane pane;
  final String paneTitle;
  final bool isActive;
  final VoidCallback? onTap;
  final VoidCallback? onLongPress;
  final VoidCallback? onResize;
  final VoidCallback? onClose;

  const TmuxPaneTile({
    super.key,
    required this.pane,
    required this.paneTitle,
    required this.isActive,
    this.onTap,
    this.onLongPress,
    this.onResize,
    this.onClose,
  });

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;

    return ActiveListTile(
      isActive: isActive,
      leading: Container(
        width: 32,
        height: 32,
        decoration: BoxDecoration(
          color: isActive
              ? colorScheme.primary.withValues(alpha: 0.2)
              : colorScheme.onSurface.withValues(alpha: 0.05),
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isActive
                ? colorScheme.primary.withValues(alpha: 0.5)
                : colorScheme.onSurface.withValues(alpha: 0.1),
          ),
        ),
        child: Center(
          child: Text(
            '${pane.index}',
            style: TextStyle(
              fontFamily: 'JetBrains Mono',
              fontSize: 14,
              fontWeight: FontWeight.w700,
              color: isActive
                  ? colorScheme.primary
                  : colorScheme.onSurface.withValues(alpha: 0.6),
            ),
          ),
        ),
      ),
      title: paneTitle,
      subtitle: '${pane.width}x${pane.height}',
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (onResize != null || onClose != null)
            PopupMenuButton<String>(
              icon: Icon(
                Icons.more_vert,
                size: 20,
                color: colorScheme.onSurface.withValues(alpha: 0.6),
              ),
              padding: EdgeInsets.zero,
              itemBuilder: (menuContext) => [
                if (onResize != null)
                  PopupMenuItem(
                    value: 'resize',
                    child: Row(
                      children: [
                        Icon(Icons.aspect_ratio, size: 18,
                            color: colorScheme.onSurface),
                        const SizedBox(width: 8),
                        Text(AppLocalizations.of(menuContext)!.resizePaneTitle),
                      ],
                    ),
                  ),
                if (onClose != null)
                  PopupMenuItem(
                    value: 'close',
                    child: Row(
                      children: [
                        Icon(Icons.close, size: 18, color: DesignColors.error),
                        const SizedBox(width: 8),
                        Text('Close Pane',
                            style: TextStyle(color: DesignColors.error)),
                      ],
                    ),
                  ),
              ],
              onSelected: (value) {
                if (value == 'resize') {
                  onResize?.call();
                } else if (value == 'close') {
                  onClose?.call();
                }
              },
            ),
        ],
      ),
      onTap: onTap,
      onLongPress: onLongPress,
    );
  }
}

/// tmuxウィンドウ用ListTile
class TmuxWindowTile extends StatelessWidget {
  final TmuxWindow window;
  final bool isActive;
  final VoidCallback? onTap;
  final VoidCallback? onRename;
  final VoidCallback? onResize;
  final VoidCallback? onClose;

  const TmuxWindowTile({
    super.key,
    required this.window,
    required this.isActive,
    this.onTap,
    this.onRename,
    this.onResize,
    this.onClose,
  });

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    final hasMenu = onRename != null || onResize != null || onClose != null;

    return ActiveListTile(
      isActive: isActive,
      leading: Icon(
        Icons.tab,
        color: ActiveListTile.iconColor(context, isActive: isActive),
      ),
      title: '${window.index}: ${window.name}',
      subtitle: '${window.paneCount} panes',
      trailing: hasMenu
          ? PopupMenuButton<String>(
              icon: Icon(
                Icons.more_vert,
                size: 20,
                color: colorScheme.onSurface.withValues(alpha: 0.6),
              ),
              padding: EdgeInsets.zero,
              itemBuilder: (menuContext) => [
                if (onRename != null)
                  PopupMenuItem(
                    value: 'rename',
                    child: Row(
                      children: [
                        Icon(Icons.edit_outlined, size: 18,
                            color: colorScheme.onSurface),
                        const SizedBox(width: 8),
                        const Text('Rename window'),
                      ],
                    ),
                  ),
                if (onResize != null)
                  PopupMenuItem(
                    value: 'resize',
                    child: Row(
                      children: [
                        Icon(Icons.aspect_ratio, size: 18,
                            color: colorScheme.onSurface),
                        const SizedBox(width: 8),
                        Text(AppLocalizations.of(menuContext)!.resizeWindowTitle),
                      ],
                    ),
                  ),
                if (onClose != null)
                  PopupMenuItem(
                    value: 'close',
                    child: Row(
                      children: [
                        Icon(Icons.close, size: 18, color: DesignColors.error),
                        const SizedBox(width: 8),
                        Text('Close Window', style: TextStyle(color: DesignColors.error)),
                      ],
                    ),
                  ),
              ],
              onSelected: (value) {
                if (value == 'rename') {
                  onRename?.call();
                } else if (value == 'resize') {
                  onResize?.call();
                } else if (value == 'close') {
                  onClose?.call();
                }
              },
            )
          : null,
      onTap: onTap,
    );
  }
}
