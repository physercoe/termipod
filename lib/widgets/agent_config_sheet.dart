import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../providers/sessions_provider.dart';
import '../screens/sessions/sessions_screen.dart' show SessionChatScreen;
import '../theme/design_colors.dart';

/// Bottom sheet that surfaces an agent's full configuration —
/// persona kind, derived role (from spawn_spec `default_role:`),
/// driving mode, parent, host, status, and the raw spawn_spec_yaml.
///
/// Distinct from [SessionInitChip], which surfaces what the *engine*
/// reported via `session.init` (model, tools, mcp_servers, slash
/// commands). That answers "what is this engine instance capable of";
/// this sheet answers "what was this agent spawned AS, and with what
/// config." Two different lenses on the same row.
///
/// Open via [showAgentConfigSheet]. The sheet fetches via
/// `client.getAgent(agentId)` on open and renders a loading spinner
/// until the data arrives. Single round-trip — cached for the sheet's
/// lifetime.
Future<void> showAgentConfigSheet(
  BuildContext context, {
  required String agentId,
}) {
  return showModalBottomSheet(
    context: context,
    isScrollControlled: true,
    builder: (_) => _AgentConfigSheet(agentId: agentId),
  );
}

class _AgentConfigSheet extends ConsumerStatefulWidget {
  final String agentId;
  const _AgentConfigSheet({required this.agentId});

  @override
  ConsumerState<_AgentConfigSheet> createState() => _AgentConfigSheetState();
}

class _AgentConfigSheetState extends ConsumerState<_AgentConfigSheet> {
  Map<String, dynamic>? _agent;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final client = ref.read(hubProvider.notifier).client;
      if (client == null) {
        if (mounted) setState(() => _error = 'Hub not configured');
        return;
      }
      final res = await client.getAgent(widget.agentId);
      if (!mounted) return;
      setState(() => _agent = res);
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    }
  }

  /// Pull `default_role:` out of the spawn_spec_yaml so the sheet can
  /// surface the human-meaningful persona role ("team.coordinator")
  /// AND the hub-enforced operation-scope role ("steward" / "worker").
  /// Single-line top-level key, same parser as the hub's
  /// parseDefaultRole — we keep them in lockstep so the user sees the
  /// same field the role-stamping code reads.
  String? _parseDefaultRole(String yaml) {
    for (final line in yaml.split('\n')) {
      if (line.isEmpty || line.startsWith(' ') || line.startsWith('\t') ||
          line.startsWith('#')) {
        continue;
      }
      const key = 'default_role:';
      if (!line.startsWith(key)) continue;
      var val = line.substring(key.length).trim();
      final hash = val.indexOf('#');
      if (hash >= 0) val = val.substring(0, hash).trim();
      return val.replaceAll('"', '').replaceAll("'", '');
    }
    return null;
  }

  /// Same logic as `hub/internal/server/mcp_authority_roles.go`'s
  /// `RoleForSpec`: kind-prefix wins; spec `default_role: team.*`
  /// escalates to steward; anything else is worker.
  String _operationRole(String kind, String? defaultRole) {
    if (kind.startsWith('steward.')) return 'steward';
    if (defaultRole != null && defaultRole.startsWith('team.')) {
      return 'steward';
    }
    return 'worker';
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;

    return SafeArea(
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxHeight: MediaQuery.of(context).size.height * 0.85,
        ),
        child: _error != null
            ? Padding(
                padding: const EdgeInsets.all(24),
                child: Text(
                  'Failed to load agent: $_error',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 12,
                    color: DesignColors.error,
                  ),
                ),
              )
            : _agent == null
                ? const Padding(
                    padding: EdgeInsets.all(32),
                    child: Center(child: CircularProgressIndicator()),
                  )
                : SingleChildScrollView(
                    child: _buildContent(context, muted, isDark),
                  ),
      ),
    );
  }

  Widget _buildContent(BuildContext context, Color muted, bool isDark) {
    final a = _agent!;
    final handle = (a['handle'] ?? '').toString();
    final kind = (a['kind'] ?? '').toString();
    final mode = (a['mode'] ?? a['driving_mode'] ?? '').toString();
    final parent = (a['parent_agent_id'] ?? '').toString();
    final host = (a['host_id'] ?? '').toString();
    final status = (a['status'] ?? '').toString();
    final pauseState = (a['pause_state'] ?? '').toString();
    final worktree = (a['worktree_path'] ?? '').toString();
    final createdAt = (a['created_at'] ?? '').toString();
    final spawnSpec = (a['spawn_spec_yaml'] ?? '').toString();
    final defaultRole = spawnSpec.isNotEmpty
        ? _parseDefaultRole(spawnSpec)
        : null;
    final opRole = _operationRole(kind, defaultRole);

    final children = <Widget>[];

    void section(String title, Widget body) {
      children.add(Padding(
        padding: const EdgeInsets.fromLTRB(16, 14, 16, 4),
        child: Text(
          title,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            fontWeight: FontWeight.w700,
            color: muted,
            letterSpacing: 0.5,
          ),
        ),
      ));
      children.add(Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
        child: body,
      ));
    }

    // PERSONA block — answers "what is this agent SUPPOSED to be."
    // Lead with role because that's the field the user just got bitten
    // by (steward.research spawned with role:worker → couldn't spawn
    // children). Role-mismatch is the one thing a user opens this
    // sheet to diagnose.
    section(
      'PERSONA',
      Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _kvLine(context, 'op role', opRole,
              valueColor:
                  opRole == 'steward' ? DesignColors.primary : null),
          if (defaultRole != null && defaultRole.isNotEmpty)
            _kvLine(context, 'default_role', defaultRole),
          if (handle.isNotEmpty) _kvLine(context, 'handle', handle),
          if (kind.isNotEmpty) _kvLine(context, 'kind', kind),
          if (mode.isNotEmpty) _kvLine(context, 'mode', mode),
        ],
      ),
    );

    // RUNTIME block — answers "where + how is this running."
    final hasRuntime = parent.isNotEmpty ||
        host.isNotEmpty ||
        status.isNotEmpty ||
        pauseState.isNotEmpty ||
        worktree.isNotEmpty ||
        createdAt.isNotEmpty;
    if (hasRuntime) {
      section(
        'RUNTIME',
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (status.isNotEmpty) _kvLine(context, 'status', status),
            if (pauseState.isNotEmpty)
              _kvLine(context, 'pause_state', pauseState),
            if (host.isNotEmpty) _kvLine(context, 'host', host),
            if (parent.isNotEmpty)
              _kvLine(context, 'parent', parent, mono: true),
            if (worktree.isNotEmpty)
              _kvLine(context, 'worktree', worktree, mono: true),
            if (createdAt.isNotEmpty)
              _kvLine(context, 'created', createdAt, mono: true),
          ],
        ),
      );
    }

    // SPAWN SPEC block — the full rendered template + overlay merge.
    // Authoritative for "what does this agent actually run." Long, so
    // we wrap in a SelectableText with copy affordance. Empty when
    // the agent predates agent_spawns capture (e.g. seeded demo data).
    // ADR-025 W11 — steward-mediated reconfiguration CTA. The sheet
    // is read-only by design (config edits flow through the
    // project's steward as the authority anchor). For project-bound
    // agents, the CTA jumps into the steward's session so the user
    // can dictate the reconfiguration request as a normal chat turn.
    // For team-scoped agents (no project_id), the CTA is absent
    // since there's no project steward to delegate to.
    final projectID = (a['project_id'] ?? '').toString();
    if (projectID.isNotEmpty) {
      children.add(Padding(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
        child: FilledButton.tonalIcon(
          onPressed: () => _askStewardToReconfigure(
            context, projectID, handle),
          icon: const Icon(Icons.forum_outlined, size: 16),
          label: const Text('Ask steward to reconfigure'),
        ),
      ));
    }

    if (spawnSpec.isNotEmpty) {
      children.add(Padding(
        padding: const EdgeInsets.fromLTRB(16, 14, 16, 4),
        child: Row(
          children: [
            Text(
              'SPAWN SPEC',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                fontWeight: FontWeight.w700,
                color: muted,
                letterSpacing: 0.5,
              ),
            ),
            const Spacer(),
            IconButton(
              tooltip: 'Copy YAML',
              icon: const Icon(Icons.copy_outlined, size: 16),
              onPressed: () async {
                await Clipboard.setData(ClipboardData(text: spawnSpec));
                if (!mounted) return;
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('Copied spawn_spec_yaml')),
                );
              },
              padding: EdgeInsets.zero,
              visualDensity: VisualDensity.compact,
              constraints:
                  const BoxConstraints(minWidth: 28, minHeight: 28),
            ),
          ],
        ),
      ));
      children.add(Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
        child: Container(
          width: double.infinity,
          padding: const EdgeInsets.all(10),
          decoration: BoxDecoration(
            color: (isDark ? Colors.white : Colors.black)
                .withValues(alpha: 0.04),
            borderRadius: BorderRadius.circular(6),
          ),
          child: SelectableText(
            spawnSpec,
            style: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: isDark
                  ? DesignColors.textPrimary
                  : DesignColors.textPrimaryLight,
            ),
          ),
        ),
      ));
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        const SizedBox(height: 6),
        Center(
          child: Container(
            width: 36,
            height: 4,
            margin: const EdgeInsets.only(bottom: 8),
            decoration: BoxDecoration(
              color: muted.withValues(alpha: 0.4),
              borderRadius: BorderRadius.circular(2),
            ),
          ),
        ),
        ...children,
      ],
    );
  }

  /// ADR-025 W11: jump into the project steward's session so the
  /// director can phrase the reconfiguration request as a normal
  /// chat turn. The steward owns the authority to spawn a new agent
  /// (the canonical "reconfigure" path = terminate + respawn with
  /// new spec) per D3/W9.
  Future<void> _askStewardToReconfigure(
    BuildContext context,
    String projectID,
    String agentHandle,
  ) async {
    final hub = ref.read(hubProvider).value;
    Map<String, dynamic>? stewardAgent;
    for (final ag in hub?.agents ?? const <Map<String, dynamic>>[]) {
      if ((ag['project_id'] ?? '').toString() != projectID) continue;
      if (!((ag['kind'] ?? '').toString().startsWith('steward.'))) continue;
      final status = (ag['status'] ?? '').toString();
      if (status == 'terminated' ||
          status == 'crashed' ||
          status == 'failed') {
        continue;
      }
      if ((ag['archived_at'] ?? '').toString().isNotEmpty) continue;
      stewardAgent = ag;
      break;
    }
    if (stewardAgent == null) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            'No live steward for this project yet — open the project '
            'Agents tab and spawn one first.',
          ),
        ),
      );
      return;
    }
    final stewardID = (stewardAgent['id'] ?? '').toString();
    final sessions = ref.read(sessionsProvider).value;
    final allSessions = <Map<String, dynamic>>[
      ...?sessions?.active,
      ...?sessions?.previous,
    ];
    Map<String, dynamic>? stewardSession;
    for (final s in allSessions) {
      if ((s['current_agent_id'] ?? '').toString() == stewardID) {
        stewardSession = s;
        break;
      }
    }
    if (stewardSession == null) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Steward has no live session yet — try again shortly.'),
        ),
      );
      return;
    }
    if (!context.mounted) return;
    // Close this read-only sheet first so the steward session lands
    // as the foreground surface.
    Navigator.of(context).pop();
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => SessionChatScreen(
          sessionId: (stewardSession?['id'] ?? '').toString(),
          agentId: stewardID,
          title: (stewardSession?['title'] ?? 'Project steward').toString(),
        ),
      ),
    );
  }

  Widget _kvLine(
    BuildContext ctx,
    String k,
    String v, {
    Color? valueColor,
    bool mono = false,
  }) {
    final isDark = Theme.of(ctx).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final valueStyle = mono
        ? GoogleFonts.jetBrainsMono(fontSize: 11)
        : GoogleFonts.jetBrainsMono(fontSize: 12);
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
      child: RichText(
        text: TextSpan(
          style: valueStyle.copyWith(
            color: isDark
                ? DesignColors.textPrimary
                : DesignColors.textPrimaryLight,
          ),
          children: [
            TextSpan(text: '$k: ', style: TextStyle(color: muted)),
            TextSpan(
              text: v,
              style: valueColor == null ? null : TextStyle(color: valueColor),
            ),
          ],
        ),
      ),
    );
  }
}
