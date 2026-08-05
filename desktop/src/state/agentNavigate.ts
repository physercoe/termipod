import { create } from 'zustand';
// `.ts` extensions so `node --test` resolves the module graph — the rule this
// module owns is the one that moves the user's screen, so it must be testable.
import { focusUiRefWithUndo } from './uiRefFocus.ts';
import type { UiRef } from './uiRef.ts';

/// Agent navigation, renderer half (coworking lane H — `desktop_open`).
///
/// An agent calls `desktop_open`; main decides (the `navigate` policy column, a
/// rate limit) and pushes ONE order down this channel. Here the order is
/// EXECUTED — this is the only module in the app where something an agent said
/// changes what is on the user's screen without a click, so it carries the two
/// things that make that acceptable:
///
///   - **attribution.** The banner names the agent and what it opened. An
///     unattributed jump reads as the app misbehaving, and a user who thinks
///     their tools are glitching stops trusting the ones that are not.
///   - **undo.** `ui_highlight` needs none — it expires on its own. A
///     navigation does not, so the banner carries the reverse of whatever the
///     focus actually changed, and the banner stays until dismissed rather than
///     fading: an undo that times out is an undo for whoever was watching.
///
/// The result goes BACK to main, because the tool's answer has to say how far
/// the reference resolved. "I put you in Replay" and "I opened that episode" are
/// different sentences, and an agent that reports the second when the first
/// happened has told the user something false about their own screen.

export interface NavigateOrder {
  id: string;
  ref: UiRef;
  note: string;
  by: string;
  at: string;
}

/// A navigation the user has not dismissed yet, with its reverse.
export interface NavigateBanner extends NavigateOrder {
  undo: (() => void) | null;
}

interface AgentNavigateState {
  /// At most one. A second navigation replaces the first — the older undo is
  /// dropped rather than stacked, because "put me back" after two agent jumps
  /// means back to before the last one, and offering a chain of undos for
  /// something the user did not do in the first place is a worse UI than
  /// offering one.
  banner: NavigateBanner | null;
  show: (b: NavigateBanner) => void;
  dismiss: () => void;
  /// Reverse the navigation and clear the banner. A no-op undo still clears —
  /// the button must never look stuck.
  undo: () => void;
}

export const useAgentNavigate = create<AgentNavigateState>((set, get) => ({
  banner: null,
  show: (banner) => set({ banner }),
  dismiss: () => set({ banner: null }),
  undo: () => {
    const b = get().banner;
    set({ banner: null });
    b?.undo?.();
  },
}));

/// Narrow one pushed order. Main is our own process, but these fields came from
/// an agent and a renderer store takes nothing on trust across that boundary
/// (the `agentHighlight.ts` discipline).
export function asNavigateOrder(value: unknown): NavigateOrder | null {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null;
  const v = value as Record<string, unknown>;
  const ref = v.ref;
  if (typeof v.id !== 'string' || v.id === '' || ref === null || typeof ref !== 'object' || Array.isArray(ref)) return null;
  const surface = (ref as Record<string, unknown>).surface;
  if (typeof surface !== 'string' || surface === '') return null;
  const params = (ref as Record<string, unknown>).params;
  return {
    id: v.id,
    ref: { surface, params: params !== null && typeof params === 'object' ? (params as Record<string, string>) : {} },
    // Re-clipped here even though main clips: same rule, second wall.
    note: typeof v.note === 'string' ? v.note.slice(0, 140) : '',
    by: typeof v.by === 'string' && v.by !== '' ? v.by : 'an agent',
    at: typeof v.at === 'string' ? v.at : '',
  };
}

/// Perform one order. Exported so `node --test` can drive the whole rule —
/// execute, record the undo, report the depth — against the real stores without
/// an IPC channel.
export function runNavigateOrder(order: NavigateOrder): 'entity' | 'surface' | 'unknown' {
  const outcome = focusUiRefWithUndo(order.ref);
  // A ref that resolved to nothing changed nothing, so there is nothing to
  // attribute and nothing to undo. Showing a banner for it would tell the user
  // an agent moved them when it did not.
  if (outcome.result === 'unknown') return 'unknown';
  useAgentNavigate.getState().show({ ...order, undo: outcome.undo });
  return outcome.result;
}

/// Main → renderer orders; renderer → main replies, correlated by the order id.
///
/// The names live here rather than in the host so both ends read them from one
/// place; the SUBSCRIPTION lives in `agentNavigateHost.ts`, which imports the
/// shell bridge and therefore cannot be loaded by `node --test`.
export const NAVIGATE_EVENT = 'desktopui_open';
export const NAVIGATE_RESULT_COMMAND = 'desktopui_open_result';
