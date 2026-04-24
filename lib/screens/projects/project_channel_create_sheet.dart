import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Tiny form for creating a project-scope channel. Returns the created
/// channel map on success so the caller can append it to its in-memory
/// list without a full refetch.
class ProjectChannelCreateSheet extends ConsumerStatefulWidget {
  final String projectId;
  const ProjectChannelCreateSheet({super.key, required this.projectId});

  @override
  ConsumerState<ProjectChannelCreateSheet> createState() =>
      _ProjectChannelCreateSheetState();
}

class _ProjectChannelCreateSheetState
    extends ConsumerState<ProjectChannelCreateSheet> {
  final _name = TextEditingController();
  bool _submitting = false;

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final name = _name.text.trim();
    if (name.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _submitting = true);
    try {
      final created = await client.createChannel(widget.projectId, name);
      if (!mounted) return;
      Navigator.of(context).pop(created);
    } catch (e) {
      if (!mounted) return;
      setState(() => _submitting = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Create failed: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.fromLTRB(
        16,
        12,
        16,
        16 + MediaQuery.of(context).viewInsets.bottom,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Center(
            child: Container(
              width: 36,
              height: 4,
              margin: const EdgeInsets.only(bottom: 12),
              decoration: BoxDecoration(
                color: DesignColors.borderDark,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          Text(
            'New channel',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 18,
              fontWeight: FontWeight.w700,
            ),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _name,
            autofocus: true,
            enabled: !_submitting,
            style: GoogleFonts.jetBrainsMono(fontSize: 14),
            decoration: const InputDecoration(
              border: OutlineInputBorder(),
              isDense: true,
              prefixText: '#',
              hintText: 'design-review',
            ),
            onSubmitted: (_) => _submit(),
          ),
          const SizedBox(height: 4),
          Text(
            'Channels are scoped to this project. Events posted here '
            'stay off the team-wide feed.',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              color: DesignColors.textMuted,
            ),
          ),
          const SizedBox(height: 16),
          FilledButton(
            onPressed: _submitting ? null : _submit,
            child: _submitting
                ? const SizedBox(
                    width: 14,
                    height: 14,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Text('Create'),
          ),
        ],
      ),
    );
  }
}
