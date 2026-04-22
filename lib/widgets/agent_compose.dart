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
class AgentCompose extends ConsumerStatefulWidget {
  final String agentId;
  const AgentCompose({super.key, required this.agentId});

  @override
  ConsumerState<AgentCompose> createState() => _AgentComposeState();
}

class _AgentComposeState extends ConsumerState<AgentCompose> {
  final TextEditingController _ctrl = TextEditingController();
  final FocusNode _focus = FocusNode();
  bool _sending = false;
  String? _error;

  @override
  void dispose() {
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
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
                builder: (_, value, __) {
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
