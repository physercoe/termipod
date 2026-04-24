import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../theme/design_colors.dart';
import '../team/team_channel_screen.dart';

/// Bottom sheet that renders a full hub event row surfaced by search.
///
/// The search list only shows title + timestamp; this sheet pops open the
/// entire payload — every text part rendered as a block, non-text parts
/// dumped as JSON, plus the event envelope (type, from, channel, ts).
/// When the event's channel matches a known team channel we surface an
/// "Open channel" shortcut so the user can jump to its live feed.
class SearchEventSheet extends ConsumerWidget {
  final Map<String, dynamic> event;

  /// channel_id → channel name lookup for team channels. Used to surface
  /// the "Open channel" action. Project channels aren't in this map and
  /// the action is hidden for them (opening a project channel requires
  /// routing through project detail, which we defer).
  final Map<String, String> teamChannels;

  const SearchEventSheet({
    super.key,
    required this.event,
    required this.teamChannels,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final type = (event['type'] ?? '').toString();
    final fromId = (event['from_id'] ?? '').toString();
    final channelId = (event['channel_id'] ?? '').toString();
    final ts = (event['received_ts'] ?? '').toString();
    final channelName = teamChannels[channelId];

    return DraggableScrollableSheet(
      initialChildSize: 0.7,
      minChildSize: 0.4,
      maxChildSize: 0.95,
      expand: false,
      builder: (_, scroll) => Container(
        decoration: const BoxDecoration(
          color: DesignColors.surfaceDark,
          borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
        ),
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
        child: ListView(
          controller: scroll,
          children: [
            Center(
              child: Container(
                width: 36,
                height: 4,
                margin: const EdgeInsets.only(bottom: 12),
                decoration: BoxDecoration(
                  color: DesignColors.borderDark,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            Text(
              type.isEmpty ? '(event)' : type,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 16,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 8),
            if (fromId.isNotEmpty) _metaRow('from', fromId),
            if (channelId.isNotEmpty)
              _metaRow(
                'channel',
                channelName == null
                    ? channelId
                    : '#$channelName · $channelId',
              ),
            if (ts.isNotEmpty) _metaRow('received', ts),
            const SizedBox(height: 14),
            _sectionLabel('Parts'),
            ..._renderParts(event['parts']),
            if (channelName != null) ...[
              const SizedBox(height: 16),
              FilledButton.icon(
                onPressed: () {
                  Navigator.of(context).pop();
                  Navigator.of(context).push(MaterialPageRoute(
                    builder: (_) => TeamChannelScreen(
                      channelId: channelId,
                      channelName: channelName,
                    ),
                  ));
                },
                icon: const Icon(Icons.forum_outlined, size: 18),
                label: Text('Open #$channelName'),
              ),
            ],
          ],
        ),
      ),
    );
  }

  List<Widget> _renderParts(dynamic raw) {
    if (raw is! List || raw.isEmpty) {
      return [
        Text(
          '(no parts)',
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            color: DesignColors.textMuted,
          ),
        ),
      ];
    }
    final widgets = <Widget>[];
    for (final raw0 in raw) {
      if (raw0 is! Map) continue;
      final part = raw0.cast<String, dynamic>();
      final kind = (part['kind'] ?? '').toString();
      if (kind == 'text' && part['text'] is String) {
        widgets.add(
          Container(
            margin: const EdgeInsets.only(bottom: 8),
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: Colors.black.withValues(alpha: 0.25),
              borderRadius: BorderRadius.circular(6),
              border: Border.all(color: DesignColors.borderDark),
            ),
            child: SelectableText(
              (part['text'] as String),
              style: GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.4),
            ),
          ),
        );
      } else {
        widgets.add(
          Container(
            margin: const EdgeInsets.only(bottom: 8),
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: Colors.black.withValues(alpha: 0.25),
              borderRadius: BorderRadius.circular(6),
              border: Border.all(color: DesignColors.borderDark),
            ),
            child: SelectableText(
              const JsonEncoder.withIndent('  ').convert(part),
              style: GoogleFonts.jetBrainsMono(fontSize: 11, height: 1.4),
            ),
          ),
        );
      }
    }
    return widgets;
  }

  Widget _metaRow(String k, String v) => Padding(
        padding: const EdgeInsets.only(bottom: 4),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 80,
              child: Text(
                k,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
            Expanded(
              child: SelectableText(
                v,
                style: GoogleFonts.jetBrainsMono(fontSize: 11),
              ),
            ),
          ],
        ),
      );

  Widget _sectionLabel(String label) => Padding(
        padding: const EdgeInsets.only(bottom: 6),
        child: Text(
          label,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            color: DesignColors.textMuted,
            letterSpacing: 0.5,
          ),
        ),
      );
}
