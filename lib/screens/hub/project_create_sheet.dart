import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';

/// Minimal project creation sheet. Pops `true` on success so the caller
/// can refresh the project list. `docs_root` and `config_yaml` are
/// optional — the hub treats them as nullable columns.
class ProjectCreateSheet extends ConsumerStatefulWidget {
  const ProjectCreateSheet({super.key});

  @override
  ConsumerState<ProjectCreateSheet> createState() =>
      _ProjectCreateSheetState();
}

class _ProjectCreateSheetState extends ConsumerState<ProjectCreateSheet> {
  final _name = TextEditingController();
  final _docsRoot = TextEditingController();
  final _configYaml = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _name.dispose();
    _docsRoot.dispose();
    _configYaml.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    if (_name.text.trim().isEmpty) {
      setState(() => _error = 'Name required');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await client.createProject(
        name: _name.text.trim(),
        docsRoot: _docsRoot.text.trim().isEmpty ? null : _docsRoot.text.trim(),
        configYaml:
            _configYaml.text.trim().isEmpty ? null : _configYaml.text.trim(),
      );
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      setState(() {
        _busy = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final insets = MediaQuery.of(context).viewInsets;
    return Padding(
      padding: EdgeInsets.only(bottom: insets.bottom),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text('New project',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 18, fontWeight: FontWeight.w700)),
              const SizedBox(height: 16),
              TextField(
                controller: _name,
                autofocus: true,
                decoration: const InputDecoration(
                  labelText: 'Name',
                  border: OutlineInputBorder(),
                ),
                textInputAction: TextInputAction.next,
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _docsRoot,
                decoration: const InputDecoration(
                  labelText: 'Docs root (optional, e.g. docs/)',
                  border: OutlineInputBorder(),
                ),
                textInputAction: TextInputAction.next,
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _configYaml,
                maxLines: 4,
                decoration: const InputDecoration(
                  labelText: 'Config YAML (optional)',
                  border: OutlineInputBorder(),
                ),
              ),
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(_error!,
                    style: GoogleFonts.jetBrainsMono(
                        fontSize: 12, color: Colors.red)),
              ],
              const SizedBox(height: 20),
              Row(
                children: [
                  TextButton(
                    onPressed: _busy ? null : () => Navigator.of(context).pop(),
                    child: const Text('Cancel'),
                  ),
                  const Spacer(),
                  FilledButton.icon(
                    onPressed: _busy ? null : _submit,
                    icon: _busy
                        ? const SizedBox(
                            width: 14,
                            height: 14,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.check, size: 16),
                    label: const Text('Create'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}
