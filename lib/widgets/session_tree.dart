import 'package:flutter/material.dart';
import 'package:flutter_muxpod/services/tmux/tmux_parser.dart';
import 'package:flutter_muxpod/widgets/active_list_tile.dart';
import 'package:flutter_muxpod/widgets/tmux_tiles.dart';

/// tmuxセッションツリー表示Widget
/// 仮想スクロール対応: ListView.builder + 遅延ウィジェット生成
class SessionTree extends StatelessWidget {
  final List<TmuxSession> sessions;
  final String? selectedPaneId;
  final void Function(String paneId)? onPaneSelected;
  final void Function(String sessionName)? onSessionDoubleTap;
  final void Function(String sessionName, int windowIndex, String windowName, bool isLastWindow)? onWindowClose;

  const SessionTree({
    super.key,
    required this.sessions,
    this.selectedPaneId,
    this.onPaneSelected,
    this.onSessionDoubleTap,
    this.onWindowClose,
  });

  @override
  Widget build(BuildContext context) {
    if (sessions.isEmpty) {
      return const Center(
        child: Text('No tmux sessions'),
      );
    }

    return ListView.builder(
      itemCount: sessions.length,
      addAutomaticKeepAlives: false,
      addRepaintBoundaries: true,
      itemBuilder: (context, index) {
        return _SessionTile(
          session: sessions[index],
          selectedPaneId: selectedPaneId,
          onPaneSelected: onPaneSelected,
          onSessionDoubleTap: onSessionDoubleTap,
          onWindowClose: onWindowClose,
        );
      },
    );
  }
}

/// セッションタイル（展開状態を管理して遅延生成）
class _SessionTile extends StatefulWidget {
  final TmuxSession session;
  final String? selectedPaneId;
  final void Function(String paneId)? onPaneSelected;
  final void Function(String sessionName)? onSessionDoubleTap;
  final void Function(String sessionName, int windowIndex, String windowName, bool isLastWindow)? onWindowClose;

  const _SessionTile({
    required this.session,
    this.selectedPaneId,
    this.onPaneSelected,
    this.onSessionDoubleTap,
    this.onWindowClose,
  });

  @override
  State<_SessionTile> createState() => _SessionTileState();
}

class _SessionTileState extends State<_SessionTile> {
  late bool _isExpanded;

  @override
  void initState() {
    super.initState();
    _isExpanded = widget.session.attached;
  }

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;

    return Column(
      children: [
        GestureDetector(
          onDoubleTap: () => widget.onSessionDoubleTap?.call(widget.session.name),
          child: TmuxSessionTile(
            session: widget.session,
            isActive: widget.session.attached,
            onTap: () => setState(() => _isExpanded = !_isExpanded),
            trailing: Icon(
              _isExpanded ? Icons.expand_less : Icons.expand_more,
              color: colorScheme.onSurface.withValues(alpha: 0.6),
            ),
          ),
        ),
        if (_isExpanded)
          ...widget.session.windows.map((window) {
            return _WindowTile(
              sessionName: widget.session.name,
              window: window,
              isLastWindow: widget.session.windows.length == 1,
              selectedPaneId: widget.selectedPaneId,
              onPaneSelected: widget.onPaneSelected,
              onWindowClose: widget.onWindowClose,
            );
          }),
      ],
    );
  }
}

/// ウィンドウタイル（展開状態を管理して遅延生成）
class _WindowTile extends StatefulWidget {
  final String sessionName;
  final TmuxWindow window;
  final bool isLastWindow;
  final String? selectedPaneId;
  final void Function(String paneId)? onPaneSelected;
  final void Function(String sessionName, int windowIndex, String windowName, bool isLastWindow)? onWindowClose;

  const _WindowTile({
    required this.sessionName,
    required this.window,
    required this.isLastWindow,
    this.selectedPaneId,
    this.onPaneSelected,
    this.onWindowClose,
  });

  @override
  State<_WindowTile> createState() => _WindowTileState();
}

class _WindowTileState extends State<_WindowTile> {
  late bool _isExpanded;

  @override
  void initState() {
    super.initState();
    _isExpanded = widget.window.active;
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(left: 16),
      child: Column(
        children: [
          TmuxWindowTile(
            window: widget.window,
            isActive: widget.window.active,
            onTap: () => setState(() => _isExpanded = !_isExpanded),
            onClose: widget.onWindowClose != null
                ? () => widget.onWindowClose?.call(
                      widget.sessionName,
                      widget.window.index,
                      widget.window.name,
                      widget.isLastWindow,
                    )
                : null,
          ),
          if (_isExpanded)
            ...widget.window.panes.map((pane) => _buildPaneNode(context, pane)),
        ],
      ),
    );
  }

  Widget _buildPaneNode(BuildContext context, TmuxPane pane) {
    final colorScheme = Theme.of(context).colorScheme;
    final isSelected = pane.id == widget.selectedPaneId;

    return Padding(
      padding: const EdgeInsets.only(left: 32),
      child: ActiveListTile(
        isActive: isSelected,
        showLeftBar: false,
        leading: Icon(
          Icons.terminal,
          color: pane.active ? colorScheme.tertiary : colorScheme.onSurface.withValues(alpha: 0.6),
        ),
        title: 'Pane ${pane.index}',
        subtitle: '${pane.width}x${pane.height}',
        onTap: () => widget.onPaneSelected?.call(pane.id),
      ),
    );
  }
}
