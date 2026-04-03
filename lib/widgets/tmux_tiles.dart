import 'package:flutter/material.dart';
import 'package:flutter_muxpod/services/tmux/tmux_parser.dart';
import 'package:flutter_muxpod/theme/design_colors.dart';
import 'package:flutter_muxpod/widgets/active_list_tile.dart';

/// tmuxセッション用ListTile
class TmuxSessionTile extends StatelessWidget {
  final TmuxSession session;
  final bool isActive;
  final VoidCallback? onTap;
  final Widget? trailing;

  const TmuxSessionTile({
    super.key,
    required this.session,
    required this.isActive,
    this.onTap,
    this.trailing,
  });

  @override
  Widget build(BuildContext context) {
    return ActiveListTile(
      isActive: isActive,
      leading: Icon(
        Icons.folder,
        color: ActiveListTile.iconColor(context, isActive: isActive),
      ),
      title: session.name,
      subtitle: '${session.windowCount} windows',
      trailing: trailing,
      onTap: onTap,
    );
  }
}

/// tmuxウィンドウ用ListTile
class TmuxWindowTile extends StatelessWidget {
  final TmuxWindow window;
  final bool isActive;
  final VoidCallback? onTap;
  final VoidCallback? onClose;

  const TmuxWindowTile({
    super.key,
    required this.window,
    required this.isActive,
    this.onTap,
    this.onClose,
  });

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;

    return ActiveListTile(
      isActive: isActive,
      leading: Icon(
        Icons.tab,
        color: ActiveListTile.iconColor(context, isActive: isActive),
      ),
      title: '${window.index}: ${window.name}',
      subtitle: '${window.paneCount} panes',
      trailing: onClose != null
          ? PopupMenuButton<String>(
              icon: Icon(
                Icons.more_vert,
                size: 20,
                color: colorScheme.onSurface.withValues(alpha: 0.6),
              ),
              padding: EdgeInsets.zero,
              itemBuilder: (menuContext) => [
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
                if (value == 'close') {
                  onClose?.call();
                }
              },
            )
          : null,
      onTap: onTap,
    );
  }
}
