// AgentFeed event card — the per-event transcript card.
//
// Final cluster wedge of the agent_feed split
// (docs/plans/agent-feed-split.md, W6). This is the residue the other
// five wedges peeled around: the card that renders one agent_event,
// dispatching on its kind to a first-class layout or a raw-JSON
// fallback. It composes the already-extracted clusters — FoldableToolCall
// (tool_renderers), ApprovalCard/AskUserQuestionCard (approval_cards),
// CollapsibleMono/feedJsonPretty (feed_render), the markdown builders —
// and keeps its own event-card-only helpers (_kv/_mono/_textBody, the
// _Diff* trio, _fmtDuration) private. AgentEventCard is public because
// the feed container builds it; everything else here is private.
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:markdown/markdown.dart' as md;

import '../../theme/design_colors.dart';
import '../markdown_builders.dart';
import 'approval_cards.dart';
import 'feed_reducer.dart';
import 'feed_render.dart';
import 'tool_renderers.dart';

/// Per-event card. The kind drives which fields get a first-class
/// treatment; everything else falls through to a raw JSON block so we
/// stay forward-compatible with new event kinds the hub emits.
class AgentEventCard extends StatefulWidget {
  final Map<String, dynamic> event;
  // tool_use_id → tool name map, built by the Feed from all visible
  // tool_call events so tool_result cards can show the human name
  // instead of a 24-char id. Empty when no context is available.
  final Map<String, String> toolNames;
  // toolCallId → latest tool_call_update payload. The tool_call body
  // pulls status/content from here so progress ticks don't need their
  // own card.
  final Map<String, Map<String, dynamic>> toolUpdates;
  // tool_use_id → matching tool_result event. The tool_call card folds
  // its result inline (lineage cards, W-UI-2) so each call is one
  // expandable surface — pending while no result, success/error once
  // it arrives. Orphaned results (no parent tool_call) are not in this
  // map and still render as standalone cards.
  final Map<String, Map<String, dynamic>> toolResults;
  // request_id → prior decision. Present entries mean the user already
  // answered this approval, so we render the chip but not the buttons.
  final Map<String, String> resolvedApprovals;
  // Needed for the approval card so it can call postAgentInput.
  final String? agentId;
  const AgentEventCard({
    super.key,
    required this.event,
    this.toolNames = const {},
    this.toolUpdates = const {},
    this.toolResults = const {},
    this.resolvedApprovals = const {},
    this.agentId,
  });

  @override
  State<AgentEventCard> createState() => _AgentEventCardState();

  // Builds the clipboard payload for a given card. The principal hits
  // copy on a transcript tile most often to drop content into a bug
  // report, a follow-up prompt to a different agent, or a doc — so
  // prefer the *rendered* content (text, tool args, json body) over
  // the wrapping event metadata. For unknown kinds, fall through to
  // pretty JSON so nothing is silently lost.
  static String _copyTextFor(
    String kind,
    Map<String, dynamic> payload,
    Map<String, dynamic> event,
  ) {
    String s;
    switch (kind) {
      case 'text':
      case 'thought':
        s = (payload['text'] ?? '').toString();
        break;
      case 'tool_call':
        final name = (payload['name'] ?? payload['tool'] ?? 'tool').toString();
        final input = payload['input'] ?? payload['arguments'] ?? payload['args'];
        s = '$name\n${feedJsonPretty(input is Map ? input : payload)}';
        break;
      case 'tool_result':
        final content = payload['content'];
        if (content is String && content.isNotEmpty) {
          s = content;
        } else if (content is Map || content is List) {
          s = feedJsonPretty(content);
        } else {
          s = (payload['text'] ?? feedJsonPretty(payload)).toString();
        }
        break;
      case 'system':
        // System rows usually carry a one-liner; otherwise fall back
        // to the full payload so audit-trail entries copy with their
        // structured fields intact.
        final t = (payload['text'] ?? payload['summary'] ?? '').toString();
        s = t.isNotEmpty ? t : feedJsonPretty(payload);
        break;
      default:
        final t = (payload['text'] ?? '').toString();
        s = t.isNotEmpty ? t : feedJsonPretty(payload);
    }
    return s.isEmpty ? feedJsonPretty(event) : s;
  }

  Widget _body(
    BuildContext ctx,
    String kind,
    String producer,
    Map<String, dynamic> payload,
  ) {
    switch (kind) {
      case 'lifecycle':
        return _lifecycleBody(ctx, payload);
      case 'session.init':
        return _sessionInitBody(ctx, payload);
      case 'text':
      case 'thought':
        return _markdownBody(
          ctx,
          (payload['text'] ?? feedJsonPretty(payload)).toString(),
          isThought: kind == 'thought',
        );
      case 'raw':
        return _rawBody(ctx, payload);
      case 'tool_call':
        return _toolCallBody(ctx, payload);
      case 'tool_call_update':
        return _toolCallUpdateBody(ctx, payload);
      case 'tool_result':
        return _toolResultBody(ctx, payload);
      case 'turn.result':
        return _turnResultBody(ctx, payload);
      case 'completion':
        return _completionBody(ctx, payload);
      case 'error':
        return _errorBody(ctx, payload);
      case 'approval_request':
        return _approvalRequestBody(ctx, payload);
      case 'plan':
        return _planBody(ctx, payload);
      case 'diff':
        return _diffBody(ctx, payload);
      case 'input.text':
        return _inputTextBody(ctx, payload);
      case 'input.cancel':
        return _inputCancelBody(ctx, payload);
      case 'input.approval':
        return _inputApprovalBody(ctx, payload);
      case 'input.attention_reply':
        return _inputAttentionReplyBody(ctx, payload);
      case 'system':
        return _systemBody(ctx, payload);
      default:
        // Any other hub-side kinds — render their text field when present,
        // fall back to pretty JSON otherwise.
        final t = payload['text']?.toString();
        if (t != null && t.isNotEmpty) return _textBody(ctx, t);
        return _textBody(ctx, feedJsonPretty(payload));
    }
  }

  Widget _inputTextBody(BuildContext ctx, Map<String, dynamic> p) {
    // ADR-032: an input.text payload is the message envelope —
    // {from,to,kind,text,cause,thread} at the top level. The body
    // resolves via `text` (legacy `body` kept as a fallback). When the
    // envelope carries a sender / kind, surface them: an A2A message
    // would otherwise render with no visible sender.
    //
    // v1.0.707 polish — `payload.raw == true` marks an
    // engine-control slash command sent without the envelope wrap
    // (e.g. /clear, /compact). For those we suppress the "from /
    // kind" header rows entirely — they'd be misleading (no
    // envelope was attached) and a slash command is self-
    // describing.
    final body = (p['text'] ?? p['body'] ?? '').toString();
    final raw = p['raw'] == true;
    final rows = <Widget>[];
    if (!raw) {
      final from = p['from'];
      final kind = (p['kind'] ?? '').toString();
      final fromLabel = (p['from_label'] ?? '').toString();
      if (from is Map) {
        final role = (from['role'] ?? '').toString();
        final handle = (from['handle'] ?? '').toString();
        final label = envelopeSenderLabel(
          role: role,
          handle: handle,
          fromLabel: fromLabel,
        );
        if (label.isNotEmpty) rows.add(_kv(ctx, 'from', label));
      } else if (fromLabel.isNotEmpty) {
        // Legacy / sparse payload that carries `from_label` without a
        // structured `from` map. Still render the row — the hub-side
        // stamp is the source of truth either way.
        rows.add(_kv(ctx, 'from', fromLabel));
      }
      if (kind.isNotEmpty) rows.add(_kv(ctx, 'kind', kind));
    }
    if (rows.isEmpty) {
      return _mono(ctx, body.isEmpty ? '(empty)' : body);
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        ...rows,
        const SizedBox(height: 4),
        _mono(ctx, body.isEmpty ? '(empty)' : body),
      ],
    );
  }

  Widget _inputCancelBody(BuildContext ctx, Map<String, dynamic> p) {
    final reason = p['reason']?.toString();
    return _mono(
      ctx,
      (reason == null || reason.isEmpty) ? 'cancel' : 'cancel · $reason',
    );
  }

  Widget _inputApprovalBody(BuildContext ctx, Map<String, dynamic> p) {
    final decision = p['decision']?.toString() ?? '?';
    final reqId = p['request_id']?.toString() ?? '';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _kv(ctx, 'decision', decision),
        if (reqId.isNotEmpty) _kv(ctx, 'request_id', reqId),
      ],
    );
  }

  // Renders the principal's reply to a vendor-neutral attention
  // (request_approval / request_select / request_help). The reply is
  // posted by hub /decide as a structured event; the rendered text we
  // build here mirrors `formatAttentionReplyText` in
  // hub/internal/hostrunner/driver_stdio.go — same per-kind shape
  // because the engine sees this exact text as a user turn, and the
  // transcript should match what the agent saw on the wire.
  Widget _inputAttentionReplyBody(BuildContext ctx, Map<String, dynamic> p) {
    final decision = p['decision']?.toString() ?? '?';
    final kind = p['kind']?.toString() ?? '';
    final reqId = p['request_id']?.toString() ?? '';
    final body = p['body']?.toString() ?? '';
    final optionId = p['option_id']?.toString() ?? '';
    final reason = p['reason']?.toString() ?? '';
    final rendered = renderAttentionReplyText(p);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // Lead with the literal text the engine receives. Reads like a
        // typed user message — that's the user's mental model when
        // they tap Approve.
        if (rendered.isNotEmpty) _mono(ctx, rendered),
        if (rendered.isNotEmpty) const SizedBox(height: 6),
        _kv(ctx, 'decision', decision),
        if (kind.isNotEmpty) _kv(ctx, 'kind', kind),
        if (optionId.isNotEmpty) _kv(ctx, 'option_id', optionId),
        if (body.isNotEmpty && rendered != body) _kv(ctx, 'reply', body),
        if (reason.isNotEmpty) _kv(ctx, 'reason', reason),
        if (reqId.isNotEmpty) _kv(ctx, 'request_id', reqId),
      ],
    );
  }


  // Compact renderer for non-init `system` frames. claude-code emits
  // these for sub-agent state (task_started / task_updated /
  // task_notification — shape: {subtype, task_id, ...}) and for the
  // occasional engine-level message. Without this case the default
  // branch dumped the full frame JSON, which dominated the transcript
  // every time the agent backgrounded a task. Render a one-liner per
  // frame; fall back to pretty JSON for subtypes we don't model.
  Widget _systemBody(BuildContext ctx, Map<String, dynamic> p) {
    final subtype = (p['subtype'] ?? '').toString();
    final taskId = (p['task_id'] ?? '').toString();
    String? line;
    switch (subtype) {
      case 'task_started':
        // claude usually carries the spawned subagent's name + initial
        // prompt; show whichever is present without pretending a
        // structure we may not have.
        final name = (p['agent'] ?? p['name'] ?? '').toString();
        final desc = (p['description'] ?? p['prompt'] ?? '').toString();
        final head = name.isEmpty ? 'Task started' : 'Task started · $name';
        line = desc.isEmpty ? head : '$head — $desc';
        break;
      case 'task_updated':
        // Surface the patch keys so the user sees *what* changed without
        // dumping the whole envelope (uuid, session_id, parent_uuid).
        final patch = p['patch'];
        if (patch is Map && patch.isNotEmpty) {
          final pairs = patch.entries
              .map((e) => '${e.key}=${e.value}')
              .join(', ');
          line = 'Task updated · $pairs';
        } else {
          line = 'Task updated';
        }
        break;
      case 'task_notification':
        final msg = (p['message'] ?? p['text'] ?? p['notification'] ?? '').toString();
        line = msg.isEmpty ? 'Task notification' : 'Task: $msg';
        break;
    }
    if (line != null) {
      final suffix = taskId.isEmpty ? '' : '  ·  $taskId';
      return _mono(ctx, '$line$suffix');
    }
    // Unknown subtype — keep the legacy JSON dump so nothing is silently
    // hidden, but tag the subtype on top so the user can spot the kind.
    final t = p['text']?.toString();
    if (t != null && t.isNotEmpty) return _textBody(ctx, t);
    return _textBody(ctx, feedJsonPretty(p));
  }

  Widget _lifecycleBody(BuildContext ctx, Map<String, dynamic> p) {
    final phase = p['phase']?.toString() ?? '?';
    final mode = p['mode']?.toString();
    return _mono(
      ctx,
      mode == null ? phase : '$phase · mode=$mode',
    );
  }

  Widget _sessionInitBody(BuildContext ctx, Map<String, dynamic> p) {
    final sid = p['session_id']?.toString() ?? '?';
    final model = p['model']?.toString() ?? '';
    final toolsRaw = p['tools'];
    final tools = toolsRaw is List ? toolsRaw.length : 0;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _kv(ctx, 'session', sid),
        if (model.isNotEmpty) _kv(ctx, 'model', model),
        if (tools > 0) _kv(ctx, 'tools', '$tools'),
      ],
    );
  }

  Widget _toolCallBody(BuildContext ctx, Map<String, dynamic> p) {
    final name = p['name']?.toString() ?? '?';
    final id = p['id']?.toString() ?? '';
    final input = p['input'];
    // AskUserQuestion is the only tool whose answer the user has to
    // produce — claude-code emits a tool_call and waits for a
    // tool_result that holds the picked option. Render it inline
    // here instead of falling through to the generic tool_call card,
    // so the user doesn't have to copy-paste the question or watch
    // the agent timeout. Falls back to the standard card if the
    // payload is missing the expected `questions[]` shape.
    if (name == 'AskUserQuestion' &&
        id.isNotEmpty &&
        input is Map &&
        input['questions'] is List) {
      return AskUserQuestionCard(
        key: ValueKey('ask-uq-$id'),
        agentId: agentId,
        toolUseId: id,
        input: input.cast<String, dynamic>(),
        priorAnswer: id.isNotEmpty ? toolResults[id] : null,
      );
    }
    // Fold the latest tool_call_update so a single card shows the end
    // state (status + optional content preview) without a second row.
    final update = id.isNotEmpty ? toolUpdates[id] : null;
    // Pair with the matching tool_result by tool_use_id (W-UI-2). When
    // present, the call has resolved — derive a terminal status from
    // is_error so the card reads "completed" / "failed" without needing
    // a tool_call_update from drivers that don't emit them.
    final resultEvent = id.isNotEmpty ? toolResults[id] : null;
    final resultPayload = resultEvent != null && resultEvent['payload'] is Map
        ? (resultEvent['payload'] as Map).cast<String, dynamic>()
        : null;
    final hasResult = resultPayload != null;
    final resultIsError = resultPayload?['is_error'] == true;
    final updateStatus = (update?['status'] ?? p['status'] ?? '').toString();
    final status = updateStatus.isNotEmpty
        ? updateStatus
        : (hasResult ? (resultIsError ? 'failed' : 'completed') : 'pending');
    // ACP tool_call_update.content is a list of content blocks; pull the
    // first text block for a compact preview. Larger outputs land in
    // tool_result anyway so this is just for at-a-glance progress.
    String? preview;
    final content = update?['content'];
    if (content is List) {
      for (final b in content) {
        if (b is Map && b['type'] == 'content') {
          final inner = b['content'];
          if (inner is Map && inner['type'] == 'text') {
            preview = inner['text']?.toString();
            break;
          }
        }
      }
    }
    return FoldableToolCall(
      // Stable identity so toggling fold state survives card rebuilds
      // when new events stream in or the parent setState fires. Without
      // a key the widget would replay its initial _expanded value on
      // every rebuild.
      key: id.isNotEmpty ? ValueKey('tool-fold-$id') : null,
      name: name,
      status: status,
      toolId: id,
      input: input,
      preview: preview,
      resultPayload: resultPayload,
      resultIsError: resultIsError,
    );
  }

  // Verbose-only renderer for ACP tool_call_update wire frames. Folds
  // its data into the parent tool_call card by default; this card is
  // for the rare case the user toggled debug visibility to inspect
  // intermediate states (e.g. confirming the request_approval gate
  // returned its attention payload).
  Widget _toolCallUpdateBody(BuildContext ctx, Map<String, dynamic> p) {
    final id = p['toolCallId']?.toString() ?? '';
    final status = p['status']?.toString() ?? '';
    final title = p['title']?.toString() ?? '';
    String? preview;
    final content = p['content'];
    if (content is List) {
      for (final b in content) {
        if (b is Map && b['type'] == 'content') {
          final inner = b['content'];
          if (inner is Map && inner['type'] == 'text') {
            preview = inner['text']?.toString();
            break;
          }
        }
      }
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (title.isNotEmpty) _kv(ctx, 'tool', title),
        if (status.isNotEmpty) _kv(ctx, 'status', status),
        if (id.isNotEmpty) _kv(ctx, 'tool_call_id', id),
        if (preview != null && preview.isNotEmpty) _mono(ctx, preview),
      ],
    );
  }

  // Verbose-only renderer for turn.result wire frames. Telemetry
  // strip already aggregates these on every turn — the card is for
  // forensic visibility (e.g. seeing stopReason=cancelled when a new
  // attention_reply prompt cancelled the in-flight one).
  Widget _turnResultBody(BuildContext ctx, Map<String, dynamic> p) {
    final status = p['status']?.toString() ?? '';
    final reason = p['stop_reason']?.toString() ?? '';
    final input = p['input_tokens'];
    final output = p['output_tokens'];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (status.isNotEmpty) _kv(ctx, 'status', status),
        if (reason.isNotEmpty) _kv(ctx, 'stop_reason', reason),
        if (input is num) _kv(ctx, 'input_tokens', input.toString()),
        if (output is num) _kv(ctx, 'output_tokens', output.toString()),
      ],
    );
  }

  Widget _toolResultBody(BuildContext ctx, Map<String, dynamic> p) {
    final id = p['tool_use_id']?.toString() ?? '';
    final name = id.isNotEmpty ? toolNames[id] : null;
    final isError = p['is_error'] == true;
    final content = p['content'];
    final text = content is String ? content : feedJsonPretty(content);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (name != null) _kv(ctx, 'tool', name),
        if (id.isNotEmpty) _kv(ctx, 'tool_use_id', id),
        if (isError) _kv(ctx, 'is_error', 'true'),
        CollapsibleMono(
          text: text,
          color: isError ? DesignColors.error : null,
        ),
      ],
    );
  }

  Widget _completionBody(BuildContext ctx, Map<String, dynamic> p) {
    final sub = p['subtype']?.toString();
    final dur = p['duration_ms'];
    final res = p['result']?.toString();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (sub != null && sub.isNotEmpty) _kv(ctx, 'subtype', sub),
        if (dur is num) _kv(ctx, 'duration', _fmtDuration(dur.toInt())),
        if (res != null && res.isNotEmpty) _mono(ctx, res),
      ],
    );
  }

  // duration_ms comes through as a raw integer; "42357" is cognitive
  // load when "42s" reads at a glance. Anything over a minute shows
  // m+s; anything over an hour shows h+m.
  String _fmtDuration(int ms) {
    if (ms < 1000) return '${ms}ms';
    final s = ms ~/ 1000;
    if (s < 60) {
      final tenths = (ms % 1000) ~/ 100;
      return tenths == 0 ? '${s}s' : '$s.${tenths}s';
    }
    if (s < 3600) return '${s ~/ 60}m ${s % 60}s';
    final h = s ~/ 3600;
    final m = (s % 3600) ~/ 60;
    return '${h}h ${m}m';
  }

  Widget _errorBody(BuildContext ctx, Map<String, dynamic> p) {
    final msg = (p['error'] ?? p['message'] ?? feedJsonPretty(p)).toString();
    return _mono(ctx, msg, color: DesignColors.error);
  }

  // ACP plan update: { sessionUpdate: "plan", entries: [{content, priority,
  // status}] }. Render as a compact checklist so the operator can see what
  // the agent is tracking without drilling into raw JSON.
  Widget _planBody(BuildContext ctx, Map<String, dynamic> p) {
    final entriesRaw = p['entries'];
    if (entriesRaw is! List || entriesRaw.isEmpty) {
      return _mono(ctx, feedJsonPretty(p));
    }
    final rows = <Widget>[];
    for (final e in entriesRaw) {
      if (e is! Map) continue;
      final status = (e['status'] ?? '').toString();
      final content = (e['content'] ?? '').toString();
      rows.add(Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(_planStatusIcon(status),
                size: 14, color: _planStatusColor(status)),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                content,
                style: TextStyle(
                  fontSize: 13,
                  decoration: status == 'completed'
                      ? TextDecoration.lineThrough
                      : null,
                  color: status == 'completed'
                      ? DesignColors.textMuted
                      : null,
                ),
              ),
            ),
          ],
        ),
      ));
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: rows,
    );
  }

  static IconData _planStatusIcon(String status) {
    switch (status) {
      case 'completed':
        return Icons.check_circle_outline;
      case 'in_progress':
        return Icons.radio_button_checked;
      case 'pending':
      default:
        return Icons.radio_button_unchecked;
    }
  }

  // ACP diff update: { sessionUpdate: "diff", path, oldText, newText }.
  // Shows the file path + a +N/-N summary + a collapsible unified-diff
  // preview (plain line-by-line comparison; not a real LCS diff).
  Widget _diffBody(BuildContext ctx, Map<String, dynamic> p) {
    final path = (p['path'] ?? '').toString();
    final oldText = (p['oldText'] ?? p['old_text'] ?? '').toString();
    final newText = (p['newText'] ?? p['new_text'] ?? '').toString();
    final oldLines = oldText.isEmpty ? <String>[] : oldText.split('\n');
    final newLines = newText.isEmpty ? <String>[] : newText.split('\n');
    int adds = 0;
    int dels = 0;
    final rows = <_DiffLine>[];
    final maxLen = math.max(oldLines.length, newLines.length);
    for (var i = 0; i < maxLen; i++) {
      final o = i < oldLines.length ? oldLines[i] : null;
      final n = i < newLines.length ? newLines[i] : null;
      if (o == n) {
        rows.add(_DiffLine(kind: _DiffKind.context, text: o ?? ''));
      } else {
        if (o != null) {
          rows.add(_DiffLine(kind: _DiffKind.delete, text: o));
          dels++;
        }
        if (n != null) {
          rows.add(_DiffLine(kind: _DiffKind.insert, text: n));
          adds++;
        }
      }
    }
    final summary = adds > 0 || dels > 0 ? '+$adds / -$dels' : '0 changes';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (path.isNotEmpty) _kv(ctx, 'path', path),
        _kv(ctx, 'change', summary),
        if (rows.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: _DiffView(lines: rows),
          ),
      ],
    );
  }

  static Color _planStatusColor(String status) {
    switch (status) {
      case 'completed':
        return DesignColors.success;
      case 'in_progress':
        return DesignColors.primary;
      case 'pending':
      default:
        return DesignColors.textMuted;
    }
  }

  Widget _approvalRequestBody(BuildContext ctx, Map<String, dynamic> p) {
    final requestId = p['request_id']?.toString() ?? '';
    final params = (p['params'] is Map)
        ? (p['params'] as Map).cast<String, dynamic>()
        : <String, dynamic>{};
    final priorDecision = resolvedApprovals[requestId];
    return ApprovalCard(
      agentId: agentId,
      requestId: requestId,
      params: params,
      priorDecision: priorDecision,
    );
  }

  Widget _textBody(BuildContext ctx, String s) => _mono(ctx, s);

  // `raw` covers three shapes from driver_acp.go:
  //   {"text": "..."}                         — scanner/unmarshal failure
  //   {"method": "x", "params": ...}          — unknown JSON-RPC notification
  //   {"sessionUpdate": "x", ...}             — unhandled session/update kind
  // Show the identifying field at the top so an unknown frame is legible
  // at a glance; hide the rest behind CollapsibleMono.
  Widget _rawBody(BuildContext ctx, Map<String, dynamic> p) {
    final text = p['text']?.toString();
    if (text != null && text.isNotEmpty && p.length == 1) {
      return _mono(ctx, text);
    }
    final method = p['method']?.toString();
    final sessionUpdate = p['sessionUpdate']?.toString();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (method != null && method.isNotEmpty) _kv(ctx, 'method', method),
        if (sessionUpdate != null && sessionUpdate.isNotEmpty)
          _kv(ctx, 'update', sessionUpdate),
        CollapsibleMono(text: feedJsonPretty(p)),
      ],
    );
  }

  // Agents (Claude Code especially) emit markdown heavily — bullet lists,
  // fenced code blocks, headers. Rendering as plain mono text buries the
  // structure; rendering with a tight style sheet keeps the card compact
  // while still reading like the agent's terminal output.
  Widget _markdownBody(BuildContext ctx, String s, {bool isThought = false}) {
    final isDark = Theme.of(ctx).brightness == Brightness.dark;
    final textColor = isThought
        ? (isDark ? DesignColors.textMuted : DesignColors.textMutedLight)
        : (isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight);
    final codeBg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;
    final codeBorder = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    final base = GoogleFonts.spaceGrotesk(
      fontSize: 13,
      height: 1.35,
      color: textColor,
      fontStyle: isThought ? FontStyle.italic : FontStyle.normal,
    );
    final codeStyle = GoogleFonts.jetBrainsMono(
      fontSize: 11,
      height: 1.35,
      color: textColor,
    );
    return MarkdownBody(
      data: normalizeMultilineMath(s),
      selectable: true,
      shrinkWrap: true,
      // Tap on `[text](href)` opens the URL in the system browser.
      // Underline + primary color come from styleSheet.a below; we
      // intentionally don't register a custom 'a' element builder,
      // because flutter_markdown appends the builder's widget *after*
      // the default styled inline span — registering one renders the
      // visible label twice (once colored-underlined, once tappable).
      onTapLink: (text, href, title) => openMarkdownLink(ctx, href),
      builders: {
        'code': HighlightedCodeBuilder(isDark: isDark),
        // KaTeX-style LaTeX math. Two flavors of the same builder so
        // the markdown parser can route inline ($...$) and display
        // ($$...$$) at different vertical sizes/alignment.
        'math': MathBuilder(isDark: isDark, display: false),
        'mathblock': MathBuilder(isDark: isDark, display: true),
      },
      // Custom inline syntaxes only — no BlockSyntax. The preprocessor
      // (normalizeMultilineMath) collapses well-formed multi-line
      // $$...$$ and \[...\] regions into single-line $$...$$ before
      // we get here; unbalanced delimiters fall through to plain text.
      // Order matters: $$...$$ must be tried before $...$ or the
      // parser will eat the leading $$ as two empty $$s; same for
      // \[...\] vs \(...\).
      extensionSet: md.ExtensionSet(
        md.ExtensionSet.gitHubFlavored.blockSyntaxes,
        [
          MathBlockInlineSyntax(),
          MathInlineSyntax(),
          LatexBracketDisplayInlineSyntax(),
          LatexBracketInlineSyntax(),
          ...md.ExtensionSet.gitHubFlavored.inlineSyntaxes,
        ],
      ),
      // Keep paragraph and block spacing tight so cards don't balloon.
      styleSheet: MarkdownStyleSheet(
        p: base,
        a: base.copyWith(
          color: DesignColors.primary,
          decoration: TextDecoration.underline,
          decorationColor: DesignColors.primary.withValues(alpha: 0.4),
        ),
        strong: base.copyWith(fontWeight: FontWeight.w700),
        em: base.copyWith(fontStyle: FontStyle.italic),
        code: codeStyle,
        codeblockPadding: const EdgeInsets.all(8),
        codeblockDecoration: BoxDecoration(
          color: codeBg,
          borderRadius: BorderRadius.circular(4),
          border: Border.all(color: codeBorder),
        ),
        h1: base.copyWith(fontSize: 16, fontWeight: FontWeight.w700),
        h2: base.copyWith(fontSize: 15, fontWeight: FontWeight.w700),
        h3: base.copyWith(fontSize: 14, fontWeight: FontWeight.w700),
        h4: base.copyWith(fontSize: 13, fontWeight: FontWeight.w700),
        blockquote: base.copyWith(
          color: isDark
              ? DesignColors.textMuted
              : DesignColors.textMutedLight,
        ),
        blockquoteDecoration: BoxDecoration(
          border: Border(
            left: BorderSide(color: codeBorder, width: 3),
          ),
        ),
        blockquotePadding: const EdgeInsets.only(left: 8),
        listBullet: base,
        tableHead: base.copyWith(fontWeight: FontWeight.w700),
        tableBody: base,
        pPadding: const EdgeInsets.only(bottom: 2),
      ),
    );
  }


  Widget _kv(BuildContext ctx, String k, String v, {Color? valueColor}) {
    final isDark = Theme.of(ctx).brightness == Brightness.dark;
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
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
              text: '$k: ',
              style: TextStyle(
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
            TextSpan(
              text: v,
              style: valueColor == null ? null : TextStyle(color: valueColor),
            ),
          ],
        ),
      ),
    );
  }

  Widget _mono(BuildContext ctx, String s, {Color? color}) {
    final isDark = Theme.of(ctx).brightness == Brightness.dark;
    return SelectableText(
      s,
      style: GoogleFonts.jetBrainsMono(
        fontSize: 11,
        color: color ??
            (isDark
                ? DesignColors.textPrimary
                : DesignColors.textPrimaryLight),
      ),
    );
  }

  // Single source of truth lives in feed_reducer.dart so the right-edge
  // minimap can paint ticks in the same colour as the card.
  static Color _accentFor(String kind, String producer) =>
      agentEventAccent(kind, producer);
}

class _AgentEventCardState extends State<AgentEventCard> {
  // Per-card collapse toggle. Default-expanded for every kind so the
  // existing transcript shape is preserved on first render; the user
  // chooses what to fold. Mounted state lives on the State, so the
  // sliver's keyed widgets keep collapsed rows collapsed across
  // scroll-and-back.
  //
  // v1.0.706 polish — orphan tool_result cards (no matching parent
  // tool_call in scope, so they aren't already folded INTO the
  // parent card) default to collapsed. They're noisy by nature
  // (long Bash output, file dumps) and the user is usually scanning
  // for text turns. Failed results collapse too: an errored result can
  // carry a huge body (e.g. a failed `attach` still holds its base64
  // content), so keeping errors expanded blew up the card. The header's
  // failed/error styling still flags it; the user taps to read the body.
  //
  // `system` frames (the system-agent card — sub-agent task_started /
  // task_updated / task_notification + engine-level messages, _systemBody)
  // collapse too: they're background bookkeeping the user scans past, not
  // conversation. The one-line preview keeps the subtype visible; tap to read.
  late bool _collapsed = _defaultCollapsedForKind();

  bool _defaultCollapsedForKind() {
    final kind = (widget.event['kind'] ?? '').toString();
    return kind == 'tool_result' || kind == 'system';
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final kind = (widget.event['kind'] ?? '').toString();
    final producer = (widget.event['producer'] ?? 'agent').toString();
    final payload = (widget.event['payload'] is Map)
        ? (widget.event['payload'] as Map).cast<String, dynamic>()
        : <String, dynamic>{};

    final accent = AgentEventCard._accentFor(kind, producer);
    final bg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;

    return Container(
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: border),
      ),
      padding: const EdgeInsets.fromLTRB(10, 8, 10, 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _CardHeader(
            kind: kind,
            producer: producer,
            accent: accent,
            ts: widget.event['ts']?.toString(),
            copyText: AgentEventCard._copyTextFor(kind, payload, widget.event),
            collapsed: _collapsed,
            onToggleCollapsed: () =>
                setState(() => _collapsed = !_collapsed),
          ),
          const SizedBox(height: 6),
          if (_collapsed)
            _collapsedPreview(context, kind, payload)
          else
            widget._body(context, kind, producer, payload),
        ],
      ),
    );
  }

  // Single-line preview rendered in place of the body when collapsed.
  // Uses the same source string as the copy affordance so what the user
  // sees in the preview is what they'd get on copy — no surprise.
  Widget _collapsedPreview(
    BuildContext ctx,
    String kind,
    Map<String, dynamic> payload,
  ) {
    final isDark = Theme.of(ctx).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final raw = AgentEventCard._copyTextFor(kind, payload, widget.event);
    final firstLine = () {
      final nl = raw.indexOf('\n');
      return nl == -1 ? raw : raw.substring(0, nl);
    }();
    final more = raw.length > firstLine.length;
    final text = firstLine.isEmpty ? '(empty)' : firstLine;
    return Text(
      more ? '$text  …' : text,
      maxLines: 1,
      overflow: TextOverflow.ellipsis,
      style: GoogleFonts.jetBrainsMono(
        fontSize: 11,
        color: muted,
      ),
    );
  }
}


class _CardHeader extends StatelessWidget {
  final String kind;
  final String producer;
  final Color accent;
  final String? ts;
  // Pre-computed clipboard text for this card. Empty disables the
  // copy affordance entirely (e.g. internal placeholders we don't
  // want operators dumping into bug reports).
  final String copyText;
  // Per-card collapse state, hoisted from AgentEventCardState. The
  // chevron rotates and tapping the header (anywhere in the row, not
  // just the chevron) toggles. Both are nullable so the header can
  // still be used by a non-collapsible owner if a future caller
  // wants the same visual without the affordance.
  final bool? collapsed;
  final VoidCallback? onToggleCollapsed;
  const _CardHeader({
    required this.kind,
    required this.producer,
    required this.accent,
    required this.ts,
    this.copyText = '',
    this.collapsed,
    this.onToggleCollapsed,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final hasToggle = onToggleCollapsed != null;
    final row = Row(
      children: [
        Container(
          width: 6,
          height: 6,
          decoration: BoxDecoration(
            color: accent,
            borderRadius: BorderRadius.circular(3),
          ),
        ),
        const SizedBox(width: 6),
        Text(
          kind,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 12,
            fontWeight: FontWeight.w700,
            color: accent,
          ),
        ),
        const SizedBox(width: 6),
        Text(
          producer,
          style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
        ),
        const Spacer(),
        if (ts != null)
          Text(
            _formatTs(ts!),
            style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
          ),
        if (copyText.isNotEmpty) ...[
          const SizedBox(width: 4),
          // Compact copy affordance — small enough to not crowd the
          // header row, large enough to hit on mobile. Tapping copies
          // the pre-computed text and surfaces a SnackBar receipt so
          // the principal knows the action took.
          InkResponse(
            radius: 14,
            onTap: () => _copy(context),
            child: Padding(
              padding: const EdgeInsets.all(2),
              child: Icon(
                Icons.copy_outlined,
                size: 14,
                color: muted,
              ),
            ),
          ),
        ],
        if (hasToggle) ...[
          const SizedBox(width: 4),
          InkResponse(
            radius: 14,
            onTap: onToggleCollapsed,
            child: Padding(
              padding: const EdgeInsets.all(2),
              child: Icon(
                (collapsed ?? false)
                    ? Icons.unfold_more
                    : Icons.unfold_less,
                size: 14,
                color: muted,
              ),
            ),
          ),
        ],
      ],
    );
    if (!hasToggle) return row;
    // Make the whole header row a tap target so users don't have to aim
    // for the chevron — much friendlier on mobile thumbs. The copy and
    // chevron InkResponses above sit on top of this and stop propagation
    // by virtue of their own onTap callbacks.
    return InkWell(
      onTap: onToggleCollapsed,
      borderRadius: BorderRadius.circular(4),
      child: row,
    );
  }

  Future<void> _copy(BuildContext context) async {
    await Clipboard.setData(ClipboardData(text: copyText));
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            'Copied ${kind.isEmpty ? "tile" : kind}',
            style: GoogleFonts.jetBrainsMono(fontSize: 12),
          ),
          duration: const Duration(seconds: 1),
          behavior: SnackBarBehavior.floating,
        ),
      );
    }
  }

  static String _formatTs(String iso) {
    final dt = DateTime.tryParse(iso);
    if (dt == null) return iso;
    final local = dt.toLocal();
    String two(int n) => n < 10 ? '0$n' : '$n';
    return '${two(local.hour)}:${two(local.minute)}:${two(local.second)}';
  }
}
enum _DiffKind { context, insert, delete }

class _DiffLine {
  final _DiffKind kind;
  final String text;
  const _DiffLine({required this.kind, required this.text});
}

/// Color-coded diff view for the `diff` event-card body. Each line
/// renders with a green / red / neutral background — the green-on-add /
/// red-on-delete convention matches what every code review tool uses,
/// so the operator doesn't have to read prefixes (+/-) to parse the
/// change. A line-number gutter on the left reinforces ordering for
/// long diffs.
class _DiffView extends StatelessWidget {
  final List<_DiffLine> lines;
  const _DiffView({required this.lines});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    final mono = GoogleFonts.jetBrainsMono(
      fontSize: 11,
      height: 1.35,
      color: isDark
          ? DesignColors.textPrimary
          : DesignColors.textPrimaryLight,
    );
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final addBg = (isDark
            ? DesignColors.success
            : DesignColors.success)
        .withValues(alpha: isDark ? 0.18 : 0.14);
    final delBg = DesignColors.error.withValues(alpha: isDark ? 0.18 : 0.12);
    final ctxBg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;

    final children = <Widget>[];
    for (var i = 0; i < lines.length; i++) {
      final l = lines[i];
      final bg = switch (l.kind) {
        _DiffKind.insert => addBg,
        _DiffKind.delete => delBg,
        _DiffKind.context => ctxBg,
      };
      final marker = switch (l.kind) {
        _DiffKind.insert => '+',
        _DiffKind.delete => '-',
        _DiffKind.context => ' ',
      };
      final markerColor = switch (l.kind) {
        _DiffKind.insert => DesignColors.success,
        _DiffKind.delete => DesignColors.error,
        _DiffKind.context => mutedColor,
      };
      children.add(Container(
        color: bg,
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 24,
              child: Text(
                '${i + 1}',
                textAlign: TextAlign.right,
                style: mono.copyWith(color: mutedColor),
              ),
            ),
            const SizedBox(width: 6),
            SizedBox(
              width: 12,
              child: Text(
                marker,
                style: mono.copyWith(
                  color: markerColor,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            Expanded(
              child: Text(
                l.text.isEmpty ? ' ' : l.text,
                style: mono,
                softWrap: true,
              ),
            ),
          ],
        ),
      ));
    }
    return Container(
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: border),
      ),
      clipBehavior: Clip.hardEdge,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: children,
      ),
    );
  }
}

