/// The claude M2 wire, as pure functions (vision-parity L3a).
///
/// Everything here is a port of `hub/internal/hostrunner/driver_stdio.go` and
/// its launch half, with no process and no clock of its own, so the shapes can
/// be asserted without spawning an engine. `claudechild.ts` is what wires these
/// to a real child.
///
/// The two supplements at the bottom are the ones L2 explicitly could NOT
/// carry: a frame profile renames fields, and these are per-session mutable
/// state (a turn counter, a per-model window learned from an earlier frame).
/// L2's note says they "belong to whatever owns the session", and this is it.

import type { Family } from './families.ts';

// ── Config root ──────────────────────────────────────────────────────────────

/// The env var that relocates claude's entire config home.
export const CONFIG_HOME_ENV = 'CLAUDE_CONFIG_DIR';

/// Resolve claude's config home — the port of
/// `claude_code.ResolveConfigHome` (pathresolver.go:104).
///
/// Order: an explicit per-session override, then the ambient env var, then
/// `<home>/.claude`. Getting this wrong is silent in exactly the way the Go
/// side warns about: the service would spawn a child that reads one root while
/// we look at another, and nothing reports a mismatch.
///
/// The plan calls this root **per-account, not per-machine** — a director with
/// work and personal claude.ai logins has more than one. L3a resolves the ONE
/// root a session is spawned against; enumerating them all is the session
/// catalog, which is L3b.
export function resolveConfigHome(
  override: string | undefined,
  env: Record<string, string | undefined>,
  home: string,
): string {
  const explicit = (override ?? '').trim();
  if (explicit !== '') return explicit;
  const ambient = (env[CONFIG_HOME_ENV] ?? '').trim();
  if (ambient !== '') return ambient;
  return `${home}/.claude`;
}

// ── Tool posture ─────────────────────────────────────────────────────────────

/// Which built-in tools a local child is launched with.
///
/// **This is not the hub's `permission mode`, and the difference is not
/// cosmetic.** Permission mode gates whether a tool call is *approved*, and it
/// does that through `--permission-prompt-tool mcp__<ns>__permission_prompt` —
/// an MCP tool a hub serves. A local session has no hub, so that flag has no
/// target.
///
/// What is left had to be measured rather than assumed, because the obvious
/// guesses are all wrong. Against claude-code 2.1.220, driving
/// `--print --output-format stream-json`:
///
///   - no flag → the child runs Bash without asking. No control frame is
///     emitted, `permission_denials` comes back empty.
///   - `--permission-mode manual` → same. It runs it.
///   - `--permission-mode plan` → **same. It runs it.** Plan mode is not a
///     safety boundary in `--print`; there is no interactive channel for it to
///     hold a plan against.
///   - `--tools <list>` → the excluded tools are absent from the session
///     entirely. The model reports them as unavailable and calls nothing.
///
/// So the only lever that gates a non-interactive child is the tool list, and
/// this type is that list behind three named postures. An allowlist, not a
/// denylist: a denylist fails open for every tool claude adds after this file
/// was written, and "the engine grew a capability" must not silently widen what
/// a local session can do to the director's machine.
export type ToolPosture = 'converse' | 'read_local' | 'unrestricted';

/// The default. Reading the workdir is what makes a co-working Companion
/// useful; writing, executing and reaching the network are what make an
/// unattended one dangerous, and none of the three are here.
export const DEFAULT_TOOL_POSTURE: ToolPosture = 'read_local';

/// Tool names are claude's own, read off a live `system/init` frame's `tools`
/// array rather than from documentation.
const POSTURE_TOOLS: Record<ToolPosture, string[] | null> = {
  // `--tools ""` is claude's documented "disable all tools".
  converse: [],
  // Local reads only. No Write/Edit/NotebookEdit (mutation), no Bash/Task
  // (execution), no WebFetch/WebSearch — those last two are reads too, but
  // they are reads *out*, and an egress path is not what "read local" offers.
  read_local: ['Read', 'Glob', 'Grep'],
  // No flag at all: whatever the engine offers, auto-executed. Never a
  // default — a caller has to name it.
  unrestricted: null,
};

export function isToolPosture(v: unknown): v is ToolPosture {
  return v === 'converse' || v === 'read_local' || v === 'unrestricted';
}

/// The `--tools` argv for a posture, or [] when the posture adds no flag.
export function toolArgs(posture: ToolPosture): string[] {
  const tools = POSTURE_TOOLS[posture];
  if (tools === null) return [];
  // The empty-string form is meaningful ("disable all tools"), so it is passed
  // as a real empty argument rather than skipped.
  return ['--tools', tools.join(',')];
}

// ── Launch ───────────────────────────────────────────────────────────────────

export interface LaunchOptions {
  posture: ToolPosture;
  model?: string;
}

/// Build the child's argv from the family registry plus the session's posture.
///
/// The mode args come from `launch.M2.mode_args` in the generated registry —
/// the same data the hub's launcher reads (ADR-043) — so the flags that make
/// this a bidirectional JSON pipe are not spelled out a second time here.
export function buildLaunchArgs(family: Family, opts: LaunchOptions): string[] {
  const modeArgs = family.launch?.M2?.mode_args ?? [];
  if (modeArgs.length === 0) {
    throw new Error(
      `family ${family.family} declares no launch.M2.mode_args; ` +
        'the child would start interactive and never speak stream-json',
    );
  }
  const args = [...modeArgs, ...toolArgs(opts.posture)];
  if (opts.model !== undefined && opts.model !== '') {
    args.push('--model', opts.model);
  }
  return args;
}

// ── Input frames ─────────────────────────────────────────────────────────────

/// A binary attachment lowered onto an Anthropic content block.
export interface AttachmentInput {
  mime: string;
  /// base64, without a data: prefix.
  data: string;
  filename?: string;
}

export interface InputPayload {
  body?: string;
  images?: AttachmentInput[];
  pdfs?: AttachmentInput[];
  request_id?: string;
  decision?: string;
  note?: string;
  reason?: string;
}

/// The input kinds the local claude driver accepts. A deliberate subset of the
/// Go driver's: `attention_reply` and `attach` are hub concepts (an attention
/// table, a document entity) that a local session has none of, so they are
/// absent rather than stubbed (D-4).
export type InputKind = 'text' | 'approval' | 'answer' | 'cancel';

/// Build the stream-json line for one user-side input — the port of
/// `buildStreamJSONInputFrame` (driver_stdio.go:707).
///
/// Returns the complete line including its trailing newline, because the
/// newline is what makes the child act on it and a caller that forgot would
/// hang waiting for a reply to a message the engine has not seen.
export function buildInputFrame(kind: InputKind, payload: InputPayload): string {
  const content: Record<string, unknown>[] = [];

  switch (kind) {
    case 'text': {
      const body = payload.body ?? '';
      const images = payload.images ?? [];
      const pdfs = payload.pdfs ?? [];
      if (body === '' && images.length === 0 && pdfs.length === 0) {
        throw new Error('local claude: text input has no body and no attachments');
      }
      // Attachments lead, text follows — the same order the Go driver builds,
      // which is the order Anthropic's readers expect for a caption.
      for (const img of images) {
        content.push({
          type: 'image',
          source: { type: 'base64', media_type: img.mime, data: img.data },
        });
      }
      for (const pdf of pdfs) {
        const block: Record<string, unknown> = {
          type: 'document',
          source: { type: 'base64', media_type: pdf.mime, data: pdf.data },
        };
        if (pdf.filename !== undefined && pdf.filename !== '') block.title = pdf.filename;
        content.push(block);
      }
      if (body !== '') content.push({ type: 'text', text: body });
      break;
    }
    case 'approval': {
      const reqId = payload.request_id ?? '';
      const decision = payload.decision ?? '';
      if (reqId === '' || decision === '') {
        throw new Error('local claude: approval missing request_id/decision');
      }
      const note = payload.note ?? '';
      content.push({
        type: 'tool_result',
        tool_use_id: reqId,
        content: note !== '' ? `${decision}: ${note}` : decision,
        is_error: decision === 'deny',
      });
      break;
    }
    case 'answer': {
      const reqId = payload.request_id ?? '';
      const body = payload.body ?? '';
      if (reqId === '' || body === '') {
        throw new Error('local claude: answer missing request_id/body');
      }
      // Carved off `approval` upstream so the agent receives the user's answer
      // verbatim instead of peeling a "decision: note" prefix off it.
      content.push({ type: 'tool_result', tool_use_id: reqId, content: body, is_error: false });
      break;
    }
    case 'cancel': {
      const reason = payload.reason ?? '';
      content.push({ type: 'text', text: `cancel: ${reason !== '' ? reason : 'user requested cancel'}` });
      break;
    }
  }

  return `${JSON.stringify({ type: 'user', message: { role: 'user', content } })}\n`;
}

// ── Session-stateful supplements ─────────────────────────────────────────────

/// Mints the turn boundary a claude session has no native marker for.
///
/// `turn.start` (ADR-038 §3) is emitted at prompt dispatch with a stable id, so
/// the digest folds an explicit turn instead of synthesizing one. Control
/// inputs (approval / answer) continue the turn already in flight and must not
/// open a new one — which is why this is a method call at one call site rather
/// than something the profile could do.
export class TurnClock {
  #seq = 0;

  next(): string {
    this.#seq += 1;
    return `t-${this.#seq}`;
  }

  get issued(): number {
    return this.#seq;
  }
}

/// Remembers the per-model context window a turn reported, and stamps it onto
/// later `usage` payloads — the port of `learnContextWindows` /
/// `stampContextWindow` (driver_stdio.go:531/569).
///
/// stream-json's per-message `usage` block carries token counts and no window
/// at all, so without this the Companion's context ring (R2) has a numerator
/// and no denominator and stays dark on every local session.
///
/// **One deliberate narrowing from the Go original.** Go falls back to
/// `claude_code.ModelContextWindow`, a static table with env overrides and
/// per-name exceptions. That table is not ported: it is a second copy of a
/// heuristic that goes stale silently as models ship, and copying it here would
/// give the local service its own drifting opinion about a number the engine
/// itself reports. The consequence is honest and bounded — a local session's
/// FIRST turn has no window (nothing has reported one yet) and the ring is
/// absent, then every turn after it is stamped from the engine's own
/// `by_model[...].context_window`. Absent beats wrong (D-4).
export class ContextWindows {
  #byModel = new Map<string, number>();

  /// Learn from a `turn.result` payload's `by_model` block. Authoritative:
  /// it is the engine's own number, and it arrives at the END of a turn, so it
  /// serves the turns after it.
  learn(payload: Record<string, unknown>): void {
    const byModel = payload.by_model;
    if (byModel === null || typeof byModel !== 'object' || Array.isArray(byModel)) return;
    for (const [model, raw] of Object.entries(byModel as Record<string, unknown>)) {
      if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) continue;
      const n = positiveInt((raw as Record<string, unknown>).context_window);
      if (n > 0) this.#byModel.set(model, n);
    }
  }

  /// Stamp `context_window` onto a `usage` payload, in place.
  ///
  /// Never overwrites what the frame already carried — the engine outranks our
  /// memory of the engine.
  stamp(payload: Record<string, unknown>): void {
    if (positiveInt(payload.context_window) > 0) return;
    const model = payload.model;
    if (typeof model !== 'string' || model === '') return;
    const learned = this.#byModel.get(model) ?? 0;
    if (learned > 0) payload.context_window = learned;
  }

  /// Route one emitted event through whichever supplement applies. Returns the
  /// payload so callers can chain; mutation is in place either way.
  apply(kind: string, payload: Record<string, unknown>): Record<string, unknown> {
    if (kind === 'turn.result') this.learn(payload);
    else if (kind === 'usage') this.stamp(payload);
    return payload;
  }
}

/// A JSON number read as a positive integer; anything else is 0, which every
/// caller treats as "no value". Mirrors Go's `asPositiveInt`.
function positiveInt(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) && v > 0 ? Math.trunc(v) : 0;
}
