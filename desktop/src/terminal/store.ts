import { create } from 'zustand';
import { sessionClose, type TermKind } from './backend';

/// App-scoped state for the persistent terminal dock (professional-terminal, →
/// ADR-053). The dock is a *view* over these live sessions, not their owner: it
/// mounts once for the app's lifetime and shows/hides via CSS, so sessions and
/// scrollback survive toggling the dock and switching tabs. Ownership lives here
/// (and in the native core) rather than in a React component that unmounts.

export interface TermTab {
  /** Stable UI id (t1, t2…), independent of the backend session id. */
  id: string;
  kind: TermKind;
  /** Backend session id — `s*` for SSH (ssh.rs), `p*` for local (pty.rs). */
  sessionId: string;
  title: string;
  /** For local shells, the shell binary pty.rs launched — gates whether the
   *  POSIX OSC-133 integration applies (cmd.exe / PowerShell can't run it).
   *  Undefined for SSH (remote shell kind is unknown; assumed POSIX). */
  shell?: string;
  /** True when this local session runs an *agent* CLI (claude/codex/…) rather than
   *  a shell — its own TUI owns the screen, so OSC-133 shell integration must never
   *  be injected (it would type the integration script into the agent's prompt). */
  agent?: boolean;
  /** For SSH tabs, the saved-connection id this session came from — lets a dead
   *  session offer one-click reconnect (#319). Undefined for ad-hoc/local. */
  connId?: string;
  /** Set when the session emitted output while this tab was NOT active — drives an
   *  unread-activity dot on the tab, cleared when the tab is focused (#319). */
  unread?: boolean;
}

/** Where the dock (non-Terminal-surface mode) attaches. Persisted. */
export type DockSide = 'bottom' | 'right';
const SIDE_KEY = 'termipod.term.dockSide';
function loadSide(): DockSide {
  try {
    return localStorage.getItem(SIDE_KEY) === 'right' ? 'right' : 'bottom';
  } catch {
    return 'bottom';
  }
}

interface TerminalState {
  open: boolean;
  tabs: TermTab[];
  activeId: string | null;
  dockSide: DockSide;
  toggle: () => void;
  setOpen: (open: boolean) => void;
  setDockSide: (side: DockSide) => void;
  /** Register an already-opened session as a new tab; returns its UI id. */
  addTab: (tab: Omit<TermTab, 'id'>) => string;
  /** Close a tab and tear its session down. */
  closeTab: (id: string) => void;
  /** Point a tab at a freshly-opened backend session (reconnect, #319) — keeps the
   *  tab's UI id + position so its <Screen> stays mounted and just rebinds. */
  replaceSession: (id: string, sessionId: string, shell?: string) => void;
  setActive: (id: string) => void;
  /** Flag a background tab as having new output (no-op for the active tab). */
  markActivity: (id: string) => void;
  rename: (id: string, title: string) => void;
}

let seq = 1;

export const useTerminals = create<TerminalState>((set, get) => ({
  open: false,
  tabs: [],
  activeId: null,
  dockSide: loadSide(),
  toggle: () => set((s) => ({ open: !s.open })),
  setOpen: (open) => set({ open }),
  setDockSide: (side) => {
    try {
      localStorage.setItem(SIDE_KEY, side);
    } catch {
      /* ignore */
    }
    set({ dockSide: side });
  },
  addTab: (tab) => {
    const id = `t${seq++}`;
    set((s) => ({ tabs: [...s.tabs, { ...tab, id }], activeId: id, open: true }));
    return id;
  },
  closeTab: (id) => {
    const tab = get().tabs.find((t) => t.id === id);
    // Best-effort explicit close; the tab's <Screen> unmount also closes (both
    // are idempotent in the core).
    if (tab !== undefined) void sessionClose(tab.kind, tab.sessionId);
    set((s) => {
      const tabs = s.tabs.filter((t) => t.id !== id);
      const activeId =
        s.activeId === id ? (tabs.length > 0 ? tabs[tabs.length - 1].id : null) : s.activeId;
      return { tabs, activeId };
    });
  },
  replaceSession: (id, sessionId, shell) =>
    set((s) => ({
      tabs: s.tabs.map((t) => (t.id === id ? { ...t, sessionId, shell: shell ?? t.shell } : t)),
    })),
  // Focusing a tab clears its unread flag.
  setActive: (id) =>
    set((s) => ({
      activeId: id,
      tabs: s.tabs.map((t) => (t.id === id && t.unread === true ? { ...t, unread: false } : t)),
    })),
  markActivity: (id) =>
    set((s) => {
      // The visible tab is never "unread"; skip the map when nothing changes so
      // a chatty session doesn't churn the store on every byte.
      if (s.activeId === id) return s;
      const tab = s.tabs.find((t) => t.id === id);
      if (tab === undefined || tab.unread === true) return s;
      return { tabs: s.tabs.map((t) => (t.id === id ? { ...t, unread: true } : t)) };
    }),
  rename: (id, title) => set((s) => ({ tabs: s.tabs.map((t) => (t.id === id ? { ...t, title } : t)) })),
}));
