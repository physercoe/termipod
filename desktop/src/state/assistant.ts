import { create } from 'zustand';

/// App-level assistant (kimi-web) dock state — the terminal-dock pattern
/// (terminal/store.ts): the dock is a persistent, daemon-like panel that HIDES
/// on toggle rather than closing. `started` flips on first open and stays true
/// until an explicit, confirmed Close — while it's true the dock keeps its
/// <WebPanel> mounted even when hidden, so the embedded SPA (and the backing
/// `kimi web` server it refcounts) keeps running underneath, exactly like
/// terminal sessions under a hidden terminal dock. `detached` mirrors the
/// main-process popped-out window (kimiwebwin.ts).

export type AssistantDockSide = 'bottom' | 'right';

const SIDE_KEY = 'termipod.assistant.dockSide';

function loadSide(): AssistantDockSide {
  try {
    return localStorage.getItem(SIDE_KEY) === 'right' ? 'right' : 'bottom';
  } catch {
    return 'bottom';
  }
}

interface AssistantState {
  /// Dock visible right now (hide keeps everything mounted + running).
  open: boolean;
  /// The panel has been opened and not explicitly closed — keeps the WebPanel
  /// mounted (server running) while hidden.
  started: boolean;
  /// The SPA is popped out into its own native window (dock shows a placeholder).
  detached: boolean;
  dockSide: AssistantDockSide;
  toggle: () => void;
  setOpen: (v: boolean) => void;
  setDetached: (v: boolean) => void;
  setDockSide: (s: AssistantDockSide) => void;
  /// The explicit close: unmounts the panel (releasing the server's last dock
  /// hold) and hides the dock. Callers confirm first — this stops the daemon.
  close: () => void;
}

export const useAssistant = create<AssistantState>((set) => ({
  open: false,
  started: false,
  detached: false,
  dockSide: loadSide(),
  toggle: () => set((s) => ({ open: !s.open, started: s.started || !s.open })),
  setOpen: (v) => set((s) => ({ open: v, started: s.started || v })),
  setDetached: (v) => set({ detached: v }),
  setDockSide: (side) => {
    try {
      localStorage.setItem(SIDE_KEY, side);
    } catch {
      /* preference only */
    }
    set({ dockSide: side });
  },
  close: () => set({ open: false, started: false, detached: false }),
}));
