import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';

/// Bottom sheet for creating a cron schedule. Pops `true` on success so the
/// caller reloads the schedule list.
class ScheduleCreateSheet extends ConsumerStatefulWidget {
  const ScheduleCreateSheet({super.key});

  @override
  ConsumerState<ScheduleCreateSheet> createState() =>
      _ScheduleCreateSheetState();
}

class _ScheduleCreateSheetState extends ConsumerState<ScheduleCreateSheet> {
  final _name = TextEditingController();
  final _cron = TextEditingController();
  final _handle = TextEditingController();
  final _yaml = TextEditingController();
  String _kind = 'claude_code';
  bool _busy = false;
  String? _error;

  static const _kinds = ['claude_code', 'kimi_code', 'other'];

  @override
  void dispose() {
    _name.dispose();
    _cron.dispose();
    _handle.dispose();
    _yaml.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    if (_name.text.trim().isEmpty) {
      setState(() => _error = 'Name required');
      return;
    }
    if (_cron.text.trim().isEmpty) {
      setState(() => _error = 'Cron expression required');
      return;
    }
    if (_handle.text.trim().isEmpty) {
      setState(() => _error = 'Child handle required');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await client.createSchedule(
        name: _name.text.trim(),
        cronExpr: _cron.text.trim(),
        spawn: {
          'child_handle': _handle.text.trim(),
          'kind': _kind,
          'spawn_spec_yaml': _yaml.text,
        },
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
              Text('New schedule',
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
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _cron,
                style: GoogleFonts.jetBrainsMono(fontSize: 13),
                decoration: const InputDecoration(
                  labelText: 'Cron expression',
                  hintText: '0 9 * * *',
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _handle,
                decoration: const InputDecoration(
                  labelText: 'Child handle',
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                value: _kind,
                decoration: const InputDecoration(
                  labelText: 'Kind',
                  border: OutlineInputBorder(),
                ),
                items: [
                  for (final k in _kinds)
                    DropdownMenuItem(value: k, child: Text(k)),
                ],
                onChanged: (v) => setState(() => _kind = v ?? 'claude_code'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _yaml,
                maxLines: 4,
                style: GoogleFonts.jetBrainsMono(fontSize: 12),
                decoration: const InputDecoration(
                  labelText: 'Spawn spec (optional)',
                  hintText: 'YAML spawn spec',
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
                    onPressed:
                        _busy ? null : () => Navigator.of(context).pop(),
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
