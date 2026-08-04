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
///
/// Since vision-parity F1 the two tabs have SEPARATE lifecycles. `started` is
/// the dock's (and so the Companion's); `kimiStarted` is the kimi guest's, and
/// with it the `kimi web` server hold. The Companion is the surface that must
/// always be reachable — it needs a hub, not a kimi install — so nothing about
/// kimi may gate it. The dock is also no longer Electron-only: the kimi tab is
/// (a <webview> guest), the Companion is not.

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

function persistView(view: AssistantView): void {
  try {
    localStorage.setItem(VIEW_KEY, view);
  } catch {
    /* preference only */
  }
}

/// Kimi-attach availability for the annotation overlay (D2.2 amendment): the
/// guest is mounted (`kimiStarted`) and embedded (not detached to its own
/// window). The dock need NOT be open — the kimi tab hides, it never unmounts,
/// so a hidden dock's guest can still take an injection; a successful attach
/// then REVEALS the dock on the kimi tab (`reveal`).
///
/// Since F1 the guest is mounted only when BOTH flags hold — the dock itself
/// (`started`, which mounts the panes) and the kimi tab's own lifecycle
/// (`kimiStarted`, which mounts the <WebPanel> inside the kimi pane). An open
/// dock showing the Companion has started no guest and can take no injection.
export function kimiAttachable(s: { started: boolean; kimiStarted: boolean; detached: boolean }): boolean {
  return s.started && s.kimiStarted && !s.detached;
}

/// Which tab the dock actually shows (vision-parity F1). `view` is the user's
/// persisted preference and may name a tab this machine doesn't have — a
/// stored 'kimi' on a box where kimi-code was never installed, or was
/// uninstalled since. Resolve it here rather than rewriting the preference, so
/// installing kimi later restores the tab the user last chose.
export function effectiveView(view: AssistantView, kimiAvailable: boolean): AssistantView {
  return kimiAvailable ? view : 'companion';
}

interface AssistantState {
  /// Dock visible right now (hide keeps everything mounted + running).
  open: boolean;
  /// The dock has been opened and not explicitly closed — keeps the Companion
  /// (and the kimi tab, when started) mounted while hidden.
  started: boolean;
  /// The kimi tab's OWN lifecycle: its <WebPanel> is mounted, so the backing
  /// `kimi web` server has this dock's hold. Separate from `started` since F1 —
  /// a user who only ever opens the Companion must not spawn a kimi server, and
  /// the Companion must not need one to exist.
  kimiStarted: boolean;
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
  /// Release the kimi tab's hold: unmounts its <WebPanel> (stopping the server
  /// when this was the last hold) and falls back to the Companion. The dock
  /// stays up and the Companion's stream and staged draft are untouched.
  /// Callers confirm first — this stops the daemon.
  closeKimi: () => void;
  /// The explicit dock close: hides it and drops every mount, kimi's included.
  close: () => void;
}

export const useAssistant = create<AssistantState>((set) => ({
  open: false,
  started: false,
  // Resume the last tab's lifecycle: a user whose last tab was kimi gets the
  // pre-F1 behaviour (open the dock → the server starts); one who left it on
  // the Companion never spawns kimi at all.
  kimiStarted: loadView() === 'kimi',
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
  // Selecting the kimi tab is what starts its server — F1's lazy lifecycle.
  // Before F1 the server started with the dock itself, so a Companion-only
  // user paid for a kimi daemon they never looked at.
  setView: (view) => {
    persistView(view);
    set((s) => ({ view, kimiStarted: s.kimiStarted || view === 'kimi' }));
  },
  reveal: (view) =>
    set((s) => ({ open: true, started: true, view, kimiStarted: s.kimiStarted || view === 'kimi' })),
  // Persisted, so the tab the user just walked away from doesn't restart its
  // server on the next launch.
  closeKimi: () => {
    persistView('companion');
    set({ kimiStarted: false, detached: false, view: 'companion' });
  },
  close: () => set({ open: false, started: false, kimiStarted: false, detached: false }),
}));
