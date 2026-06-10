// AgentFeed approval cards — the interactive approval-request and
// AskUserQuestion surfaces rendered inside an event card.
//
// Cluster wedge of the agent_feed split (docs/plans/agent-feed-split.md,
// W5). Rendered from the event card (kind=approval_request and the
// AskUserQuestion tool-call), so the two entry-point cards (ApprovalCard,
// AskUserQuestionCard) are public; their option models, the decision
// chip, and the State classes are approval-only and stay private.
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/hub_client.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import 'feed_render.dart';


/// Interactive approval card rendered for agent_events of kind
/// `approval_request`. Buttons come from the payload's options list;
/// tapping posts an input.approval back to the hub, which the
/// InputRouter then forwards to ACPDriver.Input → JSON-RPC response.
/// Once answered, the card collapses to a decision chip so reopening
/// the feed doesn't show the buttons again.
class ApprovalCard extends ConsumerStatefulWidget {
  final String? agentId;
  final String requestId;
  final Map<String, dynamic> params;
  final String? priorDecision;
  const ApprovalCard({
    required this.agentId,
    required this.requestId,
    required this.params,
    required this.priorDecision,
  });

  @override
  ConsumerState<ApprovalCard> createState() => _ApprovalCardState();
}

class _ApprovalCardState extends ConsumerState<ApprovalCard> {
  bool _sending = false;
  String? _error;
  String? _localDecision;

  String? get _effectiveDecision => _localDecision ?? widget.priorDecision;

  Future<void> _send(String decision, {String? optionId}) async {
    final agentId = widget.agentId;
    if (agentId == null || widget.requestId.isEmpty) return;
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
      await client.postAgentInput(
        agentId,
        kind: 'approval',
        requestId: widget.requestId,
        decision: decision,
        optionId: optionId,
      );
      if (!mounted) return;
      setState(() {
        _sending = false;
        _localDecision = decision;
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _error = 'Send failed (${e.status})';
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _error = 'Send failed: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final toolCall = widget.params['toolCall'];
    String? toolSummary;
    if (toolCall is Map) {
      final name = toolCall['name']?.toString();
      if (name != null && name.isNotEmpty) toolSummary = name;
    }
    // Options may arrive as a list of {optionId, name} maps. Fall back to
    // a hard-coded allow/deny pair so the card still works with agents
    // that skip the options block.
    final rawOptions = widget.params['options'];
    final options = <_ApprovalOption>[];
    if (rawOptions is List) {
      for (final o in rawOptions) {
        if (o is Map) {
          final id = o['optionId']?.toString() ?? o['id']?.toString() ?? '';
          final label = o['name']?.toString() ?? o['label']?.toString() ?? id;
          if (id.isNotEmpty) {
            options.add(_ApprovalOption(id: id, label: label));
          }
        }
      }
    }
    if (options.isEmpty) {
      options.addAll(const [
        _ApprovalOption(id: 'allow', label: 'Allow'),
        _ApprovalOption(id: 'deny', label: 'Deny'),
      ]);
    }

    final decided = _effectiveDecision;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (toolSummary != null)
          Padding(
            padding: const EdgeInsets.only(bottom: Spacing.s8),
            child: RichText(
              text: TextSpan(
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: isDark
                      ? DesignColors.textSecondary
                      : DesignColors.textSecondaryLight,
                ),
                children: [
                  TextSpan(
                      text: 'tool: ',
                      style: TextStyle(color: muted)),
                  TextSpan(text: toolSummary),
                ],
              ),
            ),
          ),
        if (widget.params.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: CollapsibleMono(
              text: feedJsonPretty(widget.params),
            ),
          ),
        if (decided != null)
          _DecisionChip(decision: decided)
        else
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              for (final o in options)
                FilledButton(
                  onPressed: _sending ? null : () => _send(o.id, optionId: o.id),
                  style: FilledButton.styleFrom(
                    backgroundColor: o.id == 'allow'
                        ? DesignColors.success
                        : (o.id == 'deny'
                            ? DesignColors.error
                            : DesignColors.primary),
                    padding: const EdgeInsets.symmetric(
                        horizontal: 12, vertical: Spacing.s8),
                    minimumSize: const Size(0, 32),
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  child: Text(
                    o.label,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
              OutlinedButton(
                onPressed: _sending ? null : () => _send('cancel'),
                style: OutlinedButton.styleFrom(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 12, vertical: Spacing.s8),
                  minimumSize: const Size(0, 32),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: Text(
                  'Cancel',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    color: muted,
                  ),
                ),
              ),
            ],
          ),
        if (_error != null)
          Padding(
            padding: const EdgeInsets.only(top: Spacing.s8),
            child: Text(
              _error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error),
            ),
          ),
      ],
    );
  }
}

class _ApprovalOption {
  final String id;
  final String label;
  const _ApprovalOption({required this.id, required this.label});
}

class _DecisionChip extends StatelessWidget {
  final String decision;
  const _DecisionChip({required this.decision});

  @override
  Widget build(BuildContext context) {
    final color = switch (decision) {
      'allow' => DesignColors.success,
      'deny' => DesignColors.error,
      'cancel' => DesignColors.textMuted,
      _ => DesignColors.primary,
    };
    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.15),
          borderRadius: BorderRadius.circular(4),
          border: Border.all(color: color.withValues(alpha: 0.5)),
        ),
        child: Text(
          'decided: $decision',
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            color: color,
          ),
        ),
      ),
    );
  }
}

/// Inline interactive card for `AskUserQuestion` tool calls. The
/// claude-code agent emits a tool_call whose input carries a list of
/// `questions[].options[]`; we render the question + options as
/// buttons here so the user can answer in-flow instead of waiting for
/// the agent to time out (which it does noisily — the user reported
/// "looks like the question prompt was canceled" after a missed
/// reply). Tap → POST input.answer with the chosen option as the
/// body; the hostrunner's stdio driver wraps it in a tool_result with
/// the matching tool_use_id and ships it back to claude-code on
/// stdin.
///
/// Multi-question payloads are technically allowed by the SDK but
/// rare in practice — we render the first question and treat the
/// rest as fallback (their bodies appear in a small JSON dump). We
/// can iterate on multi-question UX once a real example shows up.
class AskUserQuestionCard extends ConsumerStatefulWidget {
  final String? agentId;
  final String toolUseId;
  final Map<String, dynamic> input;
  final Map<String, dynamic>? priorAnswer;
  const AskUserQuestionCard({
    super.key,
    required this.agentId,
    required this.toolUseId,
    required this.input,
    required this.priorAnswer,
  });

  @override
  ConsumerState<AskUserQuestionCard> createState() =>
      _AskUserQuestionCardState();
}

class _AskUserQuestionCardState extends ConsumerState<AskUserQuestionCard> {
  bool _sending = false;
  String? _error;
  String? _localAnswer;

  String? get _effectiveAnswer {
    if (_localAnswer != null) return _localAnswer;
    final prior = widget.priorAnswer;
    if (prior == null) return null;
    final payload = prior['payload'];
    if (payload is Map) {
      final c = payload['content'];
      if (c is String && c.isNotEmpty) return c;
    }
    return null;
  }

  Future<void> _send(String label) async {
    final agentId = widget.agentId;
    if (agentId == null || agentId.isEmpty || widget.toolUseId.isEmpty) return;
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
      await client.postAgentInput(
        agentId,
        kind: 'answer',
        requestId: widget.toolUseId,
        body: label,
      );
      if (!mounted) return;
      setState(() {
        _sending = false;
        _localAnswer = label;
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _error = 'Send failed (${e.status})';
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _error = 'Send failed: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final questions = widget.input['questions'];
    Map<String, dynamic>? primary;
    if (questions is List && questions.isNotEmpty) {
      final q = questions.first;
      if (q is Map) primary = q.cast<String, dynamic>();
    }
    if (primary == null) {
      // Defensive fallback: payload didn't match the expected shape.
      // Render the raw input so nothing is silently hidden.
      return CollapsibleMono(text: feedJsonPretty(widget.input));
    }
    final header = (primary['header'] ?? '').toString();
    final question = (primary['question'] ?? '').toString();
    final rawOptions = primary['options'];
    final options = <_AskOption>[];
    if (rawOptions is List) {
      for (final o in rawOptions) {
        if (o is Map) {
          final label = (o['label'] ?? '').toString();
          if (label.isEmpty) continue;
          options.add(_AskOption(
            label: label,
            description: (o['description'] ?? '').toString(),
          ));
        }
      }
    }
    final answered = _effectiveAnswer;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (header.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(bottom: 4),
            child: Text(
              header,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                color: muted,
                letterSpacing: 0.4,
              ),
            ),
          ),
        if (question.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text(
              question,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        if (answered != null)
          Align(
            alignment: Alignment.centerLeft,
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              decoration: BoxDecoration(
                color: DesignColors.success.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
                border: Border.all(
                    color: DesignColors.success.withValues(alpha: 0.5)),
              ),
              child: Text(
                'answered: $answered',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  fontWeight: FontWeight.w700,
                  color: DesignColors.success,
                ),
              ),
            ),
          )
        else if (options.isEmpty)
          Text(
            '(no options provided)',
            style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
          )
        else
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (final o in options)
                Padding(
                  padding: const EdgeInsets.only(bottom: Spacing.s8),
                  child: OutlinedButton(
                    onPressed: _sending ? null : () => _send(o.label),
                    style: OutlinedButton.styleFrom(
                      alignment: Alignment.centerLeft,
                      padding: const EdgeInsets.symmetric(
                          horizontal: 12, vertical: 8),
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(
                          o.label,
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 13,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                        if (o.description.isNotEmpty)
                          Padding(
                            padding: const EdgeInsets.only(top: 2),
                            child: Text(
                              o.description,
                              style: GoogleFonts.spaceGrotesk(
                                fontSize: 11,
                                color: muted,
                              ),
                            ),
                          ),
                      ],
                    ),
                  ),
                ),
            ],
          ),
        if (_error != null)
          Padding(
            padding: const EdgeInsets.only(top: Spacing.s8),
            child: Text(
              _error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error),
            ),
          ),
      ],
    );
  }
}

class _AskOption {
  final String label;
  final String description;
  const _AskOption({required this.label, required this.description});
}

