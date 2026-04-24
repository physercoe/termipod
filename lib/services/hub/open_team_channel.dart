import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/hub_provider.dart';
import '../../screens/team/team_channel_screen.dart';

/// Opens the team-wide `#hub-meta` channel — the canonical surface for
/// cross-project steward direction (blueprint §6.9, ia-redesign §6.7).
/// Shared by the Me quick-CTA, the Activity pinned ingress, and the Hub
/// AppBar steward icon so all three routes go through one lookup.
Future<void> openHubMetaChannel(BuildContext context, WidgetRef ref) async {
  final client = ref.read(hubProvider.notifier).client;
  if (client == null) return;
  try {
    final channels = await client.listTeamChannels();
    final meta = channels.firstWhere(
      (c) => c['name'] == 'hub-meta',
      orElse: () => const {},
    );
    if (meta.isEmpty) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('#hub-meta channel not found')),
        );
      }
      return;
    }
    if (context.mounted) {
      await Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => TeamChannelScreen(
          channelId: (meta['id'] ?? '').toString(),
          channelName: (meta['name'] ?? '').toString(),
        ),
      ));
    }
  } catch (e) {
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Open #hub-meta failed: $e')),
      );
    }
  }
}
