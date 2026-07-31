import { create } from 'zustand';
import { listen } from '../bridge';
import { isShell } from '../platform';
import type { UiRef } from './uiRef';

/// Agent pointing, renderer half (D6 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.4b, ADR-062 D-5).
///
/// An agent calls `ui_highlight`; main decides (policy bit, rate limit, TTL)
/// and pushes ONE order down this channel. The store holds the live orders and
/// expires them; AgentHighlightOverlay paints them.
///
/// The invariants this half must not break — the highlight is an ANNOTATION,
/// not a control:
///   - it never focuses, scrolls, clicks or types (nothing here calls into
///     the workbench store — contrast uiRefFocus.ts, which runs only from a
///     user's click on a chip);
///   - it is always attributed and always dismissible;
///   - it expires on its own, and the deadline is main's, not the agent's.

export interface HighlightOrder {
  id: string;
  ref: UiRef;
  note: string;
  /// The agent, as shown on the marker. Never empty (main falls back to the
  /// id, then to a generic subject) — an unattributed glow is the failure mode
  /// the plan's risk section names.
  by: string;
  ttl_ms: number;
  at: string;
}

interface AgentHighlightState {
  orders: HighlightOrder[];
  show: (order: HighlightOrder) => void;
  dismiss: (id: string) => void;
  clear: () => void;
}

/// At most this many at once. A rate limit lives main-side too; this is the
/// visual bound — six markers is already a crowded screen, and the oldest
/// giving way keeps the newest (the one being talked about) visible.
const MAX_LIVE = 4;

export const useAgentHighlight = create<AgentHighlightState>((set) => ({
  orders: [],
  show: (order) =>
    set((s) => ({ orders: [...s.orders.filter((o) => o.id !== order.id), order].slice(-MAX_LIVE) })),
  dismiss: (id) => set((s) => ({ orders: s.orders.filter((o) => o.id !== id) })),
  clear: () => set({ orders: [] }),
}));

/// Narrow one pushed payload. Main is our own process, but a renderer store
/// takes nothing on trust from an IPC boundary that an agent's arguments
/// reached (however filtered).
export function asHighlightOrder(value: unknown): HighlightOrder | null {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null;
  const v = value as Record<string, unknown>;
  const ref = v.ref;
  if (ref === null || typeof ref !== 'object' || Array.isArray(ref)) return null;
  const surface = (ref as Record<string, unknown>).surface;
  if (typeof v.id !== 'string' || v.id === '' || typeof surface !== 'string' || surface === '') return null;
  const params = (ref as Record<string, unknown>).params;
  const ttl = typeof v.ttl_ms === 'number' && Number.isFinite(v.ttl_ms) && v.ttl_ms > 0 ? v.ttl_ms : 8000;
  return {
    id: v.id,
    ref: { surface, params: params !== null && typeof params === 'object' ? (params as Record<string, string>) : {} },
    note: typeof v.note === 'string' ? v.note : '',
    by: typeof v.by === 'string' && v.by !== '' ? v.by : 'an agent',
    ttl_ms: ttl,
    at: typeof v.at === 'string' ? v.at : '',
  };
}

/// Subscribe to main's highlight channel (called once from main.tsx). No-op
/// outside the shell — a browser build has no agent pointing at it.
export function initAgentHighlights(): void {
  if (!isShell()) return;
  void listen<unknown>('desktopui_highlight', (event) => {
    const order = asHighlightOrder(event.payload);
    if (order !== null) useAgentHighlight.getState().show(order);
  });
}
