import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';

/// Team environment-profile manager (env-profiles plan, E2c-2). List +
/// create/edit/delete over the hub REST surface; the spawn sheet's picker
/// (E2c-1) lists what is defined here. Metadata only — env_vars + setup_script
/// are hub-visible; secret_refs + network policy round-trip but are not applied
/// at spawn yet (E3/E4), so the editor omits them with a note.
class EnvProfilesScreen extends ConsumerStatefulWidget {
  const EnvProfilesScreen({super.key});

  @override
  ConsumerState<EnvProfilesScreen> createState() => _EnvProfilesScreenState();
}

class _EnvProfilesScreenState extends ConsumerState<EnvProfilesScreen> {
  List<Map<String, dynamic>> _rows = const [];
  bool _loading = false;
  bool _hubMissing = false;
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
      _hubMissing = false;
    });
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _hubMissing = true;
      });
      return;
    }
    try {
      final rows = await client.envProfiles.listEnvProfiles();
      if (!mounted) return;
      setState(() {
        _rows = rows;
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

  Future<void> _edit([Map<String, dynamic>? row]) async {
    final saved = await Navigator.of(context).push<bool>(MaterialPageRoute(
      builder: (_) => _EnvProfileEditScreen(existing: row),
    ));
    if (saved == true) await _load();
  }

  Future<void> _delete(Map<String, dynamic> row) async {
    final id = (row['id'] ?? '').toString();
    final name = (row['name'] ?? id).toString();
    final l10n = AppLocalizations.of(context)!;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.envProfileDeleteTitle(name)),
        content: Text(l10n.envProfileDeleteBody),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: Text(l10n.buttonCancel),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: DesignColors.error),
            onPressed: () => Navigator.pop(ctx, true),
            child: Text(l10n.buttonDelete),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.envProfiles.deleteEnvProfile(id);
      await _load();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('$e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return Scaffold(
      appBar: AppBar(
        title: Text(
          l10n.envProfilesTitle,
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
        onPressed: () => _edit(),
        icon: const Icon(Icons.add),
        label: Text(l10n.envProfilesNew),
      ),
      body: _buildBody(l10n),
    );
  }

  Widget _buildBody(AppLocalizations l10n) {
    if (_loading && _rows.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_hubMissing) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            l10n.hubNotConfigured,
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(color: DesignColors.error),
          ),
        ),
      );
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
        child: Text(l10n.envProfilesEmpty,
            style: GoogleFonts.spaceGrotesk(color: DesignColors.textMuted)),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, Spacing.s24 * 4),
        itemCount: _rows.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) {
          final row = _rows[i];
          final vars = (row['env_vars'] as Map?) ?? const {};
          final desc = (row['description'] ?? '').toString();
          return Card(
            child: ListTile(
              title: Text((row['name'] ?? '?').toString()),
              subtitle: Text(
                desc.isNotEmpty
                    ? desc
                    : l10n.envProfileVarCount('${vars.length}'),
              ),
              onTap: () => _edit(row),
              trailing: IconButton(
                icon: const Icon(Icons.delete_outline),
                onPressed: () => _delete(row),
              ),
            ),
          );
        },
      ),
    );
  }
}

/// One key=value row in the editor — owns its two controllers so add/remove
/// keeps focus and cursor position stable.
class _VarRow {
  final TextEditingController key;
  final TextEditingController value;
  _VarRow(String k, String v)
      : key = TextEditingController(text: k),
        value = TextEditingController(text: v);
  void dispose() {
    key.dispose();
    value.dispose();
  }
}

class _EnvProfileEditScreen extends ConsumerStatefulWidget {
  final Map<String, dynamic>? existing;
  const _EnvProfileEditScreen({this.existing});

  @override
  ConsumerState<_EnvProfileEditScreen> createState() =>
      _EnvProfileEditScreenState();
}

class _EnvProfileEditScreenState extends ConsumerState<_EnvProfileEditScreen> {
  late final TextEditingController _nameCtl;
  late final TextEditingController _descCtl;
  late final TextEditingController _setupCtl;
  late List<_VarRow> _vars;
  String _failurePolicy = 'fail';
  String _netMode = 'open';
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    final e = widget.existing;
    _nameCtl = TextEditingController(text: (e?['name'] ?? '').toString());
    _descCtl = TextEditingController(text: (e?['description'] ?? '').toString());
    _setupCtl =
        TextEditingController(text: (e?['setup_script'] ?? '').toString());
    _failurePolicy = (e?['setup_failure_policy'] ?? 'fail').toString();
    final net = (e?['network_policy'] as Map?) ?? const {};
    _netMode = (net['mode'] ?? 'open').toString();
    final envVars = (e?['env_vars'] as Map?) ?? const {};
    _vars = envVars.entries
        .map((kv) => _VarRow(kv.key.toString(), kv.value.toString()))
        .toList();
  }

  @override
  void dispose() {
    _nameCtl.dispose();
    _descCtl.dispose();
    _setupCtl.dispose();
    for (final v in _vars) {
      v.dispose();
    }
    super.dispose();
  }

  Future<void> _save() async {
    final l10n = AppLocalizations.of(context)!;
    final name = _nameCtl.text.trim();
    if (name.isEmpty) {
      setState(() => _error = l10n.envProfileNameRequired);
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _busy = false;
        _error = l10n.hubNotConfigured;
      });
      return;
    }
    final envVars = <String, String>{};
    for (final v in _vars) {
      final k = v.key.text.trim();
      if (k.isNotEmpty) envVars[k] = v.value.text;
    }
    final body = <String, dynamic>{
      'name': name,
      'description': _descCtl.text,
      'setup_script': _setupCtl.text,
      'setup_failure_policy': _failurePolicy,
      'env_vars': envVars,
      'network_policy': {'mode': _netMode},
    };
    try {
      final id = widget.existing?['id']?.toString();
      if (id == null) {
        await client.envProfiles.createEnvProfile(body);
      } else {
        await client.envProfiles.updateEnvProfile(id, body);
      }
      if (!mounted) return;
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isNew = widget.existing == null;
    return Scaffold(
      appBar: AppBar(
        title: Text(
          isNew ? l10n.envProfileNewTitle : l10n.envProfileEditTitle,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
      ),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, Spacing.s24 * 4),
        children: [
          TextField(
            controller: _nameCtl,
            decoration: InputDecoration(
              labelText: l10n.envProfileName,
              border: const OutlineInputBorder(),
            ),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _descCtl,
            decoration: InputDecoration(
              labelText: l10n.envProfileDescription,
              border: const OutlineInputBorder(),
            ),
          ),
          const SizedBox(height: 16),
          Row(
            children: [
              Expanded(
                child: Text(l10n.envProfileEnvVars,
                    style: const TextStyle(fontWeight: FontWeight.w600)),
              ),
              TextButton.icon(
                onPressed: () =>
                    setState(() => _vars.add(_VarRow('', ''))),
                icon: const Icon(Icons.add, size: 18),
                label: Text(l10n.envProfileAddVar),
              ),
            ],
          ),
          for (int i = 0; i < _vars.length; i++)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: _vars[i].key,
                      decoration: InputDecoration(
                        labelText: l10n.envProfileKey,
                        border: const OutlineInputBorder(),
                        isDense: true,
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: TextField(
                      controller: _vars[i].value,
                      decoration: InputDecoration(
                        labelText: l10n.envProfileValue,
                        border: const OutlineInputBorder(),
                        isDense: true,
                      ),
                    ),
                  ),
                  IconButton(
                    icon: const Icon(Icons.remove_circle_outline),
                    onPressed: () => setState(() {
                      _vars[i].dispose();
                      _vars.removeAt(i);
                    }),
                  ),
                ],
              ),
            ),
          const SizedBox(height: 8),
          TextField(
            controller: _setupCtl,
            minLines: 3,
            maxLines: 8,
            decoration: InputDecoration(
              labelText: l10n.envProfileSetupScript,
              hintText: l10n.envProfileSetupHint,
              border: const OutlineInputBorder(),
            ),
          ),
          const SizedBox(height: 12),
          DropdownButtonFormField<String>(
            initialValue: _failurePolicy,
            decoration: InputDecoration(
              labelText: l10n.envProfileFailurePolicy,
              border: const OutlineInputBorder(),
            ),
            items: [
              DropdownMenuItem(
                  value: 'fail', child: Text(l10n.envProfilePolicyFail)),
              DropdownMenuItem(
                  value: 'continue',
                  child: Text(l10n.envProfilePolicyContinue)),
            ],
            onChanged: (v) =>
                setState(() => _failurePolicy = v ?? 'fail'),
          ),
          const SizedBox(height: 12),
          DropdownButtonFormField<String>(
            initialValue: _netMode,
            decoration: InputDecoration(
              labelText: l10n.envProfileNetwork,
              border: const OutlineInputBorder(),
            ),
            items: [
              DropdownMenuItem(
                  value: 'open', child: Text(l10n.envProfileNetOpen)),
              DropdownMenuItem(
                  value: 'allowlist',
                  child: Text(l10n.envProfileNetAllowlist)),
              DropdownMenuItem(
                  value: 'offline', child: Text(l10n.envProfileNetOffline)),
            ],
            onChanged: (v) => setState(() => _netMode = v ?? 'open'),
          ),
          const SizedBox(height: 12),
          Text(l10n.envProfileSecretNote,
              style: TextStyle(
                  fontSize: 12, color: DesignColors.textMuted)),
          if (_error != null) ...[
            const SizedBox(height: 12),
            Text(_error!, style: TextStyle(color: DesignColors.error)),
          ],
          const SizedBox(height: 16),
          FilledButton(
            onPressed: _busy ? null : _save,
            child: Text(l10n.envProfileSave),
          ),
        ],
      ),
    );
  }
}
