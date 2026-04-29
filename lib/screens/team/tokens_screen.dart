import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/hub_client.dart';
import '../../theme/design_colors.dart';

/// Owner-only token manager. Issue new tokens for human team members
/// or for extra hosts/agents, list existing ones, and revoke.
///
/// Plaintext is returned exactly once at issue time — the screen surfaces
/// it in a copy-to-clipboard sheet and drops it after the user dismisses.
class TokensScreen extends ConsumerStatefulWidget {
  const TokensScreen({super.key});

  @override
  ConsumerState<TokensScreen> createState() => _TokensScreenState();
}

class _TokensScreenState extends ConsumerState<TokensScreen> {
  List<Map<String, dynamic>> _rows = const [];
  bool _loading = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'Hub not configured.';
      });
      return;
    }
    try {
      final rows = await client.listTokens();
      if (!mounted) return;
      setState(() {
        // Hide kind='agent' rows: those are machine-issued at spawn
        // time and machine-revoked at terminate time. Listing them
        // here floods the screen with one row per pause/resume cycle
        // and invites the operator to revoke a live agent's bearer
        // (which would just look like the agent crashed).
        _rows = rows.where((r) => (r['kind'] ?? '').toString() != 'agent').toList();
        _loading = false;
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = e.status == 403
            ? 'Token management requires an owner-kind token.'
            : '${e.status}: ${e.message}';
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  Future<void> _issue() async {
    final result = await showDialog<_IssueSpec>(
      context: context,
      builder: (_) => const _IssueDialog(),
    );
    if (result == null) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final out = await client.issueToken(
        kind: result.kind,
        role: result.role,
        handle: result.handle,
      );
      final plaintext = (out['plaintext'] ?? '').toString();
      if (!mounted) return;
      await _showPlaintextSheet(
        context,
        plaintext: plaintext,
        handle: result.handle,
        kind: result.kind,
        hubUrl: client.cfg.baseUrl,
        teamId: client.cfg.teamId,
      );
      await _load();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Issue failed: $e')),
      );
    }
  }

  Future<void> _revoke(Map<String, dynamic> row) async {
    final id = (row['id'] ?? '').toString();
    final label = (row['handle'] ?? row['kind'] ?? id).toString();
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Revoke "$label"?'),
        content: const Text(
          'The holder will be signed out on their next request. This cannot be undone.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: DesignColors.error),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Revoke'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.revokeToken(id);
      await _load();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Revoke failed: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Auth',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _issue,
        icon: const Icon(Icons.add),
        label: const Text('New token'),
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_loading && _rows.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _error!,
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(color: DesignColors.error),
          ),
        ),
      );
    }
    if (_rows.isEmpty) {
      return Center(
        child: Text('No tokens issued yet.',
            style: GoogleFonts.spaceGrotesk(color: DesignColors.textMuted)),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 96),
        itemCount: _rows.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) => _TokenTile(
          row: _rows[i],
          onRevoke: () => _revoke(_rows[i]),
        ),
      ),
    );
  }
}

class _TokenTile extends StatelessWidget {
  final Map<String, dynamic> row;
  final VoidCallback onRevoke;
  const _TokenTile({required this.row, required this.onRevoke});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final kind = (row['kind'] ?? '').toString();
    final role = (row['role'] ?? '').toString();
    final handle = (row['handle'] ?? '').toString();
    final revoked = row['revoked_at'] != null;
    final createdAt = (row['created_at'] ?? '').toString();
    final title = handle.isNotEmpty ? handle : '($kind token)';
    return Opacity(
      opacity: revoked ? 0.5 : 1.0,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        decoration: BoxDecoration(
          color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
        child: Row(
          children: [
            Icon(
              revoked ? Icons.block : Icons.key,
              size: 20,
              color: revoked ? DesignColors.textMuted : DesignColors.primary,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title,
                      style: GoogleFonts.spaceGrotesk(
                          fontSize: 14, fontWeight: FontWeight.w600)),
                  const SizedBox(height: 2),
                  Text(
                    [
                      kind,
                      if (role.isNotEmpty) role,
                      if (createdAt.isNotEmpty) createdAt.substring(0, 10),
                      if (revoked) 'revoked',
                    ].join(' · '),
                    style: GoogleFonts.spaceGrotesk(
                        fontSize: 11, color: DesignColors.textMuted),
                  ),
                ],
              ),
            ),
            if (!revoked)
              IconButton(
                icon: const Icon(Icons.delete_outline, size: 20),
                color: DesignColors.error,
                onPressed: onRevoke,
              ),
          ],
        ),
      ),
    );
  }
}

class _IssueSpec {
  final String kind;
  final String role;
  final String handle;
  _IssueSpec({required this.kind, required this.role, required this.handle});
}

class _IssueDialog extends StatefulWidget {
  const _IssueDialog();

  @override
  State<_IssueDialog> createState() => _IssueDialogState();
}

class _IssueDialogState extends State<_IssueDialog> {
  String _kind = 'user';
  final _handleCtrl = TextEditingController();

  @override
  void dispose() {
    _handleCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('New token'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Kind',
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 12, color: DesignColors.textMuted)),
          const SizedBox(height: 6),
          // 'agent' kinds are machine-issued at spawn and machine-revoked
          // at terminate (handlers_agents.go:626). Issuing one by hand here
          // would create a token that's both invisible (filtered from the
          // list above) and orphan (no agent_id to revoke against), so the
          // dialog only offers human/infra kinds.
          Wrap(
            spacing: 8,
            children: ['user', 'host', 'owner'].map((k) {
              final sel = _kind == k;
              return ChoiceChip(
                label: Text(k),
                selected: sel,
                onSelected: (_) => setState(() => _kind = k),
              );
            }).toList(),
          ),
          const SizedBox(height: 14),
          TextField(
            controller: _handleCtrl,
            autofocus: true,
            decoration: InputDecoration(
              labelText: _kind == 'user'
                  ? 'Handle (e.g. alice)'
                  : 'Label (optional)',
            ),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () {
            final h = _handleCtrl.text.trim();
            // role follows kind: user→principal, others keep their name
            final role = _kind == 'user' ? 'principal' : _kind;
            Navigator.pop(
              context,
              _IssueSpec(kind: _kind, role: role, handle: h),
            );
          },
          child: const Text('Issue'),
        ),
      ],
    );
  }
}

Future<void> _showPlaintextSheet(
  BuildContext context, {
  required String plaintext,
  required String handle,
  required String kind,
  required String hubUrl,
  required String teamId,
}) async {
  // For host-kind tokens we render a ready-to-paste setup snippet
  // (Track A from docs/hub-host-setup.md) so the demo path is one
  // screen instead of "got token, now read the doc". The snippet uses
  // `tmux display-message -p '#S'` so the runner attaches to whichever
  // session the operator is already in.
  final isHost = kind == 'host';
  final hostSetup = isHost
      ? '''~/host-runner run \\
  --hub   $hubUrl \\
  --team  $teamId \\
  --token $plaintext \\
  --tmux-session "\$(tmux display-message -p '#S')"'''
      : null;

  await showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    builder: (ctx) {
      return Padding(
        padding: EdgeInsets.only(
          left: 20,
          right: 20,
          top: 20,
          bottom: MediaQuery.of(ctx).viewInsets.bottom + 20,
        ),
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Token for $handle',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 18, fontWeight: FontWeight.w700)),
              const SizedBox(height: 8),
              Text(
                'Copy this now. The hub does not store the plaintext — this is the only time it will be shown.',
                style: GoogleFonts.spaceGrotesk(
                    fontSize: 12, color: DesignColors.textMuted),
              ),
              const SizedBox(height: 16),
              Container(
                padding: const EdgeInsets.all(12),
                decoration: BoxDecoration(
                  color: DesignColors.primary.withValues(alpha: 0.08),
                  borderRadius: BorderRadius.circular(8),
                ),
                child: SelectableText(
                  plaintext,
                  style: GoogleFonts.jetBrainsMono(fontSize: 13),
                ),
              ),
              const SizedBox(height: 16),
              Row(
                children: [
                  Expanded(
                    child: FilledButton.icon(
                      icon: const Icon(Icons.copy),
                      label: const Text('Copy'),
                      onPressed: () async {
                        await Clipboard.setData(
                            ClipboardData(text: plaintext));
                        if (!ctx.mounted) return;
                        ScaffoldMessenger.of(ctx).showSnackBar(
                          const SnackBar(
                            content: Text('Token copied'),
                            duration: Duration(seconds: 2),
                          ),
                        );
                      },
                    ),
                  ),
                  const SizedBox(width: 8),
                  TextButton(
                    onPressed: () => Navigator.pop(ctx),
                    child: const Text('Done'),
                  ),
                ],
              ),
              if (hostSetup != null) ...[
                const SizedBox(height: 24),
                Text('Run on the host',
                    style: GoogleFonts.spaceGrotesk(
                        fontSize: 14, fontWeight: FontWeight.w700)),
                const SizedBox(height: 6),
                Text(
                  'Inside a tmux session on the target box (Track A — see docs/hub-host-setup.md for systemd/Track B):',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 12, color: DesignColors.textMuted),
                ),
                const SizedBox(height: 10),
                Container(
                  width: double.infinity,
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: DesignColors.surfaceDark.withValues(alpha: 0.4),
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(color: DesignColors.borderDark),
                  ),
                  child: SelectableText(
                    hostSetup,
                    style: GoogleFonts.jetBrainsMono(fontSize: 12),
                  ),
                ),
                const SizedBox(height: 8),
                Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton.icon(
                    icon: const Icon(Icons.copy, size: 16),
                    label: const Text('Copy setup command'),
                    onPressed: () async {
                      await Clipboard.setData(ClipboardData(text: hostSetup));
                      if (!ctx.mounted) return;
                      ScaffoldMessenger.of(ctx).showSnackBar(
                        const SnackBar(
                          content: Text('Setup command copied'),
                          duration: Duration(seconds: 2),
                        ),
                      );
                    },
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  'Prereq: host has \$HOME/host-runner built from `cd hub && go build -o ~/host-runner ./cmd/host-runner`. Already on \$PATH? Drop the leading `~/`.',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 11, color: DesignColors.textMuted),
                ),
              ],
            ],
          ),
        ),
      );
    },
  );
}
