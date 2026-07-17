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
  setActive: (id: string) => void;
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
  setActive: (id) => set({ activeId: id }),
  rename: (id, title) => set((s) => ({ tabs: s.tabs.map((t) => (t.id === id ? { ...t, title } : t)) })),
}));
