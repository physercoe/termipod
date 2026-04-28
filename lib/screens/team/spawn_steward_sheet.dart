import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../providers/hub_provider.dart';
import '../../services/steward_handle.dart';
import '../../theme/design_colors.dart';

/// SharedPreferences key prefix for the per-team "user dismissed the
/// bootstrap sheet" flag. We don't want to nag a user who explicitly
/// chose Skip on every cold start, but we also want the flag to clear
/// itself when they switch to a fresh team.
const _kBootstrapDismissedPrefix = 'steward_bootstrap_dismissed_';

String bootstrapDismissedKey(String teamId) =>
    '$_kBootstrapDismissedPrefix$teamId';

/// Team-scoped bootstrap sheet that spawns the one and only steward. This
/// is intentionally different from [showSpawnAgentSheet]:
///
///   - the handle is fixed to `steward` (reserved; see `stewardPresent`
///     in projects_screen.dart and the seed-demo check in the audit log),
///   - the spec comes from the shipped `agents/steward.v1` template so
///     operators don't hand-roll principal / journal wiring,
///   - there is no preset bar, no kind field, no free-form YAML — the
///     steward is a team singleton, not a fleet member.
///
/// Surfaced from two places: the Projects AppBar "No steward" chip
/// (manual entry), and an auto-trigger on Projects-screen first-load
/// when the team has hosts but no steward (W4 bootstrap UX). The
/// auto-trigger respects a per-team "dismissed" flag so a user who
/// taps Skip isn't nagged on every cold start.
Future<void> showSpawnStewardSheet(
  BuildContext context, {
  required List<Map<String, dynamic>> hosts,
  bool autoTriggered = false,
  String? sessionId,
}) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    isDismissible: !autoTriggered,
    enableDrag: !autoTriggered,
    builder: (_) => _SpawnStewardSheet(
      hosts: hosts,
      autoTriggered: autoTriggered,
      sessionId: sessionId,
    ),
  );
}

class _SpawnStewardSheet extends ConsumerStatefulWidget {
  final List<Map<String, dynamic>> hosts;
  final bool autoTriggered;
  /// When non-null, the spawn lands inside this existing session
  /// (the "Replace steward / switch engine" path). The hub
  /// terminates the session's prior agent and rewrites the session
  /// to point at the new spawn — transcript continuity is the whole
  /// point of this code path.
  final String? sessionId;
  const _SpawnStewardSheet({
    required this.hosts,
    this.autoTriggered = false,
    this.sessionId,
  });

  @override
  ConsumerState<_SpawnStewardSheet> createState() => _SpawnStewardSheetState();
}

class _SpawnStewardSheetState extends ConsumerState<_SpawnStewardSheet> {
  bool _busy = false;
  String? _error;
  String? _hostId;
  /// Permission mode for claude's tool calls. "skip" = auto-allow (PC
  /// behaviour, default for the demo), "prompt" = route every tool call
  /// through the MCP gateway → attention_items (only useful once W2 is
  /// shipped — picking it on a hub that doesn't register the MCP tool
  /// will hang claude on the first tool call). Kept here so the user can
  /// flip between modes per-spawn without editing the steward template.
  String _permissionMode = 'skip';
  final TextEditingController _personaCtrl = TextEditingController();
  // Multi-steward fields. The handle picker accepts plain `steward`
  // when no other live steward owns it, or any `<name>-steward` form.
  // The template picker lists every `agents/steward*.yaml` so domain
  // templates show up automatically once they land in
  // team/templates/agents/.
  late final TextEditingController _handleCtrl;
  String _templateName = 'steward.v1.yaml';
  List<String> _stewardTemplates = const ['steward.v1.yaml'];
  bool _templatesLoading = true;
  // Live steward handles (running/pending/paused) on this team —
  // populated from hubProvider in initState. Used to (a) pre-validate
  // the handle field against collision, (b) pick a sensible default
  // when 'steward' is taken.
  Set<String> _liveStewardHandles = const {};

  @override
  void initState() {
    super.initState();
    final online = widget.hosts.where(
      (h) => (h['status']?.toString() ?? '') == 'online',
    );
    if (widget.hosts.isNotEmpty) {
      _hostId = (online.isNotEmpty ? online.first : widget.hosts.first)['id']
          ?.toString();
    }
    _handleCtrl = TextEditingController();
    // Pull live stewards from the cached hub state — avoids a second
    // round-trip when the sheet opens. Empty when hub state isn't
    // loaded yet (rare).
    final hub = ref.read(hubProvider).value;
    if (hub != null) {
      final live = <String>{};
      for (final a in hub.agents) {
        final handle = (a['handle'] ?? '').toString();
        if (!isStewardHandle(handle)) continue;
        final status = (a['status'] ?? '').toString();
        if (status == 'running' ||
            status == 'pending' ||
            status == 'paused') {
          live.add(handle);
        }
      }
      _liveStewardHandles = live;
    }
    // Default name: 'steward' if free, otherwise leave blank so the
    // user picks a domain (e.g. `research`, `infra`). The Name field
    // is the user-facing label; the app appends `-steward`
    // internally on submit via normalizeStewardHandle.
    _handleCtrl.text =
        _liveStewardHandles.contains('steward') ? '' : 'steward';
    _loadTemplates();
  }

  Future<void> _loadTemplates() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      if (mounted) setState(() => _templatesLoading = false);
      return;
    }
    try {
      final all = await client.listTemplates();
      if (!mounted) return;
      final picks = <String>[];
      for (final row in all) {
        final cat = (row['category'] ?? '').toString();
        final name = (row['name'] ?? '').toString();
        if (cat != 'agents') continue;
        if (!name.startsWith('steward')) continue;
        picks.add(name);
      }
      picks.sort();
      setState(() {
        _stewardTemplates = picks.isEmpty ? const ['steward.v1.yaml'] : picks;
        _templatesLoading = false;
        if (!_stewardTemplates.contains(_templateName)) {
          _templateName = _stewardTemplates.first;
        }
      });
    } catch (_) {
      if (mounted) setState(() => _templatesLoading = false);
    }
  }

  // Parse `steward.research.v1.yaml` → `research` to seed the Name
  // field when the user picks a domain template. Returns the bare
  // name (no `-steward` suffix) since that's what the user types.
  // Falls back to empty for anything that doesn't match the
  // convention so we don't clobber a name the user already typed.
  String _suggestedHandleFor(String tpl) {
    final m = RegExp(r'^steward\.([a-z][a-z0-9-]*)\.v\d+\.yaml$')
        .firstMatch(tpl);
    if (m == null) return '';
    return m.group(1)!;
  }

  @override
  void dispose() {
    _personaCtrl.dispose();
    _handleCtrl.dispose();
    super.dispose();
  }

  Future<void> _spawn() async {
    // The Name field accepts the bare domain (e.g. `research`); the
    // app appends `-steward` before sending so the server sees the
    // canonical handle. validateStewardHandle takes the normalized
    // form so its `[a-z][a-z0-9-]*-steward` pattern still applies.
    final raw = _handleCtrl.text.trim();
    final handle = normalizeStewardHandle(raw);
    final handleErr = validateStewardHandle(handle);
    if (handleErr != null) {
      setState(() => _error = handleErr);
      return;
    }
    if (widget.sessionId == null && _liveStewardHandles.contains(handle)) {
      final suggested = stewardLabel(_suggestNextHandle(handle));
      setState(() => _error =
          'A live steward already uses the name "${stewardLabel(handle)}". '
          'Pick a different one (e.g. $suggested).');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final client = ref.read(hubProvider.notifier).client;
      if (client == null) throw StateError('Hub not configured');
      final yaml = await client.getTemplate(
        'agents',
        _templateName,
        merged: true,
      );
      final res = await client.spawnAgent(
        childHandle: handle,
        kind: 'claude-code',
        spawnSpecYaml: yaml,
        hostId: _hostId,
        personaSeed: _personaCtrl.text,
        permissionMode: _permissionMode,
        sessionId: widget.sessionId,
        // Atomic spawn-with-session for the fresh-spawn path. The swap
        // path (sessionId != null) updates the named session in-tx
        // server-side; auto_open_session is ignored there.
        autoOpenSession: widget.sessionId == null,
      );
      if (!mounted) return;
      final status = res['status']?.toString() ?? '';
      final msg = status == 'pending_approval'
          ? 'Spawn request sent — awaiting approval.'
          : 'Steward spawned.';
      // Clear the dismissed flag so the auto-trigger doesn't nag if the
      // user later terminates the steward and wants a clean slate.
      await _clearDismissed();
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
      await ref.read(hubProvider.notifier).refreshAll();
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  // Pick the next free derivative when a collision happens. Tries
  // handle-2, handle-3, …; bounded to keep the suggestion reasonable
  // for a quick visual hint.
  String _suggestNextHandle(String taken) {
    for (var i = 2; i < 20; i++) {
      final cand = '$taken-$i';
      if (!_liveStewardHandles.contains(cand)) return cand;
    }
    return '${taken}-N';
  }

  Future<void> _skip() async {
    await _markDismissed();
    if (!mounted) return;
    Navigator.of(context).pop();
  }

  Future<void> _markDismissed() async {
    final teamId =
        ref.read(hubProvider).value?.config?.teamId ?? '';
    if (teamId.isEmpty) return;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(
        bootstrapDismissedKey(teamId), DateTime.now().toIso8601String());
  }

  Future<void> _clearDismissed() async {
    final teamId =
        ref.read(hubProvider).value?.config?.teamId ?? '';
    if (teamId.isEmpty) return;
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(bootstrapDismissedKey(teamId));
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final bottomInset = MediaQuery.of(context).viewInsets.bottom;
    final canSpawn = widget.hosts.isNotEmpty && !_busy;
    // Cap the sheet at ~85% of screen height and make its content
    // scrollable. Without this, tall configurations (template +
    // handle + host + backend + permission + persona seed) push the
    // Start/Cancel row off the bottom of the sheet on phones, and
    // the principal can't reach it. SafeArea handles top/bottom
    // notches; viewInsets.bottom keeps the layout above the
    // keyboard when the persona-seed field is focused.
    final maxHeight = MediaQuery.of(context).size.height * 0.85;
    return SafeArea(
      child: Padding(
        padding: EdgeInsets.only(bottom: bottomInset),
        child: ConstrainedBox(
          constraints: BoxConstraints(maxHeight: maxHeight),
        child: SingleChildScrollView(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 16, 20, 24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Icon(Icons.auto_awesome,
                      size: 24, color: scheme.primary),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Text(
                      widget.autoTriggered
                          ? 'Start your steward'
                          : 'Spawn a steward',
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 18,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                  if (!widget.autoTriggered)
                    IconButton(
                      icon: const Icon(Icons.close),
                      onPressed: () => Navigator.of(context).pop(),
                    ),
                ],
              ),
              const SizedBox(height: 12),
              Text(
                'A steward is the agent you talk to. It takes your '
                'directions in chat sessions, raises attention items '
                'when it needs your call, and hands work out to project '
                'agents. Teams can run several stewards in parallel '
                '(one per domain — research, infra, …); each runs as '
                'its own process and keeps its own conversation.',
                style: GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.4),
              ),
              const SizedBox(height: 16),
              if (widget.hosts.isEmpty) ...[
                Container(
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: scheme.errorContainer,
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: Text(
                    'No host registered. Register a host-runner first '
                    '(see docs/hub-host-setup.md), then come back here.',
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      color: scheme.onErrorContainer,
                    ),
                  ),
                ),
              ] else ...[
                // Template picker — hidden when only one steward template
                // is on the team (the legacy single-steward case stays
                // exactly as it was before multi-steward landed).
                if (_stewardTemplates.length > 1) ...[
                  DropdownButtonFormField<String>(
                    initialValue: _templateName,
                    isExpanded: true,
                    decoration: const InputDecoration(
                      labelText: 'Template',
                      border: OutlineInputBorder(),
                    ),
                    items: _stewardTemplates
                        .map((t) => DropdownMenuItem<String>(
                              value: t,
                              child: Text(t),
                            ))
                        .toList(),
                    onChanged: (v) {
                      if (v == null) return;
                      setState(() {
                        _templateName = v;
                        // Auto-suggest a matching name (bare, no
                        // `-steward` suffix) when the user hasn't
                        // typed one yet, has just the default
                        // `steward`, or has a prior suggestion in
                        // place. Don't clobber a hand-typed name.
                        final suggest = _suggestedHandleFor(v);
                        if (suggest.isNotEmpty &&
                            (_handleCtrl.text.trim().isEmpty ||
                                _handleCtrl.text.trim() == 'steward' ||
                                _stewardTemplates.any((t) =>
                                    _suggestedHandleFor(t) ==
                                    _handleCtrl.text.trim()))) {
                          // Only swap when the new suggestion would
                          // actually be free; otherwise leave the
                          // field alone and let the validator yell.
                          // _liveStewardHandles stores the canonical
                          // form (`research-steward`); compare against
                          // the normalized version of the suggestion.
                          if (!_liveStewardHandles.contains(
                              normalizeStewardHandle(suggest))) {
                            _handleCtrl.text = suggest;
                          }
                        }
                      });
                    },
                  ),
                  const SizedBox(height: 12),
                ],
                // Handle field — required, validates against the
                // steward-handle convention + collision with live
                // stewards. Hidden when nothing else exists yet AND
                // the template is the legacy steward.v1.yaml (single-
                // steward bootstrap UX is unchanged).
                if (_liveStewardHandles.isNotEmpty ||
                    _templateName != 'steward.v1.yaml') ...[
                  TextFormField(
                    controller: _handleCtrl,
                    enabled: !_busy,
                    decoration: InputDecoration(
                      labelText: 'Name',
                      hintText: 'steward, research, infra-east, …',
                      helperText: _templatesLoading
                          ? 'Loading templates…'
                          : 'Lowercase, digits, dashes. '
                              'Must be unique among live stewards on '
                              'this team — stopping a steward frees '
                              'the name.',
                      border: const OutlineInputBorder(),
                    ),
                  ),
                  const SizedBox(height: 12),
                ],
                DropdownButtonFormField<String>(
                  initialValue: _hostId,
                  isExpanded: true,
                  decoration: const InputDecoration(
                    labelText: 'Host',
                    border: OutlineInputBorder(),
                  ),
                  items: widget.hosts
                      .map((h) => DropdownMenuItem<String>(
                            value: h['id']?.toString(),
                            child: Text(
                              '${h['name'] ?? '?'} '
                              '(${h['status'] ?? 'unknown'})',
                            ),
                          ))
                      .toList(),
                  onChanged: (v) => setState(() => _hostId = v),
                ),
                const SizedBox(height: 12),
                // Backend selector — single option in v1, but the radio
                // makes the future expansion (Codex) obvious without
                // requiring another sheet rev when it lands.
                _BackendRadio(),
                const SizedBox(height: 12),
                _PermissionModeSelector(
                  value: _permissionMode,
                  onChanged: _busy
                      ? null
                      : (v) => setState(() => _permissionMode = v),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: _personaCtrl,
                  minLines: 3,
                  maxLines: 6,
                  textCapitalization: TextCapitalization.sentences,
                  decoration: InputDecoration(
                    labelText: 'Persona seed (optional)',
                    border: const OutlineInputBorder(),
                    hintText:
                        "e.g. You're terse. Always cite line numbers when "
                        "referencing code.",
                    hintStyle: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      color: DesignColors.textMuted,
                    ),
                  ),
                  style: GoogleFonts.spaceGrotesk(fontSize: 13),
                ),
                const SizedBox(height: 6),
                Text(
                  'Appended under "## Persona override" in the agent\'s '
                  'CLAUDE.md so you can see exactly what the agent reads.',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 11,
                    color: DesignColors.textMuted,
                  ),
                ),
              ],
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(_error!,
                    style: TextStyle(color: scheme.error)),
              ],
              const SizedBox(height: 20),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  if (widget.autoTriggered)
                    TextButton(
                      onPressed: _busy ? null : _skip,
                      child: const Text('Skip for now'),
                    )
                  else
                    TextButton(
                      onPressed: _busy
                          ? null
                          : () => Navigator.of(context).pop(),
                      child: const Text('Cancel'),
                    ),
                  const SizedBox(width: 8),
                  FilledButton.icon(
                    onPressed: canSpawn ? _spawn : null,
                    icon: _busy
                        ? const SizedBox(
                            width: 16,
                            height: 16,
                            child:
                                CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.arrow_forward),
                    label: Text(_busy ? 'Starting…' : 'Start →'),
                  ),
                ],
              ),
            ],
          ),
        ),
        ),
        ),
      ),
    );
  }
}

/// Permission-mode selector. Determines which CLI flag the hub appends
/// to claude's `cmd` for this spawn:
///
///   - "skip" → `--dangerously-skip-permissions`. Auto-allows every tool
///     call. PC-style behaviour: claude can run tools the same way it
///     does in a local terminal, no per-tool prompts. This is the demo
///     default — higher-level decisions (spawning agents, editing
///     policy, sending money-relevant calls) are what should reach the
///     human as attention items, not every individual edit.
///   - "prompt" → `--permission-prompt-tool mcp__termipod__permission_prompt`.
///     Routes every tool call through the MCP gateway, which currently
///     surfaces them as attention_items for the principal to approve.
///     Useful for testing the W2 attention flow, but on a hub that
///     hasn't registered the MCP tool yet, claude will hang on the
///     first tool call.
class _PermissionModeSelector extends StatelessWidget {
  final String value;
  final ValueChanged<String>? onChanged;
  const _PermissionModeSelector({required this.value, this.onChanged});

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      decoration: BoxDecoration(
        border: Border.all(color: scheme.outlineVariant),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 12, 4),
            child: Text(
              'Tool permissions',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                fontWeight: FontWeight.w600,
                color: DesignColors.textMuted,
              ),
            ),
          ),
          RadioListTile<String>(
            value: 'skip',
            groupValue: value,
            onChanged: onChanged == null ? null : (v) => onChanged!(v!),
            dense: true,
            contentPadding: const EdgeInsets.symmetric(horizontal: 8),
            title: Text(
              'Allow all tools (PC mode)',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
            subtitle: Text(
              'Auto-approve every tool call — same as running claude '
              'locally with --dangerously-skip-permissions.',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
          ),
          RadioListTile<String>(
            value: 'prompt',
            groupValue: value,
            onChanged: onChanged == null ? null : (v) => onChanged!(v!),
            dense: true,
            contentPadding: const EdgeInsets.symmetric(horizontal: 8),
            title: Text(
              'Prompt for each tool (attention)',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
            subtitle: Text(
              'Route every tool call through the MCP gateway → attention '
              'items. Requires the W2 MCP tool registered on this hub; '
              'otherwise claude will hang on the first tool call.',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// Single-option radio for the agent backend. v1 ships claude-code only;
/// the radio is here so adding codex (or other backends) later is a
/// one-line list extension rather than a sheet redesign.
class _BackendRadio extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        border: Border.all(color: scheme.outlineVariant),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Row(
        children: [
          Icon(Icons.radio_button_checked, size: 18, color: scheme.primary),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Claude Code',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                Text(
                  'opus-4-7 · stream-json · MCP permission gate',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: DesignColors.textMuted,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
