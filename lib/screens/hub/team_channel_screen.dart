import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Single team-scope channel view. Lists existing events via REST, then
/// subscribes to the SSE stream for live updates. A bottom composer posts
/// new `message` events with a single `text` part.
///
/// Reused by the Steward chip (opens `#hub-meta`) and by Team → Channels.
class TeamChannelScreen extends ConsumerStatefulWidget {
  final String channelId;
  final String channelName;
  const TeamChannelScreen({
    super.key,
    required this.channelId,
    required this.channelName,
  });

  @override
  ConsumerState<TeamChannelScreen> createState() =>
      _TeamChannelScreenState();
}

class _TeamChannelScreenState extends ConsumerState<TeamChannelScreen> {
  final _composer = TextEditingController();
  final _scroll = ScrollController();
  final List<Map<String, dynamic>> _events = [];
  StreamSubscription<Map<String, dynamic>>? _sub;
  bool _loading = true;
  bool _sending = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _bootstrap();
  }

  @override
  void dispose() {
    _sub?.cancel();
    _composer.dispose();
    _scroll.dispose();
    super.dispose();
  }

  Future<void> _bootstrap() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      // Backfill — hub returns newest-first; flip so the UI reads top→bottom.
      final rows = await client.listTeamChannelEvents(
        widget.channelId,
        limit: 50,
      );
      rows.sort((a, b) {
        final at = (a['received_ts'] ?? a['ts'] ?? '').toString();
        final bt = (b['received_ts'] ?? b['ts'] ?? '').toString();
        return at.compareTo(bt);
      });
      if (!mounted) return;
      setState(() {
        _events
          ..clear()
          ..addAll(rows);
        _loading = false;
      });
      _jumpToBottom();
      _sub = client.streamTeamEvents(widget.channelId).listen(
        (evt) {
          if (!mounted) return;
          setState(() => _events.add(evt));
          _jumpToBottom();
        },
        onError: (e) {
          if (!mounted) return;
          setState(() => _error = '$e');
        },
        cancelOnError: false,
      );
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  void _jumpToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scroll.hasClients) return;
      _scroll.jumpTo(_scroll.position.maxScrollExtent);
    });
  }

  Future<void> _send() async {
    final text = _composer.text.trim();
    if (text.isEmpty || _sending) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _sending = true);
    try {
      await client.postTeamChannelEvent(
        widget.channelId,
        type: 'message',
        parts: [
          {'kind': 'text', 'text': text},
        ],
      );
      _composer.clear();
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
    return Scaffold(
      appBar: AppBar(
        title: Text(
          '#${widget.channelName}',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 16, fontWeight: FontWeight.w700),
        ),
      ),
      body: SafeArea(
        child: Column(
          children: [
            if (_error != null)
              Container(
                width: double.infinity,
                color: DesignColors.error.withValues(alpha: 0.1),
                padding:
                    const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                child: Text(_error!,
                    style: GoogleFonts.jetBrainsMono(
                        fontSize: 11, color: DesignColors.error)),
              ),
            Expanded(
              child: _loading
                  ? const Center(child: CircularProgressIndicator())
                  : _events.isEmpty
                      ? _EmptyChannelView(name: widget.channelName)
                      : ListView.builder(
                          controller: _scroll,
                          padding: const EdgeInsets.all(12),
                          itemCount: _events.length,
                          itemBuilder: (_, i) => _EventBubble(evt: _events[i]),
                        ),
            ),
            _Composer(
              controller: _composer,
              sending: _sending,
              onSend: _send,
            ),
          ],
        ),
      ),
    );
  }
}

class _EmptyChannelView extends StatelessWidget {
  final String name;
  const _EmptyChannelView({required this.name});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.forum_outlined,
                size: 48,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight),
            const SizedBox(height: 12),
            Text(
              'Nothing in #$name yet',
              textAlign: TextAlign.center,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _EventBubble extends StatelessWidget {
  final Map<String, dynamic> evt;
  const _EventBubble({required this.evt});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final from = (evt['from_id'] ?? '').toString();
    final ts = (evt['ts'] ?? evt['received_ts'] ?? '').toString();
    final preview = _previewFromParts(
        (evt['parts'] as List?) ?? const <dynamic>[]);

    return Container(
      margin: const EdgeInsets.only(bottom: 10),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color:
            isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: isDark
              ? DesignColors.borderDark
              : DesignColors.borderLight,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                from.isEmpty ? '(system)' : from,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  fontWeight: FontWeight.w700,
                  color: DesignColors.primary,
                ),
              ),
              const Spacer(),
              Text(
                _shortTs(ts),
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 9,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
            ],
          ),
          const SizedBox(height: 4),
          Text(preview,
              style:
                  GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.35)),
        ],
      ),
    );
  }

  String _previewFromParts(List<dynamic> parts) {
    for (final raw in parts) {
      if (raw is! Map) continue;
      if (raw['kind'] == 'text' && raw['text'] is String) {
        final t = (raw['text'] as String).trim();
        if (t.isNotEmpty) return t;
      }
    }
    return '(empty)';
  }

  String _shortTs(String raw) {
    if (raw.isEmpty) return '';
    final t = DateTime.tryParse(raw);
    if (t == null) return raw;
    final diff = DateTime.now().difference(t);
    if (diff.inMinutes < 1) return 'now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m';
    if (diff.inHours < 24) return '${diff.inHours}h';
    if (diff.inDays < 7) return '${diff.inDays}d';
    return '${(diff.inDays / 7).floor()}w';
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
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Container(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
      decoration: BoxDecoration(
        color: isDark
            ? DesignColors.backgroundDark
            : DesignColors.backgroundLight,
        border: Border(
          top: BorderSide(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Expanded(
            child: TextField(
              controller: controller,
              minLines: 1,
              maxLines: 4,
              decoration: const InputDecoration(
                hintText: 'Message…',
                border: OutlineInputBorder(),
                isDense: true,
              ),
              textInputAction: TextInputAction.newline,
            ),
          ),
          const SizedBox(width: 8),
          IconButton(
            onPressed: sending ? null : onSend,
            icon: sending
                ? const SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.send),
          ),
        ],
      ),
    );
  }
}
