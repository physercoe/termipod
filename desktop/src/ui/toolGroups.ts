/// Tool-call lineage + grouping substrate (agent-transcript-redesign §6 P1) —
/// the pure, render-free half of the tool-group work: id extraction, the MCP
/// gate-name model, per-call status derivation, the consecutive-run grouping
/// pass, and ACP diffstats. Everything here is a byte-faithful port of the
/// mobile originals (cited per function) and import-free at runtime (type
/// imports are erased), so it runs under `node --test` like the other src
/// leaf modules. feedLens.ts and AgentTranscript.tsx wire these into the
/// live feed; ToolGroupCard.tsx renders them.
import type { Entity } from '../hub/types';
import type { FeedEvent } from './EventCard';

// ---------------------------------------------------------------------------
// Ids
// ---------------------------------------------------------------------------

/// tool_use_id for a tool_call: prefer `tool_use_id` (what the claude-code
/// mapper actually writes, mapper.go:369), then `id`/`toolCallId` for ACP
/// drivers. NB the mobile call side reads only `p['id']` — a latent bug that
/// makes claude-code pairing silently fail; we deliberately do not copy it.
/// (Defined here, re-exported by EventCard.tsx which historically owned it.)
export function callToolId(p: Entity): string | undefined {
  const a = p['tool_use_id'];
  if (typeof a === 'string') return a;
  const b = p['id'];
  if (typeof b === 'string') return b;
  const c = p['toolCallId'];
  return typeof c === 'string' ? c : undefined;
}

/// The parent tool_call id a `tool_call_update` folds into — mobile
/// fold_maps.dart:56 `(p['toolCallId'] ?? p['tool_call_id'])`, toString
/// coercion included.
export function toolCallUpdateParentId(p: Entity): string | undefined {
  const v = p['toolCallId'] ?? p['tool_call_id'];
  if (v === null || v === undefined) return undefined;
  const s = String(v);
  return s === '' ? undefined : s;
}

// ---------------------------------------------------------------------------
// MCP gate names (mobile feed_reducer.dart isGatedToolName + the tool_call
// gate set). A "gate" tool's effect is to open an attention item that already
// renders as an inline card — showing the tool_call card too would
// double-count the same gesture.
// ---------------------------------------------------------------------------

/// The tool_call hide rule's gate set (mobile feed_reducer.dart:877-886):
///   - permission_prompt — claude-code's --permission-prompt-tool contract;
///     rendered as the inline approval card.
///   - request_select — multi-choice; rendered as the inline SELECT card.
///   - request_decision — back-compat: an agent spawned with a stale prompt
///     template may still call request_decision; the server aliases to
///     request_select but the tool_call event keeps the old name. Hide both
///     so the duplicate-card fix covers either spelling.
///   - request_approval — generic ask-for-human-yes/no; rendered as an
///     attention item (no inline card, but the tool_call card is still noisy).
export const GATE_TOOL_NAMES: ReadonlySet<string> = new Set([
  'permission_prompt',
  'request_select',
  'request_decision',
  'request_approval',
]);

/// The update-parent gate set (mobile feed_reducer.dart:772-778
/// `_kGateToolNames`) — the four tool_call gates plus `request_help`. Used by
/// the tool_call_update visibility rule so updates for gated tools fall back
/// to a standalone card when the parent is suppressed.
const GATED_PARENT_TOOL_NAMES: ReadonlySet<string> = new Set([...GATE_TOOL_NAMES, 'request_help']);

function hasGateSuffix(name: string, gates: ReadonlySet<string>): boolean {
  // Bare names also accepted (no `mcp__<server>__` prefix) so alternate
  // engines that surface the same tool names hide too — mobile matches the
  // suffix form `endsWith('__$g')`.
  for (const g of gates) {
    if (name.endsWith(`__${g}`)) return true;
  }
  return false;
}

/// True when [name] is a gate tool_call (bare or `mcp__<server>__`-prefixed)
/// — the tool_call hide rule.
export function isGateToolCallName(name: string): boolean {
  return GATE_TOOL_NAMES.has(name) || hasGateSuffix(name, GATE_TOOL_NAMES);
}

/// True when [name] is a gate tool for the tool_call_update parent check
/// (mobile `isGatedToolName`) — the tool_call gates plus `request_help`.
export function isGatedToolName(name: string): boolean {
  return GATED_PARENT_TOOL_NAMES.has(name) || hasGateSuffix(name, GATED_PARENT_TOOL_NAMES);
}

/// The tool_call_update visibility rule (mobile feed_reducer.dart:902-924):
/// an update folds into the parent tool_call card when there IS a visible
/// parent — rendering the standalone card too would just duplicate the
/// latest status pill the parent already shows. For gated tools the parent
/// is hidden by the gate rule, so the standalone card becomes the only place
/// to see the wire-level result content (e.g. the attention_id + severity
/// payload the agent received). Same fall-through for updates whose
/// toolCallId never had a matching tool_call event (drivers that emit
/// updates without an opening frame). [toolNames] is the in-scope
/// tool_call-id → name map (desktop `useToolMaps` nameById).
export function isToolCallUpdateHidden(p: Entity, toolNames: Map<string, string>): boolean {
  const id = toolCallUpdateParentId(p);
  if (id === undefined) return false;
  const parentName = toolNames.get(id) ?? '';
  return parentName !== '' && !isGatedToolName(parentName);
}

// ---------------------------------------------------------------------------
// Per-call status (mobile event_card.dart:375-389) mapped onto the group
// aggregate's three states (kimi-web ToolGroup.vue: running > error > done).
// ---------------------------------------------------------------------------

export type ToolStatus = 'running' | 'error' | 'done';

function statusString(p: Entity | undefined, key: string): string | undefined {
  if (p === undefined) return undefined;
  const v = p[key];
  return typeof v === 'string' ? v : undefined;
}

/// One call's status, mobile's exact derivation: the latest
/// `tool_call_update`'s status wins, then the tool_call's own status field;
/// when neither carries one, a paired tool_result resolves it
/// (`is_error` → failed, else completed); anything else is still pending.
/// ACP statuses map: failed → error, completed → done, everything else
/// (pending / in_progress) → running.
export function toolStatusOf(
  call: FeedEvent,
  resultById: Map<string, Entity>,
  updateById: Map<string, Entity>,
): ToolStatus {
  const p = call.payload;
  const id = callToolId(p);
  const update = id !== undefined ? updateById.get(id) : undefined;
  const result = id !== undefined ? resultById.get(id) : undefined;
  const updateStatus = statusString(update, 'status') ?? statusString(p, 'status') ?? '';
  const status =
    updateStatus !== ''
      ? updateStatus
      : result !== undefined
        ? result['is_error'] === true
          ? 'failed'
          : 'completed'
        : 'pending';
  if (status === 'failed') return 'error';
  if (status === 'completed') return 'done';
  return 'running';
}

/// The group header's aggregate state — kimi-web ToolGroup.vue's rule:
/// running > error > done (any running call makes the group running; else
/// any error makes it an error; else done). Empty runs report done.
export function aggregateToolStatus(statuses: Iterable<ToolStatus>): ToolStatus {
  let sawError = false;
  for (const s of statuses) {
    if (s === 'running') return 'running';
    if (s === 'error') sawError = true;
  }
  return sawError ? 'error' : 'done';
}

// ---------------------------------------------------------------------------
// Run grouping (kimi-web chatTurnRendering.ts tool-stack rule). Render-layer
// only: reducers (busy, lens, counts) keep working on the raw event list.
// ---------------------------------------------------------------------------

/// One virtual-list row: a single visible event, or a grouped run of ≥2
/// consecutive tool_call events rendered as ONE group card. `key` is the
/// stable Virtuoso item key — the event id for a standalone row, the first
/// call's id (namespaced) for a group, so a growing run keeps its identity
/// (and its user collapse state) as new calls join.
export interface FeedRow {
  key: string;
  events: FeedEvent[];
}

function turnIdOf(ev: FeedEvent): string | undefined {
  const v = ev.payload['turn_id'];
  return typeof v === 'string' && v !== '' ? v : undefined;
}

/// Group CONSECUTIVE tool_call rows in the visible list (post-fold,
/// post-lens, post-hide): a run of ≥2 becomes one group row; a lone call
/// stays standalone. The run breaks at any non-tool_call row, and at a
/// `turn_id` change when both neighbours carry one (the hub stamps turn_id
/// on ACP tool calls — driver_acp.go stampTurnID); a call without a stamp
/// can't be proven to be a different turn, so it joins the run.
export function groupToolCalls(visible: FeedEvent[]): FeedRow[] {
  const rows: FeedRow[] = [];
  let run: FeedEvent[] = [];
  const flush = (): void => {
    if (run.length === 0) return;
    const first = run[0];
    rows.push(run.length === 1 ? { key: first.id, events: [first] } : { key: `grp:${first.id}`, events: [...run] });
    run = [];
  };
  for (const ev of visible) {
    if (ev.kind !== 'tool_call') {
      flush();
      rows.push({ key: ev.id, events: [ev] });
      continue;
    }
    if (run.length > 0) {
      const prevTurn = turnIdOf(run[run.length - 1]);
      const thisTurn = turnIdOf(ev);
      if (prevTurn !== undefined && thisTurn !== undefined && prevTurn !== thisTurn) flush();
    }
    run.push(ev);
  }
  flush();
  return rows;
}

// ---------------------------------------------------------------------------
// Diffstat (kimi-web ToolRow `+A −R`): ACP tool_call / tool_call_update
// `content` arrays carry diff blocks ({type:'diff', oldText, newText}) for
// edit-ish tools. Line counting mirrors DiffBody (empty text → 0 lines).
// ---------------------------------------------------------------------------

export interface DiffStats {
  added: number;
  removed: number;
}

/// Summed +/- line counts across the diff content blocks of a call and its
/// latest update, or undefined when neither carries a diff (most tools).
export function toolDiffStats(call: Entity, update?: Entity): DiffStats | undefined {
  let added = 0;
  let removed = 0;
  let found = false;
  for (const src of [call, update]) {
    if (src === undefined) continue;
    const content = src['content'];
    if (!Array.isArray(content)) continue;
    for (const b of content) {
      if (b === null || typeof b !== 'object') continue;
      const blk = b as Entity;
      if (blk['type'] !== 'diff') continue;
      const oldText = typeof blk['oldText'] === 'string' ? (blk['oldText'] as string) : '';
      const newText = typeof blk['newText'] === 'string' ? (blk['newText'] as string) : '';
      found = true;
      if (oldText !== '') removed += oldText.split('\n').length;
      if (newText !== '') added += newText.split('\n').length;
    }
  }
  return found ? { added, removed } : undefined;
}
