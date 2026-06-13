import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../l10n/app_localizations.dart';
import '../../providers/hub_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../theme/tokens.dart';
import '../../services/hub/spawn_preset_service.dart';
import '../../services/steward_handle.dart';
import '../../services/template_filter.dart';
import '../../services/vocab/vocab_axis.dart';
import '../team/template_icon.dart';

/// Bottom-sheet editor for spawning an agent. Shared by the project detail
/// Agents pill (pre-fills `project_id:` in the YAML so the backend binds
/// marker forwarding to the right project) and any future team-scope
/// spawn surface.
///
/// Steward spawn has its own entry point (see `spawn_steward_sheet.dart`)
/// because it's always team-scoped, always uses the `steward.v1` template,
/// and never needs the handle/kind/preset machinery.
Future<void> showSpawnAgentSheet(
  BuildContext context, {
  required List<Map<String, dynamic>> hosts,
  String? projectId,
}) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    builder: (_) => _SpawnAgentDialog(hosts: hosts, projectId: projectId),
  );
}

class _SpawnAgentDialog extends ConsumerStatefulWidget {
  final List<Map<String, dynamic>> hosts;
  final String? projectId;
  const _SpawnAgentDialog({required this.hosts, this.projectId});

  @override
  ConsumerState<_SpawnAgentDialog> createState() => _SpawnAgentDialogState();
}

class _SpawnAgentDialogState extends ConsumerState<_SpawnAgentDialog> {
  final _handleCtl = TextEditingController();
  final _kindCtl = TextEditingController(text: 'claude-code');
  late final TextEditingController _yamlCtl;
  String? _hostId;
  bool _busy = false;
  String? _error;
  List<Map<String, dynamic>>? _templates;
  final _presetSvc = SpawnPresetService();
  List<SpawnPreset> _presets = const [];

  @override
  void initState() {
    super.initState();
    // Seed the YAML with a project_id binding when the caller is scoped
    // to a project — otherwise marker forwarding has no channel target.
    final pid = widget.projectId;
    _yamlCtl = TextEditingController(
      text: pid == null
          ? 'backend:\n  cmd: "claude --model opus-4-7 --no-update"\n'
          : 'backend:\n  cmd: "claude --model opus-4-7 --no-update"\n'
              'project_id: "$pid"\n',
    );
    final online = widget.hosts.where(
      (h) => (h['status']?.toString() ?? '') == 'online',
    );
    if (widget.hosts.isNotEmpty) {
      _hostId = (online.isNotEmpty ? online.first : widget.hosts.first)['id']
          ?.toString();
    }
    _loadPresets();
  }

  Future<void> _loadPresets() async {
    final items = await _presetSvc.load();
    if (!mounted) return;
    setState(() => _presets = items);
  }

  void _applyPreset(SpawnPreset p) {
    setState(() {
      _handleCtl.text = p.handle;
      _kindCtl.text = p.kind;
      _yamlCtl.text = p.yaml;
    });
  }

  Future<void> _deletePreset(SpawnPreset p) async {
    final items = await _presetSvc.delete(p.id);
    if (!mounted) return;
    setState(() => _presets = items);
    final l10n = AppLocalizations.of(context)!;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(l10n.presetDeletedNamed(p.name))));
  }

  Future<void> _confirmDeletePreset(SpawnPreset p) async {
    final l10n = AppLocalizations.of(context)!;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.deletePresetTitle(p.name)),
        content: Text(l10n.deletePresetBody),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: Text(l10n.buttonCancel),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(ctx).colorScheme.error,
            ),
            child: Text(l10n.buttonDelete),
          ),
        ],
      ),
    );
    if (ok == true) await _deletePreset(p);
  }

  Future<void> _saveAsPreset() async {
    final l10n = AppLocalizations.of(context)!;
    final handle = _handleCtl.text.trim();
    final kind = _kindCtl.text.trim();
    final yaml = _yamlCtl.text;
    if (handle.isEmpty || kind.isEmpty || yaml.trim().isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.spawnFillFirst)),
      );
      return;
    }
    final nameCtl = TextEditingController(text: handle);
    final name = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.saveSpawnPreset),
        content: TextField(
          controller: nameCtl,
          autofocus: true,
          decoration: InputDecoration(
            labelText: l10n.presetNameLabel,
            border: const OutlineInputBorder(),
          ),
          onSubmitted: (v) => Navigator.of(ctx).pop(v.trim()),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text(l10n.buttonCancel),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(nameCtl.text.trim()),
            child: Text(l10n.buttonSave),
          ),
        ],
      ),
    );
    if (name == null || name.isEmpty) return;
    final preset = SpawnPreset(
      id: DateTime.now().microsecondsSinceEpoch.toString(),
      name: name,
      handle: handle,
      kind: kind,
      yaml: yaml,
    );
    final items = await _presetSvc.upsert(preset);
    if (!mounted) return;
    setState(() => _presets = items);
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(l10n.presetSavedNamed(name))));
  }

  @override
  void dispose() {
    _handleCtl.dispose();
    _kindCtl.dispose();
    _yamlCtl.dispose();
    super.dispose();
  }

  Future<void> _loadTemplate() async {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.read(vocabularyProvider);
    final agentTerm = vocab.term(VocabAxis.roleAgent);
    final templateTerm = vocab.term(VocabAxis.entityTemplate);
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      _templates ??= await client.listTemplates();
      // Resolve the project's template_id so applicable_to.template_ids
      // can filter the picker. Falls back to "" when the spawn isn't
      // project-scoped (legacy path) — filterTemplatesForProject then
      // returns team-shared templates only, which is the right default.
      final pid = widget.projectId ?? '';
      final hub = ref.read(hubProvider).value;
      String? projectTemplateId;
      if (pid.isNotEmpty && hub != null) {
        for (final p in hub.projects) {
          if ((p['id'] ?? '').toString() == pid) {
            projectTemplateId = (p['template_id'] ?? '').toString();
            break;
          }
        }
      }
      final agentTemplates = filterTemplatesForProject(
        _templates!
            .where((t) => (t['category']?.toString() ?? '') == 'agents')
            .toList(),
        projectTemplateId,
      );
      if (!mounted) return;
      if (agentTemplates.isEmpty) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.noAgentTemplates(agentTerm.lower))),
        );
        return;
      }
      final picked = await showModalBottomSheet<Map<String, dynamic>>(
        context: context,
        builder: (_) => ListView(
          shrinkWrap: true,
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: Text(l10n.pickTemplateTitle(templateTerm.lower),
                  style: const TextStyle(fontWeight: FontWeight.w700)),
            ),
            const Divider(height: 1),
            for (final t in agentTemplates)
              ListTile(
                leading: templateIconWidget(
                  idOrName: t['name']?.toString() ?? '',
                  displayName: t['name']?.toString() ?? '?',
                  size: 24,
                ),
                title: Text(t['name']?.toString() ?? '?'),
                subtitle: Text('${t['size'] ?? 0}B'),
                onTap: () => Navigator.of(context).pop(t),
              ),
          ],
        ),
      );
      if (picked == null || !mounted) return;
      final name = picked['name']?.toString() ?? '';
      final body = await client.getTemplate('agents', name, merged: true);
      if (!mounted) return;
      setState(() => _yamlCtl.text = body);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(l10n.loadTemplateFailed('$e'))));
    }
  }

  Future<void> _submit() async {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.read(vocabularyProvider);
    final agentTerm = vocab.term(VocabAxis.roleAgent);
    final stewardTerm = vocab.term(VocabAxis.roleSteward);
    final handle = _handleCtl.text.trim();
    final kind = _kindCtl.text.trim();
    final yaml = _yamlCtl.text;
    if (handle.isEmpty || kind.isEmpty || yaml.trim().isEmpty) {
      setState(() => _error = l10n.spawnRequiredFields);
      return;
    }
    if (isStewardHandle(handle)) {
      setState(() => _error = l10n.stewardHandleReserved(stewardTerm.title));
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final client = ref.read(hubProvider.notifier).client;
      if (client == null) throw StateError('Hub not configured');
      final res = await client.spawnAgent(
        childHandle: handle,
        kind: kind,
        spawnSpecYaml: yaml,
        hostId: _hostId,
      );
      if (!mounted) return;
      final status = res['status']?.toString() ?? '';
      final msg = status == 'pending_approval'
          ? l10n.spawnRequestSent
          : l10n.spawnAgentSpawned(agentTerm.title, handle);
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

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final agentTerm = vocab.term(VocabAxis.roleAgent);
    final projectTerm = vocab.term(VocabAxis.entityProject);
    final bottomInset = MediaQuery.of(context).viewInsets.bottom;
    return DraggableScrollableSheet(
      initialChildSize: 0.9,
      minChildSize: 0.5,
      maxChildSize: 0.95,
      expand: false,
      builder: (_, scroll) => Padding(
        padding: EdgeInsets.only(bottom: bottomInset),
        child: Column(
          children: [
            Padding(
              padding:
                  const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      widget.projectId == null
                          ? l10n.spawnAgentTitle(agentTerm.lower)
                          : l10n.spawnProjectAgentTitle(
                              projectTerm.lower, agentTerm.lower),
                      style: const TextStyle(
                          fontSize: 16, fontWeight: FontWeight.w700),
                    ),
                  ),
                  IconButton(
                    icon: const Icon(Icons.close),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                ],
              ),
            ),
            const Divider(height: 1),
            Expanded(
              child: ListView(
                controller: scroll,
                padding: const EdgeInsets.all(16),
                children: [
                  if (_presets.isNotEmpty) ...[
                    Row(
                      children: [
                        Text(l10n.presetsLabel,
                            style: const TextStyle(
                                fontWeight: FontWeight.w600)),
                        const Spacer(),
                        Text(l10n.longPressToDelete,
                            style: GoogleFonts.jetBrainsMono(
                              fontSize: FontSizes.label,
                              color: Theme.of(context)
                                  .textTheme
                                  .bodySmall
                                  ?.color
                                  ?.withValues(alpha: 0.7),
                            )),
                      ],
                    ),
                    const SizedBox(height: 6),
                    SizedBox(
                      height: 36,
                      child: ListView.separated(
                        scrollDirection: Axis.horizontal,
                        itemCount: _presets.length,
                        separatorBuilder: (_, __) => const SizedBox(width: 6),
                        itemBuilder: (_, i) {
                          final p = _presets[i];
                          return GestureDetector(
                            onLongPress: () => _confirmDeletePreset(p),
                            child: ActionChip(
                              avatar: const Icon(Icons.bolt, size: 16),
                              label: Text(p.name),
                              onPressed: () => _applyPreset(p),
                            ),
                          );
                        },
                      ),
                    ),
                    const SizedBox(height: 12),
                  ],
                  TextField(
                    controller: _handleCtl,
                    decoration: InputDecoration(
                      labelText: l10n.handleLabel,
                      hintText: l10n.handleHint,
                      border: const OutlineInputBorder(),
                    ),
                    autofocus: true,
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: _kindCtl,
                    decoration: InputDecoration(
                      labelText: l10n.fieldKind,
                      hintText: l10n.kindHint,
                      border: const OutlineInputBorder(),
                    ),
                  ),
                  const SizedBox(height: 12),
                  DropdownButtonFormField<String>(
                    initialValue: _hostId,
                    isExpanded: true,
                    decoration: InputDecoration(
                      labelText: l10n.hostLabel,
                      border: const OutlineInputBorder(),
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
                  Row(
                    children: [
                      Expanded(
                        child: Text(l10n.spawnSpecYamlLabel,
                            style: const TextStyle(
                                fontWeight: FontWeight.w600)),
                      ),
                      IconButton(
                        onPressed: _saveAsPreset,
                        icon: const Icon(Icons.bookmark_add_outlined,
                            size: 20),
                        tooltip: l10n.saveAsPreset,
                        visualDensity: VisualDensity.compact,
                      ),
                      IconButton(
                        onPressed: _loadTemplate,
                        icon: const Icon(Icons.file_open, size: 20),
                        tooltip: l10n.loadTemplateTooltip,
                        visualDensity: VisualDensity.compact,
                      ),
                    ],
                  ),
                  const SizedBox(height: 4),
                  TextField(
                    controller: _yamlCtl,
                    maxLines: 14,
                    style: GoogleFonts.jetBrainsMono(fontSize: 12),
                    decoration: const InputDecoration(
                      hintText:
                          'backend:\n  cmd: "claude --model opus-4-7"\n',
                      border: OutlineInputBorder(),
                      alignLabelWithHint: true,
                    ),
                  ),
                  if (_error != null) ...[
                    const SizedBox(height: 12),
                    Text(_error!,
                        style: TextStyle(
                            color: Theme.of(context).colorScheme.error)),
                  ],
                  const SizedBox(height: 16),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.end,
                    children: [
                      TextButton(
                        onPressed:
                            _busy ? null : () => Navigator.of(context).pop(),
                        child: Text(l10n.buttonCancel),
                      ),
                      const SizedBox(width: 8),
                      FilledButton.icon(
                        onPressed: _busy ? null : _submit,
                        icon: _busy
                            ? const SizedBox(
                                width: 16,
                                height: 16,
                                child: CircularProgressIndicator(
                                    strokeWidth: 2),
                              )
                            : const Icon(Icons.play_arrow),
                        label: Text(
                            _busy ? l10n.spawningButton : l10n.spawnButton),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
