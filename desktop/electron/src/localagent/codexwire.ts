/// The codex app-server wire, as pure functions (vision-parity **L4c**).
///
/// Everything here is shape: JSON-RPC params in, JSON-RPC results out, plus the
/// translation of a server-initiated request into the `approval_request` event
/// R1's inline cards already render. No socket, no process, no clock — so every
/// claim below is asserted in `codexwire.test.ts` rather than eyeballed on a
/// machine with a display. `codexdriver.ts` is what wires these to a channel.
///
/// **Measured against codex-cli 0.147.0, not read off documentation.** Three of
/// the shapes the hub's Go driver sends are rejected by this build, so the
/// probe log is the authority for what is here:
///
///   - `{type:"input_image", image_url}` → `-32600 unknown variant
///     'input_image', expected one of 'text', 'image', 'localImage', 'audio',
///     'localAudio', 'skill', 'mention'`. The accepted image form is
///     `{type:"image", url:"data:<mime>;base64,<data>"}` — probed with a 1×1 png
///     the model then described, so it is carried end to end, not just parsed.
///   - `{type:"input_file", file_data}` → the same error. **There is no PDF
///     variant at all**, which is why `buildTurnInput` refuses one instead of
///     dropping it silently.
///   - `{type:"text", text}` with no `text_elements` IS accepted; the server
///     fills `text_elements: []` itself. The generated type marks the field
///     required, so this is the one place the wire is more forgiving than the
///     schema, and sending an empty array anyway would be inventing UI spans.
///
/// The vendor generates its own protocol — `codex app-server generate-ts --out
/// DIR` — and that generated union is what every shape here was checked against.

import type { AttachmentInput, InputPayload, ToolPosture } from './driver.ts';

/// Who we say we are on `initialize`. `title` is what codex shows a user when
/// it names the client that opened a thread.
export const CODEX_CLIENT_INFO = {
  name: 'termipod-companion',
  title: 'TermiPod',
  version: '0',
} as const;

// ── Posture ──────────────────────────────────────────────────────────────────

/// codex's sandbox mode for a posture, and the approval policy that goes with
/// it. Returned together because they are one decision: a sandbox with a policy
/// that lets the agent ask its way out of it is not the boundary its name
/// claims.
export interface CodexPosture {
  /// `SandboxMode` — `read-only` | `workspace-write` | `danger-full-access`.
  sandbox: string;
  /// `AskForApproval` — `untrusted` | `on-request` | `never` (or a granular
  /// object we do not use).
  approvalPolicy: string;
  /// Present when the posture cannot be kept exactly. Recorded on the
  /// lifecycle event so the transcript states the real boundary rather than
  /// the name we asked for (D-4).
  note?: string;
}

/// **The proof, not the documentation.** Against codex-cli 0.147.0, a thread
/// started with `{sandbox:'read-only', approvalPolicy:'never'}` was asked to
/// create a file in its cwd. It tried, failed, said *"the environment is
/// read-only, so POSTURE-PROBE.txt could not be created"*, and **no file
/// existed on disk afterwards**. The response echoed the lowered policy as
/// `{"type":"readOnly","networkAccess":false}`, so the network half is refused
/// by the same setting.
///
/// This is a different mechanism from claude's, for the same contract: claude
/// enforces `read_local` by which tools exist (`--tools Read,Glob,Grep`), codex
/// by an OS sandbox. One posture, two proofs — and each driver owns its own.
///
/// `approvalPolicy` is `never` on every posture on purpose. The alternative,
/// `untrusted`, makes codex ask to retry outside the sandbox when a command
/// fails (measured — it emits `item/commandExecution/requestApproval` with
/// `reason: "command failed; retry without sandbox?"`), which would turn
/// `read_local` into "read local until the director clicks accept". A posture
/// whose boundary can be clicked away is not the boundary claude's posture of
/// the same name is.
export function codexPosture(posture: ToolPosture): CodexPosture {
  switch (posture) {
    case 'unrestricted':
      return { sandbox: 'danger-full-access', approvalPolicy: 'never' };
    case 'converse':
      // codex has no tool-disable switch — nothing in `ThreadStartParams`
      // corresponds to claude's `--tools ""`. The closest true statement is
      // the read-only sandbox, so that is what it gets, and the difference is
      // RECORDED rather than smoothed over: a director who picked "converse"
      // is entitled to know the agent can still read their files.
      return {
        sandbox: 'read-only',
        approvalPolicy: 'never',
        note: 'codex has no tool-disable switch, so `converse` runs with the read-only sandbox: the agent can still read files in the workspace, but cannot write, execute outside it, or reach the network',
      };
    case 'read_local':
    default:
      return { sandbox: 'read-only', approvalPolicy: 'never' };
  }
}

// ── Handshake + turn params ──────────────────────────────────────────────────

export interface ThreadOptions {
  cwd: string;
  posture: ToolPosture;
  model?: string;
  /// Per-thread config overrides, merged over `config.toml` by codex itself.
  config?: Record<string, unknown>;
}

/// `thread/start` params.
///
/// Note what is NOT here: a written `.codex/config.toml`. `ThreadStartParams`
/// carries a `config` map of the same overrides, applied to this thread only,
/// so seeding a session's configuration never means writing a file into the
/// director's project — a file we would then own the lifecycle of, and which
/// would outlive the session that wanted it.
export function threadStartParams(opts: ThreadOptions): Record<string, unknown> {
  const posture = codexPosture(opts.posture);
  const params: Record<string, unknown> = {
    cwd: opts.cwd,
    sandbox: posture.sandbox,
    approvalPolicy: posture.approvalPolicy,
  };
  if (opts.model !== undefined && opts.model !== '') params.model = opts.model;
  if (opts.config !== undefined && Object.keys(opts.config).length > 0) params.config = opts.config;
  return params;
}

/// `thread/resume` params. The overrides are re-sent because a resumed thread
/// takes the ones it is given, not the ones it had — a resume that omitted the
/// sandbox would silently reopen the session under codex's own default.
export function threadResumeParams(threadId: string, opts: ThreadOptions): Record<string, unknown> {
  return { threadId, ...threadStartParams(opts) };
}

/// One `UserInput` block for an image attachment.
function imageInput(att: AttachmentInput): Record<string, unknown> {
  return { type: 'image', url: `data:${att.mime};base64,${att.data}` };
}

/// Build `turn/start.input` from a director's message.
///
/// Attachments lead and text follows — the order a caption reads in, and the
/// order the hub's driver already builds.
///
/// A PDF **throws** rather than being dropped. codex 0.147.0's `UserInput`
/// union has no file variant, so there is no shape that could carry one; a
/// silent drop would send the agent a question about a document it never
/// received, which is the failure mode that is worst to debug. The composer
/// should never offer it in the first place — the family registry's
/// `prompt_pdf.M2` is what gates that, and this is the backstop for an
/// out-of-band caller.
export function buildTurnInput(payload: InputPayload): Record<string, unknown>[] {
  const body = payload.body ?? '';
  const images = payload.images ?? [];
  const pdfs = payload.pdfs ?? [];
  if (pdfs.length > 0) {
    throw new Error(
      'local codex: this build of codex accepts no file attachments — ' +
        "turn/start rejects `input_file` with \"unknown variant\", and its UserInput union is text|image|localImage|audio|localAudio|skill|mention",
    );
  }
  if (body === '' && images.length === 0) {
    throw new Error('local codex: text input has no body and no attachments');
  }
  const input: Record<string, unknown>[] = images.map(imageInput);
  if (body !== '') input.push({ type: 'text', text: body });
  return input;
}

// ── Server-initiated requests ────────────────────────────────────────────────

/// How a parked request can be answered by the Companion.
///
///  - `approval` — a yes/no gate. Renders as R1's PermissionCard, answered by
///    `input.approval`.
///  - `question` — the agent asking the director something, with options.
///    Renders as R1's QuestionCard, answered by `input.answer`.
///  - `unsupported` — we cannot honestly present it, so it is refused
///    immediately with the shape codex expects and a `system` row saying why.
///    The alternative is a card that can never be answered, which parks the
///    engine forever: a stub, which D-4 forbids.
export type CodexAskForm = 'approval' | 'question' | 'unsupported';

export interface CodexAskOption {
  id: string;
  label: string;
  description?: string;
}

export interface CodexAsk {
  method: string;
  form: CodexAskForm;
  /// What the transcript event carries as `request_id`, and what comes back on
  /// `input.approval` / `input.answer`.
  requestId: string;
  summary: string;
  options: CodexAskOption[];
  /// `question` form: the id the answers map must be keyed on.
  questionId?: string;
  /// `unsupported` form: the sentence the `system` row carries.
  note?: string;
}

const APPROVAL_METHODS = new Set([
  'item/commandExecution/requestApproval',
  'item/fileChange/requestApproval',
]);

/// Decisions we will offer, in the order a card should show them, mapped to the
/// label the button carries.
///
/// **Object-shaped decisions are deliberately omitted.**
/// `CommandExecutionApprovalDecision` also admits
/// `{acceptWithExecpolicyAmendment}` and `{applyNetworkPolicyAmendment}` — both
/// mint a STANDING policy from a single click, which is the same class of
/// decision R1's `inlineDecidable` already refuses to put on an inline card
/// (browser_action's session grant). `acceptForSession` is offered only when
/// codex itself advertises it, and reads as what it is.
const DECISION_LABELS: Record<string, string> = {
  accept: 'accept',
  acceptForSession: 'accept for session',
  decline: 'decline',
  cancel: 'cancel',
};
const ACCEPT_ORDER = ['accept', 'acceptForSession'];
const REFUSE_ORDER = ['decline', 'cancel'];

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

function record(v: unknown): Record<string, unknown> | undefined {
  return v !== null && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : undefined;
}

/// The string variants of `availableDecisions`, if the server sent it.
///
/// It is on the wire (probed: `["accept", {"acceptWithExecpolicyAmendment":
/// …}, "cancel"]`) but **not** in the generated `CommandExecutionRequest
/// ApprovalParams`, so it is read defensively: an absent field means "offer
/// the defaults", never "offer nothing". Notice that probe — the advertised
/// refusal there was `cancel`, not `decline`, which is exactly why the
/// refusal button is chosen from what was advertised rather than hard-coded.
function advertisedDecisions(params: Record<string, unknown>): string[] | undefined {
  const raw = params.availableDecisions;
  if (!Array.isArray(raw)) return undefined;
  const out = raw.filter((d): d is string => typeof d === 'string');
  return out.length > 0 ? out : undefined;
}

function approvalOptions(params: Record<string, unknown>): CodexAskOption[] {
  const advertised = advertisedDecisions(params);
  const pick = (order: string[]): CodexAskOption[] => {
    const ids = advertised === undefined ? order.slice(0, 1) : order.filter((id) => advertised.includes(id));
    // Nothing advertised that we understand: fall back to the first known id
    // rather than rendering a card with a missing half.
    const chosen = ids.length > 0 ? ids : order.slice(0, 1);
    return chosen.map((id) => ({ id, label: DECISION_LABELS[id] ?? id }));
  };
  return [...pick(ACCEPT_ORDER), ...pick(REFUSE_ORDER)];
}

function approvalSummary(method: string, params: Record<string, unknown>): string {
  const reason = str(params.reason);
  if (method === 'item/commandExecution/requestApproval') {
    const cmd = str(params.command);
    const head = cmd !== '' ? `Run: ${cmd}` : 'Run a command';
    return reason !== '' ? `${head} — ${reason}` : head;
  }
  const head = 'Apply a file change';
  const root = str(params.grantRoot);
  const withRoot = root !== '' ? `${head} under ${root}` : head;
  return reason !== '' ? `${withRoot} — ${reason}` : withRoot;
}

/// Whether an elicitation is codex asking permission for an MCP tool call
/// rather than forwarding a real form fill. A permission gate collects no
/// input, so it has an empty schema; a form fill describes its fields.
function isToolCallGate(params: Record<string, unknown>): boolean {
  const meta = record(params._meta);
  if (meta !== undefined && str(meta.codex_approval_kind) === 'mcp_tool_call') return true;
  const schema = record(params.requestedSchema);
  if (schema === undefined) return true;
  const props = record(schema.properties);
  return props === undefined || Object.keys(props).length === 0;
}

function elicitationAsk(requestId: string, params: Record<string, unknown>): CodexAsk {
  const server = str(params.serverName) || str(params.server);
  const message = str(params.message);
  const mode = str(params.mode);
  const base = { method: 'mcpServer/elicitation/request', requestId };

  if (mode === 'url') {
    return {
      ...base,
      form: 'unsupported',
      summary: message !== '' ? message : `${server || 'An MCP server'} asked the user to open a URL`,
      options: [],
      note: `declined a url-mode elicitation from ${server || 'an MCP server'}: opening a browser on the director's behalf is a decision this driver does not make on its own`,
    };
  }
  if (!isToolCallGate(params)) {
    return {
      ...base,
      form: 'unsupported',
      summary: message !== '' ? message : `${server || 'An MCP server'} asked for input`,
      options: [],
      note: `declined a form elicitation from ${server || 'an MCP server'}: it wants structured fields, and the Companion's inline cards answer with a chosen option, not a filled form`,
    };
  }
  return {
    ...base,
    form: 'approval',
    summary: message !== '' ? message : `${server || 'An MCP server'} wants to run a tool`,
    options: [
      { id: 'accept', label: DECISION_LABELS.accept },
      { id: 'decline', label: DECISION_LABELS.decline },
    ],
  };
}

function userInputAsk(requestId: string, params: Record<string, unknown>): CodexAsk {
  const questions = Array.isArray(params.questions) ? params.questions : [];
  const first = record(questions[0]);
  if (first === undefined) {
    return {
      method: 'item/tool/requestUserInput',
      form: 'unsupported',
      requestId,
      summary: 'The agent asked a question with no content',
      options: [],
      note: 'declined a requestUserInput carrying no questions',
    };
  }
  // A secret answer must never take this path. `service.input()` RECORDS every
  // input into the durable transcript before sending it, so answering a secret
  // question through the card would write the secret to disk in plain text —
  // a place the director never chose to put it and cannot easily unwrite.
  if (first.isSecret === true) {
    return {
      method: 'item/tool/requestUserInput',
      form: 'unsupported',
      requestId,
      summary: str(first.question) || 'The agent asked for a secret',
      options: [],
      note: 'declined a secret question: an answer sent through the Companion is written to the session transcript on disk, which is not where a secret belongs',
    };
  }
  const rawOptions = Array.isArray(first.options) ? first.options : [];
  const options: CodexAskOption[] = [];
  for (const o of rawOptions) {
    const m = record(o);
    if (m === undefined) continue;
    const label = str(m.label);
    if (label === '') continue;
    const description = str(m.description);
    options.push({ id: label, label, ...(description !== '' ? { description } : {}) });
  }
  if (options.length === 0) {
    return {
      method: 'item/tool/requestUserInput',
      form: 'unsupported',
      requestId,
      summary: str(first.question) || 'The agent asked an open question',
      options: [],
      note: "declined an open question: the Companion's question card answers with one of the offered options, and this one offered none",
    };
  }
  return {
    method: 'item/tool/requestUserInput',
    form: 'question',
    requestId,
    summary: str(first.question),
    options,
    questionId: str(first.id),
    ...(questions.length > 1
      ? { note: `${questions.length - 1} further question(s) in this request are not shown` }
      : {}),
  };
}

/// Classify one server-initiated request.
///
/// `requestId` is minted by the caller rather than taken from the JSON-RPC id:
/// the id counter restarts with every connection, so a card left on screen
/// across a rebind could otherwise answer a DIFFERENT request that happens to
/// have reached the same number.
export function parseServerAsk(
  method: string,
  rawParams: unknown,
  requestId: string,
): CodexAsk {
  const params = record(rawParams) ?? {};
  if (APPROVAL_METHODS.has(method)) {
    return {
      method,
      form: 'approval',
      requestId,
      summary: approvalSummary(method, params),
      options: approvalOptions(params),
    };
  }
  if (method === 'mcpServer/elicitation/request') return elicitationAsk(requestId, params);
  if (method === 'item/tool/requestUserInput') return userInputAsk(requestId, params);
  if (method === 'item/permissions/requestApproval') {
    return {
      method,
      form: 'unsupported',
      requestId,
      summary: str(params.reason) || 'The agent asked for additional permissions',
      options: [],
      // The response shape is `{permissions, scope}` — a granted profile, not a
      // verdict. Synthesizing one from a yes/no button would be inventing the
      // grant's contents, so this refuses by granting nothing.
      note: 'granted no additional permissions: this request asks for a permission profile, which is a decision with contents an approve button cannot express',
    };
  }
  return {
    method,
    form: 'unsupported',
    requestId,
    summary: method,
    options: [],
    note: `declined an unbridged server request (${method})`,
  };
}

/// The JSON-RPC result that ANSWERS an ask. `choice` is an option id for the
/// `approval` form and the chosen option's label for `question`.
///
/// Every shape here is the vendor's generated response type for that method;
/// sending the wrong one trips a deserialization error on the codex side that
/// surfaces to the agent as a flat rejection with no explanation.
export function askResult(ask: CodexAsk, choice: string): Record<string, unknown> {
  if (ask.form === 'question') {
    // `ToolRequestUserInputResponse` = `{answers: {[questionId]: {answers:
    // [string]}}}` — a map keyed by question id, each holding a LIST, because
    // a multi-select question returns more than one.
    return { answers: { [ask.questionId ?? '']: { answers: [choice] } } };
  }
  if (ask.method === 'mcpServer/elicitation/request') {
    const accepted = choice === 'accept' || choice === 'acceptForSession';
    return accepted
      ? { action: 'accept', content: {}, _meta: null }
      : { action: 'decline', content: null, _meta: null };
  }
  return { decision: choice };
}

/// The result that REFUSES a request we cannot present, per method.
///
/// Not one shape: `mcpServer/elicitation/request` wants `{action}`, the v2
/// approvals want `{decision}`, `item/permissions/requestApproval` wants a
/// granted profile, and the two legacy v1 methods take a `ReviewDecision`
/// whose refusal is an object. An empty `{}` — the hub's fallback for anything
/// unknown — fails to deserialize on every one of them.
export function refuseResult(method: string): Record<string, unknown> {
  switch (method) {
    case 'mcpServer/elicitation/request':
      return { action: 'decline', content: null, _meta: null };
    case 'item/commandExecution/requestApproval':
    case 'item/fileChange/requestApproval':
      return { decision: 'decline' };
    case 'item/permissions/requestApproval':
      // Granting an empty profile IS the refusal: the response has no
      // decision field, so "no additional permissions" is the only way to say
      // no. `turn` scope keeps even that scoped to the turn that asked.
      return { permissions: {}, scope: 'turn' };
    case 'item/tool/requestUserInput':
      return { answers: {} };
    case 'applyPatchApproval':
    case 'execCommandApproval':
      // The v1 `ReviewDecision` refusal carries the reason with it.
      return { decision: { denied: { rejection: 'the Companion cannot present this request' } } };
    default:
      return {};
  }
}

/// The `approval_request` payload an ask becomes.
///
/// This is R1's ACP-permission shape (`approvalRequest.ts` `parseApprovalRequest`)
/// for the approval form, and its claude AskUserQuestion shape for the question
/// form — deliberately, so codex approvals render through the cards that
/// already ship rather than through a third parser. R1's own comment predicted
/// this: *"A local driver parks nothing: its approvals arrive as
/// `approval_request` events and are answered by the two cards above (plan
/// L4)."*
export function askEventPayload(ask: CodexAsk): Record<string, unknown> {
  if (ask.form === 'question') {
    return {
      dialog_type: 'user_question',
      tool_use_id: ask.requestId,
      questions: [
        {
          header: 'codex',
          question: ask.summary,
          options: ask.options.map((o) => ({
            label: o.label,
            ...(o.description !== undefined ? { description: o.description } : {}),
          })),
        },
      ],
    };
  }
  return {
    request_id: ask.requestId,
    params: {
      toolCall: { name: ask.method, title: ask.summary },
      options: ask.options.map((o) => ({
        optionId: o.id,
        name: o.label,
        ...(o.description !== undefined ? { description: o.description } : {}),
      })),
    },
  };
}

// ── Notifications ────────────────────────────────────────────────────────────

/// codex's streaming-delta notifications, which do NOT traverse the frame
/// profile: they arrive at 50–200× the rate of the completed item they build
/// up, so they are buffered per item and throttle-flushed instead
/// (vision-parity E3). Method spellings observed on the wire include both
/// `.../delta` and camelCase `...Delta`.
export function isDeltaMethod(method: string): boolean {
  return method !== '' && (method.endsWith('/delta') || method.endsWith('Delta'));
}

/// The two deltas that reach the transcript. The rest — reasoning text, raw
/// reasoning content — stay dropped: internal monologue the `agent_events`
/// vocabulary does not surface.
export const AGENT_MESSAGE_DELTA = 'item/agentMessage/delta';
export const COMMAND_OUTPUT_DELTA = 'item/commandExecution/outputDelta';

/// Cap on the cumulative output one running command may carry. Each flush posts
/// the WHOLE buffer — that is what makes a partial self-contained for a client
/// that joins late — so an uncapped buffer would cost O(n²) bytes over a chatty
/// command's life. Past the cap the TAIL is kept: for a running command the
/// newest output is the interesting end, and the authoritative full text
/// arrives with the tool_result anyway.
export const MAX_STREAMED_OUTPUT_BYTES = 32 * 1024;

/// Bound `s` to `max` characters, keeping the end, and cut forward to the next
/// line boundary when one is inside the kept window so no client renders half a
/// line as if it were whole.
export function trimToTail(s: string, max: number): string {
  if (max <= 0 || s.length <= max) return s;
  const tail = s.slice(s.length - max);
  const nl = tail.indexOf('\n');
  return nl >= 0 && nl + 1 < tail.length ? tail.slice(nl + 1) : tail;
}
