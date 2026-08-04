import { obj, str, type Entity } from '../hub/types.ts';

/// Parsing for the two agent-blocking event kinds — `approval_request` and
/// `attention_request` — into the shapes their cards render (vision-parity R1,
/// plan D-3: questions and approvals are the ONLY two full interactive cards
/// in the Companion feed).
///
/// Pure and bridge-free so `node --test` can pin the contract: the cards
/// themselves are screen-bound and this host has no display, so every claim
/// about what a payload *means* is asserted here rather than eyeballed.
///
/// Three producers write `approval_request`, in three different shapes, and a
/// card that guesses wrong either offers the wrong buttons or posts to the
/// wrong endpoint:
///
///   - ACP / M1 permission (`driver_acp.go` handlePermissionRequest)
///       {request_id, params: {toolCall, options: [{optionId, name}], …}}
///   - claude M4 AskUserQuestion (`claude_code/hooks.go` parkAskUserQuestion)
///       {dialog_type: "user_question", questions: <tool_input>, tool_use_id}
///   - claude M4 PreCompact (`claude_code/hooks.go` hookPreCompact)
///       {dialog_type: "compaction", trigger, custom_instructions,
///        options: ["compact", "defer"]}
///
/// The option shapes differ too — maps for ACP, bare strings for compaction —
/// which is why `readOptions` accepts both. Mobile's approval_cards.dart is
/// the parity reference for every rule here.

export interface ApprovalOption {
  /// What goes on the wire. For ACP this is the agent's own `optionId`, which
  /// the hub trusts as the source of truth over the semantic decision string.
  id: string;
  label: string;
  description?: string;
}

/// A permission ask the agent is blocked on. Answered with `input.approval`.
export interface PermissionSpec {
  form: 'permission';
  requestId: string;
  /// The tool being gated, when the agent named one — this is what the user is
  /// actually deciding about, so a card without it is a blind approval.
  toolSummary?: string;
  options: ApprovalOption[];
  /// True when the agent offered no options and we synthesized allow/deny.
  /// The wire treats the two cases differently: a synthesized decision goes as
  /// a semantic string, an agent-offered one must carry its `option_id`.
  synthesized: boolean;
}

/// An AskUserQuestion the agent is blocked on. Answered with `input.answer`,
/// whose body is the chosen option's LABEL — the hub carved this off from
/// approval precisely because the agent expects the option text, not a verdict.
export interface QuestionSpec {
  form: 'question';
  /// The originating tool_call id; the driver keys the tool_result on it.
  requestId: string;
  header?: string;
  question: string;
  options: ApprovalOption[];
  /// Multi-question payloads are allowed by the SDK but rare. We render the
  /// first and report the remainder so the card can say so instead of
  /// silently dropping them (mobile does the same).
  moreQuestions: number;
}

/// A compaction prompt. NOT inline-answerable: `hookPreCompact` parks a real
/// attention item and blocks on it, and the event carries no attention id, so
/// there is nothing an inline button could legitimately POST to. The card
/// shows the ask and points at the attention dock (D-4 — degrade honestly
/// rather than render a button that resolves nothing).
export interface CompactionSpec {
  form: 'compaction';
  trigger?: string;
  customInstructions?: string;
}

/// A payload none of the above matched. The card falls back to a payload dump,
/// which is what every approval_request got before R1 — but now only the
/// genuinely unrecognised ones.
export interface UnknownSpec {
  form: 'unknown';
}

export type ApprovalSpec = PermissionSpec | QuestionSpec | CompactionSpec | UnknownSpec;

function text(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

/// Read an options array in either shape the producers emit:
///   - `[{optionId|id, name|label, description?}]` — ACP permission options
///   - `["compact", "defer"]` — bare strings, id and label the same
///
/// Entries with no usable id are dropped rather than rendered as a nameless
/// button: a button whose id is empty posts an empty decision.
export function readOptions(raw: unknown): ApprovalOption[] {
  if (!Array.isArray(raw)) return [];
  const out: ApprovalOption[] = [];
  for (const o of raw) {
    if (typeof o === 'string') {
      if (o !== '') out.push({ id: o, label: o });
      continue;
    }
    if (o === null || typeof o !== 'object') continue;
    // `label` is a legitimate id source — AskUserQuestion options carry no id
    // and the label IS the answer body. `name` is NOT: in ACP's shape it is
    // explicitly the human label ("Allow once"), so using it as the id would
    // post prose where the hub expects an option id.
    const m = o as Record<string, unknown>;
    const id = text(m['optionId']) || text(m['id']) || text(m['label']);
    if (id === '') continue;
    const label = text(m['name']) || text(m['label']) || id;
    const description = text(m['description']);
    out.push({ id, label, ...(description !== '' ? { description } : {}) });
  }
  return out;
}

/// The AskUserQuestion question list. The hook forwards claude's whole
/// `tool_input` (`{questions: [...]}`) under `questions`, while the tool_call
/// path carries the array directly — accept both rather than betting on which
/// producer a given session used.
function readQuestionList(payload: Entity): unknown[] {
  const q = payload['questions'];
  if (Array.isArray(q)) return q;
  const inner = obj(payload, 'questions');
  if (inner !== undefined && Array.isArray(inner['questions'])) return inner['questions'];
  return [];
}

/// Classify an `approval_request` payload.
///
/// `dialog_type` is the discriminator when present (claude M4); its absence
/// means the ACP permission shape, which predates the field. A permission
/// without a `request_id` is unanswerable — there is nothing to key the reply
/// on — so it degrades to `unknown` rather than rendering buttons that can
/// only fail.
export function parseApprovalRequest(payload: Entity): ApprovalSpec {
  const dialogType = str(payload, 'dialog_type') ?? '';

  if (dialogType === 'user_question') {
    const list = readQuestionList(payload);
    const first = list.length > 0 && typeof list[0] === 'object' && list[0] !== null
      ? (list[0] as Record<string, unknown>)
      : undefined;
    const requestId = str(payload, 'tool_use_id') ?? str(payload, 'request_id') ?? '';
    if (first === undefined || requestId === '') return { form: 'unknown' };
    const header = text(first['header']);
    return {
      form: 'question',
      requestId,
      ...(header !== '' ? { header } : {}),
      question: text(first['question']),
      options: readOptions(first['options']),
      moreQuestions: list.length - 1,
    };
  }

  if (dialogType === 'compaction') {
    const trigger = str(payload, 'trigger') ?? '';
    const custom = str(payload, 'custom_instructions') ?? '';
    return {
      form: 'compaction',
      ...(trigger !== '' ? { trigger } : {}),
      ...(custom !== '' ? { customInstructions: custom } : {}),
    };
  }

  const requestId = str(payload, 'request_id') ?? '';
  if (requestId === '') return { form: 'unknown' };
  const params = obj(payload, 'params') ?? {};
  const toolCall = obj(params, 'toolCall');
  const toolSummary =
    toolCall === undefined ? '' : text(toolCall['name']) || text(toolCall['title']);
  const offered = readOptions(params['options']);
  return {
    form: 'permission',
    requestId,
    ...(toolSummary !== '' ? { toolSummary } : {}),
    // Fall back to allow/deny so the card still works against agents that
    // skip the options block — the same fallback mobile ships.
    options: offered.length > 0 ? offered : [{ id: 'allow', label: 'allow' }, { id: 'deny', label: 'deny' }],
    synthesized: offered.length === 0,
  };
}

/// An `attention_request` — today always `kind: "auth_required"` from the ACP
/// driver. Deliberately NOT interactive: the remediation is a command on the
/// host (`gemini auth`, `kimi login`), so buttons here would promise a fix the
/// desktop cannot perform. The card's job is to show the reason and the exact
/// remediation the hub already computed.
export interface AttentionSpec {
  kind: string;
  reason?: string;
  remediation?: string;
  methods: ApprovalOption[];
}

export function parseAttentionRequest(payload: Entity): AttentionSpec {
  const reason = str(payload, 'reason') ?? '';
  const remediation = str(payload, 'remediation') ?? '';
  return {
    kind: str(payload, 'kind') ?? 'attention',
    ...(reason !== '' ? { reason } : {}),
    ...(remediation !== '' ? { remediation } : {}),
    methods: readOptions(payload['available_methods']),
  };
}

/// The pending attention items this agent raised, for inline rendering in its
/// own feed (vision-parity R1).
///
/// This closes a real hole rather than adding a convenience. `toolGroups.ts`
/// HIDES a gate `tool_call` (`permission_prompt`, `request_select`,
/// `request_decision`, `request_approval`) on the stated grounds that "the
/// inline attention/approval card already represents the same gesture" — true
/// on mobile, where `interaction_cards.dart` renders attention items inside the
/// transcript. On desktop the AttentionDock is a SEPARATE surface, so the gate
/// was hidden and nothing replaced it: the agent blocked, and its own feed
/// showed no trace of what it was waiting for.
///
/// Scoping, in order of trust:
///   - `pending_payload.agent_id` — stamped by the permission_prompt path,
///     which is exactly the kind whose tool_call gets hidden.
///   - `session_id` — the request_* MCP handlers stamp it; use it only when a
///     session is actually in scope, since an empty session on both sides
///     would match every system-raised row into this agent's feed.
///
/// Resolved rows are excluded: this renders what still needs the user, and a
/// settled decision is already a matter of record in the transcript.
export function pendingAttentionFor(
  items: readonly Entity[],
  agentId: string,
  sessionId?: string,
): Entity[] {
  if (agentId === '') return [];
  return items.filter((it) => {
    // The hub's status vocabulary is 'open' (unresolved) / 'resolved' —
    // `handleListAttention` even defaults its query to status=open. There is
    // no 'pending' status on the wire; testing for one here would exclude
    // every real row and the inline cards would simply never render.
    if ((str(it, 'status') ?? 'open') !== 'open') return false;
    const payload = obj(it, 'pending_payload');
    if (payload !== undefined && str(payload, 'agent_id') === agentId) return true;
    const rowSession = str(it, 'session_id') ?? '';
    return sessionId !== undefined && sessionId !== '' && rowSession === sessionId;
  });
}

/// Whether an attention kind can be decided by the inline card's plain
/// approve/reject pair — or must be sent to the AttentionDock, whose per-kind
/// cards carry the inputs the decision actually needs.
///
/// The split is about what `POST /attention/{id}/decide` requires per kind:
///
///   - `permission_prompt` / `approval_request` are binary by construction
///     (request_approval is "the n=2 sibling of request_select").
///   - `browser_action` / `desktop_action` approve as allow-ONCE; the dock
///     additionally offers browser_action's session grant, which the inline
///     card deliberately omits — a standing grant deserves the full card.
///   - `select` needs an `option_id` (the picked option); approving without
///     one resolves the row but delivers an answer that names no choice.
///   - `help_request` needs a `body` — the hub 400s an approve without one,
///     so an inline approve button could only ever fail.
///
/// Anything unrecognised defers too: a new kind's decide contract is unknown
/// here, and a wrong button is worse than a pointer (D-4).
export function inlineDecidable(kind: string): boolean {
  return (
    kind === 'permission_prompt' ||
    kind === 'approval_request' ||
    kind === 'browser_action' ||
    kind === 'desktop_action'
  );
}

/// What `input.approval` should carry for a chosen option.
///
/// The hub validates `decision` against approve|allow|deny|cancel UNLESS an
/// `option_id` is present, in which case it trusts the option id and forwards
/// it as the selected outcome. So an agent-offered option must send BOTH (its
/// id is not in the semantic vocabulary — `proceed_always_server` would be
/// rejected on its own), while a synthesized allow/deny sends the decision
/// alone. Getting this backwards is a 400 the user sees as "nothing happened".
export function approvalWire(
  spec: PermissionSpec,
  option: ApprovalOption,
): { decision: string; optionId?: string } {
  if (spec.synthesized) return { decision: option.id };
  return { decision: option.id, optionId: option.id };
}
