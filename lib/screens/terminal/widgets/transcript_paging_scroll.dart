import 'package:flutter/material.dart';

/// At a captured screen's edge, a deliberate pull pages application-owned
/// history. Local scrolling within a tall pane still works normally. Only
/// direct, single-finger vertical drags count; flings, layout corrections,
/// horizontal scrolls, and pinch/pane-switch gestures must not send keys.
class TranscriptPagingScroll extends StatefulWidget {
  final Widget child;
  final void Function(bool older)? onPage;

  const TranscriptPagingScroll({super.key, required this.child, this.onPage});

  @override
  State<TranscriptPagingScroll> createState() => _TranscriptPagingScrollState();
}

class _TranscriptPagingScrollState extends State<TranscriptPagingScroll> {
  final _pointers = <int>{};
  double _pull = 0;
  bool _sent = false;
  bool _multiTouch = false;

  @override
  void didUpdateWidget(TranscriptPagingScroll oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.onPage == null) _pull = 0;
  }

  bool _onScroll(ScrollNotification notification) {
    if (notification.metrics.axis != Axis.vertical) return false;
    if (notification is ScrollStartNotification) {
      _pull = 0;
      _sent = false;
    } else if (notification is OverscrollNotification &&
        notification.dragDetails != null &&
        _pointers.length == 1 &&
        !_multiTouch &&
        !_sent &&
        widget.onPage != null) {
      final delta = notification.overscroll;
      if (_pull.sign != delta.sign) _pull = 0;
      _pull += delta;
      if (_pull.abs() >= 48) {
        _sent = true;
        widget.onPage!(_pull < 0);
      }
    } else if (notification is ScrollEndNotification) {
      _pull = 0;
    }
    return false;
  }

  @override
  Widget build(BuildContext context) => Listener(
    onPointerDown: (event) {
      if (_pointers.isEmpty) _multiTouch = false;
      _pointers.add(event.pointer);
      if (_pointers.length > 1) _multiTouch = true;
    },
    onPointerUp: (event) => _pointers.remove(event.pointer),
    onPointerCancel: (event) => _pointers.remove(event.pointer),
    child: NotificationListener<ScrollNotification>(
      onNotification: _onScroll,
      child: widget.child,
    ),
  );
}
