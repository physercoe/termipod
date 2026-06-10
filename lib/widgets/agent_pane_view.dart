// AgentPaneView — the Pane-capture + Spawn-spec body for an agent.
//
// Extracted from the project-agent sheet (P2 — docs/plans/agent-
// transcript-debug-and-header-parity.md) so the SAME body renders behind
// the shared SessionHeader's `View ▾` switcher on both the project sheet
// and SessionChatScreen — one copy, no fork. Self-contained: it derives
// pane attachment from cached hub state and fetches the spawn spec
// itself, so a host only passes the agent id.
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../theme/design_colors.dart';
import '../theme/tokens.dart';

class AgentPaneView extends ConsumerStatefulWidget {
  final String agentId;
  const AgentPaneView({super.key, required this.agentId});

  @override
  ConsumerState<AgentPaneView> createState() => _AgentPaneViewState();
}

class _AgentPaneViewState extends ConsumerState<AgentPaneView> {
  bool _busy = false;
  String? _error;
  String? _paneText;
  String? _paneCapturedAt;
  String _specYaml = '';
  bool _hasPane = false;
  // False until the hub-state lookup + spec fetch resolve, so the
  // "no pane" copy doesn't flash before we know.
  bool _resolved = false;

  @override
  void initState() {
    super.initState();
    _resolve();
  }

  Future<void> _resolve() async {
    // Pane attachment is a property of the cached agent row.
    final hub = ref.read(hubProvider).value;
    var hasPane = false;
    if (hub != null) {
      for (final a in hub.agents) {
        if ((a['id'] ?? '').toString() == widget.agentId) {
          hasPane = (a['pane_id']?.toString() ?? '').isNotEmpty;
          break;
        }
      }
    }
    if (mounted) {
      setState(() {
        _hasPane = hasPane;
        _resolved = true;
      });
    }
    // Spawn spec rides on the full agent row (cached).
    final client = ref.read(hubProvider.notifier).client;
    if (client != null) {
      try {
        final out = (await client.getAgentCached(widget.agentId)).body;
        if (mounted) {
          setState(() => _specYaml = (out['spawn_spec_yaml'] ?? '').toString());
        }
      } catch (_) {
        // Spec-fetch failure is non-fatal — the pane still works without it.
      }
    }
    if (hasPane) _loadPane();
  }

  Future<void> _loadPane({bool refresh = false}) async {
    if (!_hasPane) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final out = await client.getAgentPane(widget.agentId, refresh: refresh);
      if (!mounted) return;
      setState(() {
        _paneText = out['text']?.toString();
        _paneCapturedAt = out['captured_at']?.toString();
      });
    } catch (e) {
      if (mounted) setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
      children: [
        if (_error != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text(_error!,
                style: const TextStyle(color: DesignColors.error)),
          ),
        _SectionHeader(
          title: 'Pane capture',
          trailing: _hasPane
              ? TextButton.icon(
                  onPressed: _busy ? null : () => _loadPane(refresh: true),
                  icon: const Icon(Icons.refresh, size: 18),
                  label: const Text('Refresh'),
                )
              : null,
        ),
        if (!_hasPane)
          Text(_resolved ? 'No pane attached yet.' : 'Loading…',
              style: TextStyle(color: mutedColor))
        else
          Container(
            padding: const EdgeInsets.all(Spacing.s8),
            decoration: BoxDecoration(
              color: isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight,
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
                Text(
                  _paneCapturedAt == null
                      ? '(no capture yet)'
                      : 'captured ${_shortTs(_paneCapturedAt!)} ago',
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: FontSizes.label, color: mutedColor),
                ),
                const SizedBox(height: 6),
                SelectableText(
                  _paneText == null || _paneText!.isEmpty
                      ? '(empty — hit Refresh to request a fresh capture)'
                      : _paneText!,
                  style: GoogleFonts.jetBrainsMono(fontSize: 11),
                ),
              ],
            ),
          ),
        if (_specYaml.isNotEmpty) ...[
          const SizedBox(height: 16),
          const _SectionHeader(title: 'Spawn spec'),
          Container(
            padding: const EdgeInsets.all(Spacing.s8),
            decoration: BoxDecoration(
              color: isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight,
              borderRadius: BorderRadius.circular(8),
              border: Border.all(
                color: isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight,
              ),
            ),
            child: SelectableText(
              _specYaml,
              style: GoogleFonts.jetBrainsMono(fontSize: 11),
            ),
          ),
        ],
      ],
    );
  }
}

/// Local section header (title + optional trailing action). Kept private
/// to this view so the extraction is self-contained.
class _SectionHeader extends StatelessWidget {
  final String title;
  final Widget? trailing;
  const _SectionHeader({required this.title, this.trailing});
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: Spacing.s8, top: 4),
      child: Row(
        children: [
          Text(title,
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 13, fontWeight: FontWeight.w700)),
          const Spacer(),
          if (trailing != null) trailing!,
        ],
      ),
    );
  }
}

String _shortTs(String iso) {
  if (iso.isEmpty) return '';
  final t = DateTime.tryParse(iso);
  if (t == null) return iso;
  final diff = DateTime.now().difference(t.toLocal());
  if (diff.inSeconds < 60) return '${diff.inSeconds}s';
  if (diff.inMinutes < 60) return '${diff.inMinutes}m';
  if (diff.inHours < 24) return '${diff.inHours}h';
  return '${diff.inDays}d';
}
