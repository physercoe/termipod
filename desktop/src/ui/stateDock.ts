/// State-dock derivations (agent-transcript-redesign §6 P2, kimi-web
/// ChatDock.vue parity) — the pure, render-free half of the state dock: from
/// the FULL session event list plus the tool-lineage maps (AgentTranscript's
/// `useToolMaps`), derive the shell-task list, the sub-agent list, and the
/// todo snapshot that the dock chips + panel render. Import-free at runtime
/// except for the equally-pure toolGroups.ts (type imports are erased), so it
/// runs under `node --test` like the other src leaf modules. StateDock.tsx
/// renders the model; AgentTranscript.tsx wires it above the composer.
///
/// INPUT CONTRACT — the full session FeedEvent list, NOT the lens-filtered /
/// visible list: the chips are SESSION state, so a lens change must not move
/// the counts. The dock is state visibility, never feed filtering; the lens
/// system stays untouched (plan §6 P2, goal G2).
///
/// Baseline honesty note (plan §6 P2): kimi-web's dock lists BACKGROUND
/// bash/subagent tasks only — foreground subagents render inline in the
/// transcript. Background-vs-foreground is engine metadata ACP doesn't carry
/// (kimi's own `display` hints arrive only with the P4 wire-tail; claude task
/// frames likewise), so this baseline is the plan's engine-agnostic NAME
/// MATCH: every shell/sub-agent tool_call counts as a dock task while it's
/// running. Foreground sub-agents keep rendering inline in the transcript
/// (unchanged) — the dock is an ambient mirror, not a replacement card.
import type { Entity } from '../hub/types';
import type { FeedEvent } from './EventCard';
import { callToolId, toolStatusOf, type ToolStatus } from './toolGroups.ts';

// ---------------------------------------------------------------------------
// Name matching (the engine-agnostic baseline — plan §6 P2). Case-insensitive;
// a name matches bare (`Bash`) or as an MCP suffix (`mcp__<server>__bash`),
/// the same shape as the P1 gate-name matcher (toolGroups.ts hasGateSuffix):
/// the `__` separator means `tasklist`/`mybash` never false-match.
// ---------------------------------------------------------------------------

/// Shell-task tool names (plan §6 P2 set).
const SHELL_TASK_NAMES: ReadonlySet<string> = new Set([
  'bash',
  'shell',
  'exec',
  'exec_command',
  'run_shell_command',
  'execute_command',
]);

/// Sub-agent tool names (plan §6 P2 set).
const SUBAGENT_NAMES: ReadonlySet<string> = new Set(['agent', 'task']);

function nameMatches(name: string, set: ReadonlySet<string>): boolean {
  const n = name.toLowerCase();
  if (set.has(n)) return true;
  for (const g of set) {
    if (n.endsWith(`__${g}`)) return true;
  }
  return false;
}

/// True when [name] is a shell-task tool_call (bare or `mcp__<server>__`-
/// suffixed), case-insensitive.
export function isShellTaskName(name: string): boolean {
  return nameMatches(name, SHELL_TASK_NAMES);
}

/// True when [name] is a sub-agent tool_call, same matching.
export function isSubagentName(name: string): boolean {
  return nameMatches(name, SUBAGENT_NAMES);
}

// ---------------------------------------------------------------------------
// Key argument (minimal port of EventCard.toolMeta's pick chains — that one
/// lives in a React module, so the pure layer can't import it under
/// `node --test`). Shell rows want the command, agent rows the prompt; paths /
/// patterns / urls cover the rest. Falls back to the first string value, like
/// toolMeta's generic branch.
// ---------------------------------------------------------------------------

const KEY_ARG_KEYS = [
  'command',
  'cmd',
  'script',
  // description before prompt — mobile's toolCallKeyArg picks the short
  // description for a Task/sub-agent call; a Task row must not show the
  // long prompt on one client and the one-liner on the other.
  'description',
  'prompt',
  'task',
  'file_path',
  'path',
  'filepath',
  'file',
  'pattern',
  'query',
  'glob',
  'q',
  'regex',
  'url',
  'uri',
  'dir',
  'directory',
  'text',
] as const;

export function toolCallKeyArg(input: unknown): string | undefined {
  if (input === null || typeof input !== 'object') return undefined;
  const p = input as Entity;
  for (const k of KEY_ARG_KEYS) {
    const v = p[k];
    if (typeof v === 'string' && v !== '') return v;
  }
  return Object.values(p).find((v): v is string => typeof v === 'string' && v !== '');
}

// ---------------------------------------------------------------------------
// The model.
// ---------------------------------------------------------------------------

export type DockKind = 'tasks' | 'subagents' | 'todos';

/// One shell/sub-agent call row: the stable event id, the tool name as wired,
/// its key argument, and the P1 per-call status (running / error / done).
export interface DockCall {
  id: string;
  name: string;
  arg?: string;
  status: ToolStatus;
}

/// One plan entry, normalized: content + the raw status string (ACP:
/// pending | in_progress | completed; 'done' tolerated as completed, matching
/// the plan card's planMark).
export interface DockTodo {
  content: string;
  status: string;
}

export interface StateDockModel {
  /// Running shell-task count (the Tasks chip's n; chip shows iff > 0).
  shellRunning: number;
  /// Running first (feed order), then settled (error/done) most-recent-first,
  /// capped at MAX_DOCK_CALLS.
  shellCalls: DockCall[];
  subagentRunning: number;
  subagentCalls: DockCall[];
  /// The NEWEST plan event's snapshot ({done, total, items}), or undefined
  /// when the session has no plan event at all (no Todos chip then).
  todos?: { done: number; total: number; items: DockTodo[] };
}

/// The panel's per-kind row cap (plan §6 P2: "running first, then recent
/// completed, cap 20"). The RUNNING counts are never capped — the chips read
/// the true counts, the cap only bounds the rendered list.
export const MAX_DOCK_CALLS = 20;

function orderCalls(calls: DockCall[]): DockCall[] {
  const running: DockCall[] = [];
  const settled: DockCall[] = [];
  for (const c of calls) {
    (c.status === 'running' ? running : settled).push(c);
  }
  // Settled newest-first (a forward scan pushed oldest first); running keeps
  // feed order so a task's position is stable while it runs.
  settled.reverse();
  return [...running, ...settled].slice(0, MAX_DOCK_CALLS);
}

function runningCount(calls: DockCall[]): number {
  let n = 0;
  for (const c of calls) {
    if (c.status === 'running') n += 1;
  }
  return n;
}

/// The newest plan event's todo snapshot, or undefined with no plan event.
/// `payload.entries` is a FULL snapshot per plan update, so the latest event
/// alone is authoritative (P1 folds the plan chain into one card from the
/// same events; the dock reads the raw list, so it works either way).
function todoSnapshot(plan: FeedEvent | undefined): StateDockModel['todos'] {
  if (plan === undefined) return undefined;
  const raw = plan.payload['entries'];
  const list = Array.isArray(raw) ? raw : [];
  const items: DockTodo[] = [];
  let done = 0;
  for (const e of list) {
    const entry = (e !== null && typeof e === 'object' ? e : {}) as Entity;
    const content = typeof entry['content'] === 'string' ? (entry['content'] as string) : '';
    const status = typeof entry['status'] === 'string' ? (entry['status'] as string) : 'pending';
    // 'done' tolerated alongside the canonical 'completed' — the plan card's
    // planMark (EventCard.tsx) treats both as finished, and the dock must
    // agree with the card the user sees in the feed.
    if (status === 'completed' || status === 'done') done += 1;
    items.push({ content, status });
  }
  return { done, total: items.length, items };
}

/// Derive the state-dock model from the FULL session event list (see the
/// input contract above) plus the tool-lineage maps. A tool_call's own
/// payload name classifies it; `nameById` is the fallback for a call whose
/// payload dropped the name. Per-call status reuses P1's `toolStatusOf`:
/// the latest tool_call_update status wins, then the call's own status, then
/// a paired tool_result (is_error), else running.
export function deriveStateDock(
  events: FeedEvent[],
  maps: { nameById: Map<string, string>; resultById: Map<string, Entity>; updateById: Map<string, Entity> },
): StateDockModel {
  const shell: DockCall[] = [];
  const subs: DockCall[] = [];
  let plan: FeedEvent | undefined;
  for (const ev of events) {
    if (ev.kind === 'plan') {
      plan = ev; // forward scan — the newest plan event wins
      continue;
    }
    if (ev.kind !== 'tool_call') continue;
    const p = ev.payload;
    const id = callToolId(p);
    const wiredName = typeof p['name'] === 'string' ? (p['name'] as string) : undefined;
    const name = wiredName ?? (id !== undefined ? maps.nameById.get(id) : undefined) ?? '';
    if (name === '') continue;
    const shellHit = isShellTaskName(name);
    // The two name sets don't overlap, so a call lands in at most one bucket
    // (shell checked first, belt-and-braces).
    const subHit = !shellHit && isSubagentName(name);
    if (!shellHit && !subHit) continue;
    const call: DockCall = {
      id: ev.id,
      name,
      arg: toolCallKeyArg(p['input']),
      status: toolStatusOf(ev, maps.resultById, maps.updateById),
    };
    (shellHit ? shell : subs).push(call);
  }
  return {
    shellRunning: runningCount(shell),
    shellCalls: orderCalls(shell),
    subagentRunning: runningCount(subs),
    subagentCalls: orderCalls(subs),
    todos: todoSnapshot(plan),
  };
}

/// The chips to render, in dock order (plan §6 P2 visibility rules): Tasks
/// iff a shell task is RUNNING; Sub-agents iff a sub-agent call is RUNNING;
/// Todos iff any plan event exists.
export function visibleDockChips(model: StateDockModel): DockKind[] {
  const kinds: DockKind[] = [];
  if (model.shellRunning > 0) kinds.push('tasks');
  if (model.subagentRunning > 0) kinds.push('subagents');
  if (model.todos !== undefined) kinds.push('todos');
  return kinds;
}
