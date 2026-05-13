import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/hub_client.dart';
import '../../theme/design_colors.dart';

/// Owner-only editor for hub-wide governance files. MVP exposes the
/// operation-scope manifest (`roles.yaml`); the AppBar's "Reset to
/// default" action mirrors the per-template editor pattern in
/// `templates_screen.dart`.
///
/// Two-pane shape was rejected in favour of a single YAML editor with
/// a Save / Reset toolbar so this screen reads the same as the team
/// policy editor — the owner audience for both is identical and we
/// keep the cognitive shape consistent. Save validates server-side
/// (yaml.Unmarshal + non-empty roles map) and hot-reloads the manifest
/// without restarting the hub; failure rolls back to the prior
/// `roles.yaml.bak` snapshot.
class HubRolesConfigScreen extends ConsumerStatefulWidget {
  const HubRolesConfigScreen({super.key});

  @override
  ConsumerState<HubRolesConfigScreen> createState() =>
      _HubRolesConfigScreenState();
}

class _HubRolesConfigScreenState extends ConsumerState<HubRolesConfigScreen> {
  final TextEditingController _ctrl = TextEditingController();
  bool _loading = true;
  bool _saving = false;
  bool _dirty = false;
  String? _error;
  String _savedBody = '';

  @override
  void initState() {
    super.initState();
    _ctrl.addListener(_onChanged);
    _load();
  }

  @override
  void dispose() {
    _ctrl.removeListener(_onChanged);
    _ctrl.dispose();
    super.dispose();
  }

  void _onChanged() {
    final isDirty = _ctrl.text != _savedBody;
    if (isDirty != _dirty) {
      setState(() => _dirty = isDirty);
    }
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
      final body = await client.getHubRolesConfig();
      if (!mounted) return;
      setState(() {
        _savedBody = body;
        _ctrl.text = body;
        _dirty = false;
        _loading = false;
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = e.status == 403
            ? 'Hub config requires an owner-kind token.'
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

  Future<void> _save() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _saving = true);
    try {
      final canonical = await client.putHubRolesConfig(_ctrl.text);
      if (!mounted) return;
      setState(() {
        _savedBody = canonical;
        _ctrl.text = canonical;
        _dirty = false;
        _saving = false;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Saved + reloaded'),
          duration: Duration(seconds: 2),
        ),
      );
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Save failed (${e.status}): ${e.message}')),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Save failed: $e')),
      );
    }
  }

  Future<void> _resetToDefault() async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Reset to default?'),
        content: const Text(
          'Removes the on-disk roles.yaml override and reloads the '
          'embedded built-in. The MCP role gate falls back to the '
          'manifest the hub binary shipped with.\n\n'
          'Any local edits are discarded.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton.tonal(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Reset'),
          ),
        ],
      ),
    );
    if (confirm != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _saving = true);
    try {
      final embedded = await client.resetHubRolesConfig();
      if (!mounted) return;
      setState(() {
        _savedBody = embedded;
        _ctrl.text = embedded;
        _dirty = false;
        _saving = false;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Reset to built-in default'),
          duration: Duration(seconds: 2),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Reset failed: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Scaffold(
      appBar: AppBar(
        title: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'Hub config',
              style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
            ),
            Text(
              'roles.yaml — operation-scope manifest',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                color: muted,
              ),
            ),
          ],
        ),
        actions: [
          IconButton(
            tooltip: 'Reset to default',
            icon: const Icon(Icons.settings_backup_restore),
            onPressed: (_loading || _saving) ? null : _resetToDefault,
          ),
          IconButton(
            tooltip: _dirty ? 'Save' : 'No changes',
            icon: _saving
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : Icon(
                    Icons.save_outlined,
                    color: _dirty ? DesignColors.primary : muted,
                  ),
            onPressed: (_loading || _saving || !_dirty) ? null : _save,
          ),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Text(
                      _error!,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 12,
                        color: DesignColors.error,
                      ),
                      textAlign: TextAlign.center,
                    ),
                  ),
                )
              : Padding(
                  padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
                  child: TextField(
                    controller: _ctrl,
                    maxLines: null,
                    expands: true,
                    keyboardType: TextInputType.multiline,
                    textAlignVertical: TextAlignVertical.top,
                    style: GoogleFonts.jetBrainsMono(fontSize: 12),
                    decoration: InputDecoration(
                      hintText: 'roles.yaml…',
                      hintStyle: GoogleFonts.jetBrainsMono(
                        fontSize: 12,
                        color: muted,
                      ),
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(8),
                      ),
                      contentPadding: const EdgeInsets.all(12),
                    ),
                    inputFormatters: const [],
                    smartDashesType: SmartDashesType.disabled,
                    smartQuotesType: SmartQuotesType.disabled,
                  ),
                ),
    );
  }
}
