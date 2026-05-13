// ADR-025 W7 — host-picker sheet for project steward materialization.
//
// Distinct from the team-scoped showSpawnStewardSheet (which lets the
// principal pick a template + handle for the team singleton). Here
// the project is pre-determined; the sheet asks for host + permission
// mode and calls /v1/teams/{team}/projects/{project}/steward/ensure
// (W3 endpoint). The hub idempotency-coalesces a second call, so a
// double-tap doesn't double-spawn.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../theme/design_colors.dart';

/// Returns the spawned/found agent id on success, or null if the user
/// dismissed the sheet. Callers typically refresh the agents list on
/// non-null return so the project Agents tab populates.
Future<String?> showSpawnProjectStewardSheet(
  BuildContext context, {
  required String projectId,
  String? suggestedHostId,
}) {
  return showModalBottomSheet<String?>(
    context: context,
    isScrollControlled: true,
    builder: (_) => _SpawnProjectStewardSheet(
      projectId: projectId,
      suggestedHostId: suggestedHostId,
    ),
  );
}

class _SpawnProjectStewardSheet extends ConsumerStatefulWidget {
  final String projectId;
  final String? suggestedHostId;
  const _SpawnProjectStewardSheet({
    required this.projectId,
    this.suggestedHostId,
  });

  @override
  ConsumerState<_SpawnProjectStewardSheet> createState() =>
      _SpawnProjectStewardSheetState();
}

class _SpawnProjectStewardSheetState
    extends ConsumerState<_SpawnProjectStewardSheet> {
  bool _busy = false;
  String? _error;
  String? _hostId;
  String _permissionMode = 'skip';

  @override
  void initState() {
    super.initState();
    final hosts = ref.read(hubProvider).value?.hosts ?? const [];
    // Default ladder per ADR-025 D4: suggested > first online > first.
    if (widget.suggestedHostId != null &&
        widget.suggestedHostId!.isNotEmpty &&
        hosts.any((h) => (h['id'] ?? '').toString() == widget.suggestedHostId)) {
      _hostId = widget.suggestedHostId;
    } else {
      final online = hosts.where(
        (h) => (h['status']?.toString() ?? '') == 'online',
      );
      if (online.isNotEmpty) {
        _hostId = (online.first['id'] ?? '').toString();
      } else if (hosts.isNotEmpty) {
        _hostId = (hosts.first['id'] ?? '').toString();
      }
    }
  }

  Future<void> _spawn() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() => _error = 'Hub client not ready.');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final out = await client.ensureProjectSteward(
        projectId: widget.projectId,
        hostId: _hostId,
        permissionMode: _permissionMode,
      );
      // Trigger a fresh hub poll so the Agents tab picks up the new
      // row immediately. Reading the snapshot after this returns is
      // the simplest sync point.
      await ref.read(hubProvider.notifier).refreshAll();
      if (!mounted) return;
      Navigator.of(context).pop((out['agent_id'] ?? '').toString());
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        _error = e.toString();
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final hosts = ref.watch(hubProvider).value?.hosts ?? const [];
    final mq = MediaQuery.of(context);

    return Padding(
      padding: EdgeInsets.only(bottom: mq.viewInsets.bottom),
      child: SafeArea(
        top: false,
        child: SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(20, 16, 20, 20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Container(
                    width: 32, height: 4,
                    decoration: BoxDecoration(
                      color: isDark
                          ? DesignColors.borderDark
                          : DesignColors.borderLight,
                      borderRadius: BorderRadius.circular(2),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              Text('Spawn project steward',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 16, fontWeight: FontWeight.w700)),
              const SizedBox(height: 6),
              Text(
                'Materializes the @steward for this project. Operates with '
                'director-granted authority over project-scoped spawns.',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
              const SizedBox(height: 16),
              Text('Host',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 12, fontWeight: FontWeight.w600)),
              const SizedBox(height: 6),
              if (hosts.isEmpty)
                Text(
                  'No hosts registered. Install a host-runner first.',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    color: Colors.redAccent,
                  ),
                )
              else
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: hosts.map((h) {
                    final id = (h['id'] ?? '').toString();
                    final name = (h['name'] ?? id).toString();
                    final status = (h['status'] ?? '').toString();
                    final online = status == 'online';
                    return ChoiceChip(
                      selected: _hostId == id,
                      onSelected: _busy
                          ? null
                          : (sel) => setState(
                              () => _hostId = sel ? id : null),
                      label: Text(online ? name : '$name · $status'),
                    );
                  }).toList(),
                ),
              const SizedBox(height: 16),
              Text('Permission mode',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 12, fontWeight: FontWeight.w600)),
              const SizedBox(height: 6),
              Wrap(
                spacing: 8,
                children: [
                  ChoiceChip(
                    selected: _permissionMode == 'skip',
                    onSelected: _busy
                        ? null
                        : (_) => setState(() => _permissionMode = 'skip'),
                    label: const Text('skip'),
                  ),
                  ChoiceChip(
                    selected: _permissionMode == 'prompt',
                    onSelected: _busy
                        ? null
                        : (_) =>
                            setState(() => _permissionMode = 'prompt'),
                    label: const Text('prompt'),
                  ),
                ],
              ),
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(_error!,
                    style: const TextStyle(color: Colors.redAccent)),
              ],
              const SizedBox(height: 20),
              FilledButton.icon(
                onPressed: (_busy || _hostId == null || hosts.isEmpty)
                    ? null
                    : _spawn,
                icon: _busy
                    ? const SizedBox(
                        width: 16, height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2))
                    : const Icon(Icons.bolt_outlined),
                label: Text(_busy ? 'Spawning…' : 'Spawn steward'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
