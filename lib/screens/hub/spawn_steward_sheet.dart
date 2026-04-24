import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Team-scoped bootstrap sheet that spawns the one and only steward. This
/// is intentionally different from [showSpawnAgentSheet]:
///
///   - the handle is fixed to `steward` (reserved; see `_stewardPresent`
///     in hub_screen.dart and the seed-demo check in the audit log),
///   - the spec comes from the shipped `agents/steward.v1` template so
///     operators don't hand-roll principal / journal wiring,
///   - there is no preset bar, no kind field, no free-form YAML — the
///     steward is a team singleton, not a fleet member.
///
/// Surfaced from the Projects AppBar "No steward" chip. A ready steward
/// is implied by the chip's live-colour state; tapping that variant opens
/// `#hub-meta` instead of this sheet.
Future<void> showSpawnStewardSheet(
  BuildContext context, {
  required List<Map<String, dynamic>> hosts,
}) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    builder: (_) => _SpawnStewardSheet(hosts: hosts),
  );
}

class _SpawnStewardSheet extends ConsumerStatefulWidget {
  final List<Map<String, dynamic>> hosts;
  const _SpawnStewardSheet({required this.hosts});

  @override
  ConsumerState<_SpawnStewardSheet> createState() => _SpawnStewardSheetState();
}

class _SpawnStewardSheetState extends ConsumerState<_SpawnStewardSheet> {
  bool _busy = false;
  String? _error;
  String? _hostId;

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
  }

  Future<void> _spawn() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final client = ref.read(hubProvider.notifier).client;
      if (client == null) throw StateError('Hub not configured');
      final yaml = await client.getTemplate('agents', 'steward.v1');
      final res = await client.spawnAgent(
        childHandle: 'steward',
        kind: 'claude-code',
        spawnSpecYaml: yaml,
        hostId: _hostId,
      );
      if (!mounted) return;
      final status = res['status']?.toString() ?? '';
      final msg = status == 'pending_approval'
          ? 'Spawn request sent — awaiting approval.'
          : 'Steward spawned.';
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
    final scheme = Theme.of(context).colorScheme;
    final bottomInset = MediaQuery.of(context).viewInsets.bottom;
    final canSpawn = widget.hosts.isNotEmpty && !_busy;
    return SafeArea(
      child: Padding(
        padding: EdgeInsets.only(bottom: bottomInset),
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
                      'Spawn the team steward',
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 18,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                  IconButton(
                    icon: const Icon(Icons.close),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              Text(
                'The steward is the one agent that speaks for the team in '
                '#hub-meta. It takes delegations from you, files attention '
                'items, and hands work out to project agents.',
                style: GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.4),
              ),
              const SizedBox(height: 10),
              Text(
                'This spawns a team-scoped agent with handle "steward" '
                'using the shipped agents/steward.v1 template. You only '
                'need one per team.',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  height: 1.35,
                  color: DesignColors.textMuted,
                ),
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
              ],
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(_error!,
                    style: TextStyle(color: scheme.error)),
              ],
              const SizedBox(height: 20),
              FilledButton.icon(
                onPressed: canSpawn ? _spawn : null,
                icon: _busy
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.play_arrow),
                label: Text(_busy ? 'Spawning…' : 'Spawn Steward'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
