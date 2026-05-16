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
  // Engine kind parsed from the selected template's `backend.kind`.
  // Drives both the BackendRadio display and the `kind` field on the
  // spawn request. Defaults to claude-code for the legacy template;
  // refreshed whenever _templateName changes (or on initial load).
  String _currentKind = 'claude-code';
  bool _kindLoading = false;
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
        // Exclude the team's frozen general-steward — it's a
        // singleton (`@steward`, ensure-spawn endpoint) and must not
        // appear next to user-named domain stewards on this sheet.
        // Tap the home-tab "General Steward" card instead.
        if (name.startsWith('steward.general')) continue;
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
      // After picking the default template, fetch its YAML to learn the
      // engine kind. Without this the BackendRadio shows whatever
      // _currentKind defaulted to (claude-code), even when the user
      // selected steward.codex.v1 / steward.gemini.v1.
      await _refreshKindFor(_templateName);
    } catch (_) {
      if (mounted) setState(() => _templatesLoading = false);
    }
  }

  /// Parse `backend.kind: <value>` out of a steward template body.
  /// We only need a few fields, so tiny regexes are enough — adding
  /// the `yaml` package for read-only inspection would be overkill.
  /// Returns null when the field can't be located (e.g. malformed YAML
  /// or template that doesn't follow the convention).
  String? _parseBackendKind(String yaml) =>
      _parseNestedString(yaml, 'backend', 'kind');

  /// `backend.model: <value>` — only set on engines that pin the model
  /// at template time (claude-code). codex/gemini/kimi negotiate the
  /// model server-side and leave this field empty.
  String? _parseBackendModel(String yaml) =>
      _parseNestedString(yaml, 'backend', 'model');

  /// Top-level `driving_mode: <M1|M2|M4>`. Surfaced on the engine row
  /// so a YAML-side mode swap (M2→M4) is visible without opening the
  /// editor — the BackendRadio used to hardcode the per-engine mode
  /// blurb regardless of what the YAML actually said.
  String? _parseDrivingMode(String yaml) =>
      _parseTopLevelString(yaml, 'driving_mode');

  // Block-aware parser: find a parent key (e.g. `backend:`), then look
  // for an indented `<child>:` whose value is a single token. Resets
  // when a new top-level key appears so we don't bleed across blocks.
  String? _parseNestedString(String yaml, String parent, String child) {
    final lines = yaml.split('\n');
    final childRe = RegExp('^\\s+' + RegExp.escape(child) +
        r':\s*([A-Za-z0-9_.-]+)\s*$');
    var inBlock = false;
    for (final line in lines) {
      final trimmed = line.trimRight();
      if (trimmed == '$parent:') {
        inBlock = true;
        continue;
      }
      if (inBlock && trimmed.isNotEmpty && !trimmed.startsWith(' ')) {
        inBlock = false;
      }
      if (inBlock) {
        final m = childRe.firstMatch(line);
        if (m != null) return m.group(1);
      }
    }
    return null;
  }

  // Top-level `<key>: <value>` parser. Stops at the first match or EOF;
  // the YAML schema doesn't repeat top-level keys.
  String? _parseTopLevelString(String yaml, String key) {
    final re = RegExp('^' + RegExp.escape(key) +
        r':\s*([A-Za-z0-9_.-]+)\s*$', multiLine: true);
    final m = re.firstMatch(yaml);
    return m?.group(1);
  }

  // The latest YAML body for the selected template. Cached so
  // _BackendInfo's detail derivation doesn't refire the network on
  // every rebuild. Cleared whenever _refreshKindFor starts a new fetch.
  String? _currentDrivingMode;
  String? _currentModel;

  Future<void> _refreshKindFor(String templateName) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    if (mounted) setState(() => _kindLoading = true);
    try {
      final yaml = await client.getTemplate(
        'agents',
        templateName,
        merged: true,
      );
      final kind = _parseBackendKind(yaml) ?? 'claude-code';
      final mode = _parseDrivingMode(yaml);
      final model = _parseBackendModel(yaml);
      if (!mounted) return;
      setState(() {
        _currentKind = kind;
        _currentDrivingMode = mode;
        _currentModel = model;
        _kindLoading = false;
      });
    } catch (_) {
      if (mounted) setState(() => _kindLoading = false);
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
      // Fall back to parsing the just-fetched YAML if _currentKind hasn't
      // been refreshed yet (e.g. user races the dropdown). Belt-and-braces:
      // _refreshKindFor runs on selection, but it's async, so ensure the
      // request payload reflects the actual template the operator picked.
      final kindForSpawn = _parseBackendKind(yaml) ?? _currentKind;
      final res = await client.spawnAgent(
        childHandle: handle,
        kind: kindForSpawn,
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
                      // Fire the YAML refetch outside setState so the UI
                      // reflects the new template name immediately while
                      // _currentKind catches up asynchronously.
                      _refreshKindFor(v);
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
                // Backend display — derived from the selected template's
                // `backend.kind`. Stewards can pick claude-code, codex, or
                // gemini-cli today (steward.{codex,gemini}.v1.yaml ship
                // bundled). The widget shows whichever one this template
                // wires up; the spawn submits the same kind on the wire.
                _BackendInfo(
                  kind: _currentKind,
                  drivingMode: _currentDrivingMode,
                  model: _currentModel,
                  loading: _kindLoading,
                ),
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

/// Static info row showing the backend that the selected steward
/// template wires up. Reads `backend.kind`, `driving_mode`, and
/// `backend.model` from the YAML so a YAML-side mode swap (M2→M4) or
/// model swap is visible without opening the editor. The operator
/// changes engine by picking a different template, not a separate
/// radio. Engines we ship today: claude-code, codex, gemini-cli,
/// kimi-code.
class _BackendInfo extends StatelessWidget {
  final String kind;
  final String? drivingMode;
  final String? model;
  final bool loading;
  const _BackendInfo({
    required this.kind,
    required this.loading,
    this.drivingMode,
    this.model,
  });

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final info = _engineInfoFor(kind, drivingMode, model);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        border: Border.all(color: scheme.outlineVariant),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Row(
        children: [
          Icon(info.icon, size: 18, color: scheme.primary),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Text(
                      info.label,
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 13,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    if (loading) ...[
                      const SizedBox(width: 8),
                      const SizedBox(
                        width: 10,
                        height: 10,
                        child: CircularProgressIndicator(strokeWidth: 1.5),
                      ),
                    ],
                  ],
                ),
                Text(
                  info.detail,
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

  /// Per-engine display values. `kind` matches the value the hub stamps
  /// on `agents.kind` and what the spawn endpoint expects on the wire.
  /// `mode` and `model` are pulled from the template YAML so a swap
  /// in either field surfaces immediately.
  static _EngineInfo _engineInfoFor(String kind, String? mode, String? model) {
    final modeBlurb = _modeBlurb(kind, mode);
    switch (kind) {
      case 'codex':
        return _EngineInfo(
          label: 'Codex',
          detail: _joinDetail([
            modeBlurb,
            'app-server JSON-RPC',
            'per-tool approval bridge',
          ]),
          icon: Icons.smart_toy_outlined,
        );
      case 'gemini-cli':
        return _EngineInfo(
          label: 'Gemini CLI',
          detail: _joinDetail([
            modeBlurb,
            'exec-per-turn with --resume',
            'MCP via settings.json',
          ]),
          icon: Icons.auto_awesome_motion_outlined,
        );
      case 'kimi-code':
        return _EngineInfo(
          label: 'Kimi Code',
          detail: _joinDetail([
            modeBlurb,
            'ACP over stdio',
            'permission_prompt MCP gate',
          ]),
          icon: Icons.bolt_outlined,
        );
      case 'claude-code':
        return _EngineInfo(
          label: 'Claude Code',
          detail: _joinDetail([
            modeBlurb,
            (model == null || model.isEmpty) ? null : _shortModel(model),
            // Mode-derived transport hint: M2 streams stdio, M4 tails
            // a JSONL log. Without the mode-aware branch the chip used
            // to claim "stream-json" even on an M4 spawn.
            _transportFor(mode),
            'MCP permission gate',
          ]),
          icon: Icons.radio_button_checked,
        );
      default:
        return _EngineInfo(
          label: kind.isEmpty ? 'Unknown engine' : kind,
          detail: _joinDetail([
            modeBlurb,
            'kind=$kind (custom template)',
          ]),
          icon: Icons.help_outline,
        );
    }
  }

  // Display the driving mode as the leading blurb so it's the first
  // signal the user reads. Falls back to the kind's typical mode when
  // the YAML doesn't set one explicitly (templates that omit it
  // inherit the launcher's per-engine default).
  static String? _modeBlurb(String kind, String? mode) {
    if (mode == null || mode.isEmpty) return null;
    return mode;
  }

  // Per-mode transport hint for claude-code. Other engines have a
  // single transport so we don't render a placeholder.
  static String? _transportFor(String? mode) {
    switch (mode) {
      case 'M1':
        return 'ACP stdio';
      case 'M2':
        return 'stream-json';
      case 'M4':
        return 'JSONL tail';
      default:
        return null;
    }
  }

  // Trim long claude model strings ("claude-opus-4-7-20260101") down
  // to family + version so the chip stays readable.
  static String _shortModel(String raw) {
    if (raw.startsWith('claude-')) {
      final parts = raw.split('-');
      if (parts.length >= 4) return '${parts[1]} ${parts[2]}.${parts[3]}';
    }
    return raw;
  }

  // Join with the canonical separator, dropping null/empty fragments.
  static String _joinDetail(List<String?> parts) =>
      parts.where((s) => s != null && s.isNotEmpty).join(' · ');
}

class _EngineInfo {
  final String label;
  final String detail;
  final IconData icon;
  const _EngineInfo({
    required this.label,
    required this.detail,
    required this.icon,
  });
}
