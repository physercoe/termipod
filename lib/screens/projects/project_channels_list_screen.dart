import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../../widgets/hub_offline_banner.dart';
import 'project_channel_create_sheet.dart';
import 'project_channel_screen.dart';

/// Standalone project-channel list. W2 demoted "Channel" out of the
/// pill bar (D10 — Activity is the primary feed); the AppBar Discussion
/// icon now pushes this screen instead of embedding the list inline.
/// Behavior is unchanged from the prior `_ChannelsView`: read-through
/// cache + create FAB + tap-to-channel routing.
class ProjectChannelsListScreen extends ConsumerStatefulWidget {
  final String projectId;
  final String projectName;
  const ProjectChannelsListScreen({
    super.key,
    required this.projectId,
    required this.projectName,
  });

  @override
  ConsumerState<ProjectChannelsListScreen> createState() =>
      _ProjectChannelsListScreenState();
}

class _ProjectChannelsListScreenState
    extends ConsumerState<ProjectChannelsListScreen> {
  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _channels = const [];
  DateTime? _staleSince;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final cached = await client.listChannelsCached(widget.projectId);
      if (!mounted) return;
      setState(() {
        _channels = cached.body;
        _staleSince = cached.staleSince;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  Future<void> _create() async {
    final created = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      builder: (_) =>
          ProjectChannelCreateSheet(projectId: widget.projectId),
    );
    if (created == null || !mounted) return;
    setState(() => _channels = [..._channels, created]);
  }

  void _open(Map<String, dynamic> row) {
    final id = (row['id'] ?? '').toString();
    final name = (row['name'] ?? id).toString();
    if (id.isEmpty) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ProjectChannelScreen(
          projectId: widget.projectId,
          channelId: id,
          channelName: name,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final title = widget.projectName.isEmpty
        ? 'Discussion'
        : 'Discussion · ${widget.projectName}';
    return Scaffold(
      appBar: AppBar(
        title: Text(
          title,
          overflow: TextOverflow.ellipsis,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 16, fontWeight: FontWeight.w700),
        ),
      ),
      body: _body(),
      floatingActionButton: FloatingActionButton.extended(
        heroTag: 'project-channel-fab-${widget.projectId}',
        onPressed: _create,
        icon: const Icon(Icons.add),
        label: const Text('Channel'),
      ),
    );
  }

  Widget _body() {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _error!,
            style: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: DesignColors.error,
            ),
          ),
        ),
      );
    }
    final list = _channels.isEmpty
        ? Center(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Text(
                'No channels yet — tap + to create',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  color: DesignColors.textMuted,
                ),
              ),
            ),
          )
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 96),
              itemCount: _channels.length,
              separatorBuilder: (_, __) => const SizedBox(height: 8),
              itemBuilder: (_, i) => _ChannelTile(
                row: _channels[i],
                onTap: () => _open(_channels[i]),
              ),
            ),
          );
    return Column(
      children: [
        HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
        Expanded(child: list),
      ],
    );
  }
}

class _ChannelTile extends StatelessWidget {
  final Map<String, dynamic> row;
  final VoidCallback onTap;
  const _ChannelTile({required this.row, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final name = (row['name'] ?? '').toString();
    final id = (row['id'] ?? '').toString();
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(10),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: isDark
              ? DesignColors.surfaceDark
              : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
        child: Row(
          children: [
            const Icon(Icons.tag, size: 18, color: DesignColors.primary),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    name.isEmpty ? '(unnamed)' : name,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  if (id.isNotEmpty)
                    Text(
                      id,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 10,
                        color: isDark
                            ? DesignColors.textMuted
                            : DesignColors.textMutedLight,
                      ),
                    ),
                ],
              ),
            ),
            const Icon(Icons.chevron_right, size: 18),
          ],
        ),
      ),
    );
  }
}
