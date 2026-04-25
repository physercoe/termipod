// AgentCompose — producer='user' input bar for the AgentFeed
// (blueprint P2.2). Sends text + cancel today; approval and attach are
// scaffolded so pending-request UI can hook them up without adding a
// whole new widget later.
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../services/hub/hub_client.dart';
import '../theme/design_colors.dart';

/// Sits under AgentFeed and routes text/cancel inputs to the hub's
/// /agents/{id}/input endpoint. The hub persists them as producer='user'
/// agent_events; host-runner's InputRouter then delivers them to the
/// running driver over its native transport (stream-json stdin, tmux
/// send-keys, ACP session/prompt).
///
/// W-UI-4: slashCommands / mentions are sourced from the active
/// session.init payload by AgentFeed and surface as a suggestion chip
/// strip when the user types a `/` or `@` token. Empty lists silently
/// disable the matching prefix — drivers that don't surface those
/// fields just don't get the picker.
class AgentCompose extends ConsumerStatefulWidget {
  final String agentId;
  final List<String> slashCommands;
  final List<String> mentions;
  const AgentCompose({
    super.key,
    required this.agentId,
    this.slashCommands = const [],
    this.mentions = const [],
  });

  @override
  ConsumerState<AgentCompose> createState() => _AgentComposeState();
}

class _AgentComposeState extends ConsumerState<AgentCompose> {
  final TextEditingController _ctrl = TextEditingController();
  final FocusNode _focus = FocusNode();
  bool _sending = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    // Re-render on every keystroke so the suggestion strip can react to
    // `/` and `@` prefixes at the cursor. The TextField itself doesn't
    // need a setState; the strip computes from controller.value.
    _ctrl.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  // Find the token at the current cursor (last whitespace-delimited
  // chunk that ends at selection.end). Returns null when the cursor is
  // mid-text or there's no leading prefix character.
  _PrefixMatch? _activeMatch() {
    final value = _ctrl.value;
    final sel = value.selection;
    if (!sel.isValid || !sel.isCollapsed) return null;
    final upto = value.text.substring(0, sel.end);
    final start = upto.lastIndexOf(RegExp(r'\s'));
    final tokenStart = start + 1;
    final token = upto.substring(tokenStart);
    if (token.isEmpty) return null;
    final lead = token[0];
    if (lead != '/' && lead != '@') return null;
    final query = token.substring(1).toLowerCase();
    final pool = lead == '/' ? widget.slashCommands : widget.mentions;
    if (pool.isEmpty) return null;
    final matches = <String>[];
    for (final c in pool) {
      // Slash commands in claude-code arrive as "/help" — strip the
      // leading slash before comparing so the user can type "/he" and
      // still match "/help" without seeing "//help" suggestions.
      final norm = lead == '/' && c.startsWith('/') ? c.substring(1) : c;
      if (query.isEmpty || norm.toLowerCase().startsWith(query)) {
        matches.add(norm);
      }
      if (matches.length >= 8) break;
    }
    if (matches.isEmpty) return null;
    return _PrefixMatch(
      lead: lead,
      tokenStart: tokenStart,
      tokenEnd: sel.end,
      suggestions: matches,
    );
  }

  void _applySuggestion(_PrefixMatch m, String suggestion) {
    final text = _ctrl.text;
    final replacement = '${m.lead}$suggestion ';
    final next = text.substring(0, m.tokenStart) +
        replacement +
        text.substring(m.tokenEnd);
    final cursor = m.tokenStart + replacement.length;
    _ctrl.value = TextEditingValue(
      text: next,
      selection: TextSelection.collapsed(offset: cursor),
    );
    _focus.requestFocus();
  }

  Future<void> _send() async {
    final body = _ctrl.text.trimRight();
    if (body.isEmpty || _sending) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() => _error = 'Not connected');
      return;
    }
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      await client.postAgentInput(widget.agentId, kind: 'text', body: body);
      if (!mounted) return;
      _ctrl.clear();
      // Keep focus so the user can fire a follow-up without another tap.
      _focus.requestFocus();
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Send failed (${e.status})');
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Send failed: $e');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  Future<void> _cancel() async {
    if (_sending) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() => _error = 'Not connected');
      return;
    }
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      // Reason is optional; keep it human so the feed shows why.
      await client.postAgentInput(widget.agentId,
          kind: 'cancel', reason: 'user requested cancel');
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Cancel failed (${e.status})');
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Cancel failed: $e');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;

    final match = _activeMatch();
    return Container(
      decoration: BoxDecoration(
        color: bg,
        border: Border(top: BorderSide(color: border)),
      ),
      padding: const EdgeInsets.fromLTRB(8, 6, 8, 8),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (match != null)
            _SuggestionStrip(
              match: match,
              onTap: (s) => _applySuggestion(match, s),
              border: border,
              muted: muted,
            ),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.only(bottom: 4, left: 4),
              child: Text(
                _error!,
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 11, color: DesignColors.error),
              ),
            ),
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              IconButton(
                tooltip: 'Send cancel (Ctrl+C) to the agent',
                onPressed: _sending ? null : _cancel,
                icon: Icon(Icons.stop_circle_outlined,
                    size: 22, color: muted),
                padding: EdgeInsets.zero,
                visualDensity: VisualDensity.compact,
                constraints:
                    const BoxConstraints(minWidth: 36, minHeight: 36),
              ),
              Expanded(
                child: TextField(
                  controller: _ctrl,
                  focusNode: _focus,
                  enabled: !_sending,
                  minLines: 1,
                  maxLines: 6,
                  keyboardType: TextInputType.multiline,
                  textInputAction: TextInputAction.newline,
                  style: GoogleFonts.jetBrainsMono(fontSize: 12),
                  decoration: InputDecoration(
                    isDense: true,
                    hintText: 'Send to agent…',
                    hintStyle: GoogleFonts.jetBrainsMono(
                        fontSize: 12, color: muted),
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(6),
                      borderSide: BorderSide(color: border),
                    ),
                    enabledBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(6),
                      borderSide: BorderSide(color: border),
                    ),
                    contentPadding: const EdgeInsets.symmetric(
                        horizontal: 10, vertical: 8),
                  ),
                ),
              ),
              const SizedBox(width: 4),
              ValueListenableBuilder<TextEditingValue>(
                valueListenable: _ctrl,
                builder: (_, value, _) {
                  final empty = value.text.trim().isEmpty;
                  return IconButton(
                    tooltip: 'Send as text input',
                    onPressed: (_sending || empty) ? null : _send,
                    icon: _sending
                        ? const SizedBox(
                            width: 18,
                            height: 18,
                            child:
                                CircularProgressIndicator(strokeWidth: 2),
                          )
                        : Icon(Icons.send,
                            size: 20,
                            color: empty
                                ? muted
                                : DesignColors.primary),
                    padding: EdgeInsets.zero,
                    visualDensity: VisualDensity.compact,
                    constraints: const BoxConstraints(
                        minWidth: 40, minHeight: 40),
                  );
                },
              ),
            ],
          ),
        ],
      ),
    );
  }
}

/// Resolved prefix-trigger state — what's at the cursor + what to offer.
class _PrefixMatch {
  final String lead; // '/' or '@'
  final int tokenStart;
  final int tokenEnd;
  final List<String> suggestions;
  const _PrefixMatch({
    required this.lead,
    required this.tokenStart,
    required this.tokenEnd,
    required this.suggestions,
  });
}

/// Horizontal chip strip rendered above the composer when a `/` or `@`
/// trigger is active. Capped at the same length as `suggestions` (the
/// match's slice) so a 200-tool driver doesn't blow the row out.
class _SuggestionStrip extends StatelessWidget {
  final _PrefixMatch match;
  final void Function(String) onTap;
  final Color border;
  final Color muted;
  const _SuggestionStrip({
    required this.match,
    required this.onTap,
    required this.border,
    required this.muted,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 6, left: 4),
      child: SizedBox(
        height: 28,
        child: ListView.separated(
          scrollDirection: Axis.horizontal,
          itemCount: match.suggestions.length,
          separatorBuilder: (_, _) => const SizedBox(width: 6),
          itemBuilder: (ctx, i) {
            final s = match.suggestions[i];
            return InkWell(
              onTap: () => onTap(s),
              borderRadius: BorderRadius.circular(4),
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 8),
                alignment: Alignment.center,
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(4),
                  border: Border.all(color: border),
                ),
                child: Text(
                  '${match.lead}$s',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 11,
                    color: muted,
                  ),
                ),
              ),
            );
          },
        ),
      ),
    );
  }
}
