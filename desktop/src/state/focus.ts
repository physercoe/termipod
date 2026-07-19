import { create } from 'zustand';

/// What a Focus (center) region is showing. WS4: an agent transcript; WS6: a
/// project board; null falls back to the activity console.
/// A focus selection. `name` is the entity's human label, captured at selection
/// time from the nav (which already has it) so the Focus header reads
/// "Agent · builder-3" instead of "Agent · agt_01KTK…" (#316). Optional — falls
/// back to the id when a caller doesn't have the name in scope.
export type Selection =
  | { type: 'agent'; id: string; name?: string }
  | { type: 'project'; id: string; name?: string }
  | { type: 'host'; id: string; name?: string }
  | null;

/// The two tabs that share the centre `FocusRegion` (Fleet and Projects) each keep
/// their OWN selection. Without this, selecting an agent in Fleet and switching to
/// Projects left the Projects tab showing that agent's transcript instead of a
/// project (director report) — one global selection can't mean "the agent I'm
/// driving" and "the project I'm planning" at the same time. A drill-down *within*
/// a tab (a project board → one of its agents) stays in that tab's scope, with
/// `prev` giving the one-step back.
export type FocusScope = 'fleet' | 'projects';

interface ScopeState {
  selection: Selection;
  /// The selection immediately before the current one — a one-step "back" so a
  /// drill-down (project board → an agent's transcript) can return to where it
  /// came from. Cleared once consumed.
  prev: Selection;
}

const EMPTY: ScopeState = { selection: null, prev: null };

interface FocusState {
  fleet: ScopeState;
  projects: ScopeState;
  selectAgent: (scope: FocusScope, id: string, name?: string) => void;
  selectProject: (scope: FocusScope, id: string, name?: string) => void;
  selectHost: (scope: FocusScope, id: string, name?: string) => void;
  back: (scope: FocusScope) => void;
  clear: (scope: FocusScope) => void;
}

/// Push a new selection onto a scope, remembering the prior one for `back`.
function pushed(cur: ScopeState, sel: Selection): ScopeState {
  return { prev: cur.selection, selection: sel };
}

export const useFocus = create<FocusState>((set, get) => ({
  fleet: EMPTY,
  projects: EMPTY,
  selectAgent: (scope, id, name) =>
    set({ [scope]: pushed(get()[scope], { type: 'agent', id, name }) } as Partial<FocusState>),
  selectProject: (scope, id, name) =>
    set({ [scope]: pushed(get()[scope], { type: 'project', id, name }) } as Partial<FocusState>),
  selectHost: (scope, id, name) =>
    set({ [scope]: pushed(get()[scope], { type: 'host', id, name }) } as Partial<FocusState>),
  back: (scope) => set({ [scope]: { selection: get()[scope].prev, prev: null } } as Partial<FocusState>),
  clear: (scope) => set({ [scope]: EMPTY } as Partial<FocusState>),
}));
