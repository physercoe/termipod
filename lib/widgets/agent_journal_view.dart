// AgentJournalView — the Journal read + append body for an agent.
//
// Extracted from the project-agent sheet (P2 — docs/plans/agent-
// transcript-debug-and-header-parity.md) so the SAME body renders behind
// the shared SessionHeader's `View ▾` switcher on both the project sheet
// and SessionChatScreen — one copy, no fork. Self-contained: a host only
// passes the agent id. The journal is load-on-demand (a manual Load tap)
// — same as the original, so opening the view doesn't fire a fetch the
// user didn't ask for.
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../theme/design_colors.dart';

class AgentJournalView extends ConsumerStatefulWidget {
  final String agentId;
  const AgentJournalView({super.key, required this.agentId});

  @override
  ConsumerState<AgentJournalView> createState() => _AgentJournalViewState();
}

class _AgentJournalViewState extends ConsumerState<AgentJournalView> {
  bool _busy = false;
  String? _error;
  String? _journal;
  bool _journalLoaded = false;
  final _noteCtl = TextEditingController();

  @override
  void dispose() {
    _noteCtl.dispose();
    super.dispose();
  }

  Future<void> _loadJournal() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final out = await client.readAgentJournal(widget.agentId);
      if (!mounted) return;
      setState(() {
        _journal = out;
        _journalLoaded = true;
      });
    } catch (e) {
      if (mounted) setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _appendJournal() async {
    final entry = _noteCtl.text.trim();
    if (entry.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await client.appendAgentJournal(widget.agentId, entry);
      if (!mounted) return;
      _noteCtl.clear();
    } catch (e) {
      if (mounted) setState(() => _error = e.toString());
      return;
    } finally {
      if (mounted) setState(() => _busy = false);
    }
    await _loadJournal();
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
      children: [
        if (_error != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text(_error!,
                style: const TextStyle(color: DesignColors.error)),
          ),
        _SectionHeader(
          title: 'Journal',
          trailing: TextButton.icon(
            onPressed: _busy ? null : _loadJournal,
            icon: Icon(_journalLoaded ? Icons.refresh : Icons.download,
                size: 18),
            label: Text(_journalLoaded ? 'Refresh' : 'Load'),
          ),
        ),
        if (_journalLoaded)
          Container(
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight,
              borderRadius: BorderRadius.circular(8),
              border: Border.all(
                color: isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight,
              ),
            ),
            child: SelectableText(
              (_journal ?? '').isEmpty
                  ? '(empty — the agent hasn\'t written a journal yet)'
                  : _journal!,
              style: GoogleFonts.jetBrainsMono(fontSize: 11),
            ),
          ),
        const SizedBox(height: 8),
        TextField(
          controller: _noteCtl,
          maxLines: 3,
          decoration: InputDecoration(
            hintText: 'Append a note to the journal…',
            border: const OutlineInputBorder(),
            suffixIcon: IconButton(
              icon: const Icon(Icons.send),
              tooltip: 'Append',
              onPressed: _busy ? null : _appendJournal,
            ),
          ),
        ),
      ],
    );
  }
}

/// Local section header (title + optional trailing action). Kept private
/// to this view so the extraction is self-contained.
class _SectionHeader extends StatelessWidget {
  final String title;
  final Widget? trailing;
  const _SectionHeader({required this.title, this.trailing});
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 6, top: 4),
      child: Row(
        children: [
          Text(title,
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 13, fontWeight: FontWeight.w700)),
          const Spacer(),
          if (trailing != null) trailing!,
        ],
      ),
    );
  }
}
