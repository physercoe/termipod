import { create } from 'zustand';

/// App-level assistant dock state — the terminal-dock pattern
/// (terminal/store.ts): the dock is a persistent, daemon-like panel that HIDES
/// on toggle rather than closing. `started` flips on first open and stays true
/// until an explicit, confirmed Close — while it's true the dock keeps its
/// <WebPanel> mounted even when hidden, so the embedded SPA (and the backing
/// `kimi web` server it refcounts) keeps running underneath, exactly like
/// terminal sessions under a hidden terminal dock. `detached` mirrors the
/// main-process popped-out window (kimiwebwin.ts).
///
/// The dock is TABBED (the unified assistant dock): `view` picks the kimi-web
/// tab or the Companion tab (the dock's AgentCompanion); both render once
/// `started` and are CSS-hidden by `view`, never unmounted. `view` persists
/// like `dockSide`.

export type AssistantDockSide = 'bottom' | 'right';
export type AssistantView = 'kimi' | 'companion';

const SIDE_KEY = 'termipod.assistant.dockSide';
const VIEW_KEY = 'termipod.assistant.view';

function loadSide(): AssistantDockSide {
  try {
    return localStorage.getItem(SIDE_KEY) === 'right' ? 'right' : 'bottom';
  } catch {
    return 'bottom';
  }
}

/// Exported for `node --test` (the store reads it once at module load, before
/// a test can stub localStorage).
export function loadView(): AssistantView {
  try {
    return localStorage.getItem(VIEW_KEY) === 'companion' ? 'companion' : 'kimi';
  } catch {
    return 'kimi';
  }
}

/// Kimi-attach availability for the annotation overlay (D2.2 amendment): the
/// guest is mounted (`started`) and embedded (not detached to its own window).
/// The dock need NOT be open — the kimi tab hides, it never unmounts, so a
/// hidden dock's guest can still take an injection; a successful attach then
/// REVEALS the dock on the kimi tab (`reveal`).
export function kimiAttachable(s: { started: boolean; detached: boolean }): boolean {
  return s.started && !s.detached;
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
  /// Which tab the dock shows: the embedded kimi web SPA or the Companion.
  view: AssistantView;
  toggle: () => void;
  setOpen: (v: boolean) => void;
  setDetached: (v: boolean) => void;
  setDockSide: (s: AssistantDockSide) => void;
  setView: (v: AssistantView) => void;
  /// Reveal the dock on a tab — open + started (mounts the dock if it never
  /// opened) + switch the tab. The annotation flows land the user in the
  /// right composer: 'kimi' after a kimi attach, 'companion' when a handoff
  /// goes to the dock companion.
  reveal: (v: AssistantView) => void;
  /// The explicit close: unmounts the panel (releasing the server's last dock
  /// hold) and hides the dock. Callers confirm first — this stops the daemon.
  close: () => void;
}

export const useAssistant = create<AssistantState>((set) => ({
  open: false,
  started: false,
  detached: false,
  dockSide: loadSide(),
  view: loadView(),
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
  setView: (view) => {
    try {
      localStorage.setItem(VIEW_KEY, view);
    } catch {
      /* preference only */
    }
    set({ view });
  },
  reveal: (view) => set({ open: true, started: true, view }),
  close: () => set({ open: false, started: false, detached: false }),
}));
