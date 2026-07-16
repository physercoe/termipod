import { create } from 'zustand';

/// What the Focus (center) region is showing. WS4: an agent transcript; WS6: a
/// project board; null falls back to the activity console.
export type Selection =
  | { type: 'agent'; id: string }
  | { type: 'project'; id: string }
  | { type: 'host'; id: string }
  | null;

interface FocusState {
  selection: Selection;
  /// The selection immediately before the current one — a one-step "back" so a
  /// drill-down (project board → an agent's transcript) can return to where it
  /// came from. Cleared once consumed.
  prev: Selection;
  selectAgent: (id: string) => void;
  selectProject: (id: string) => void;
  selectHost: (id: string) => void;
  back: () => void;
  clear: () => void;
}

export const useFocus = create<FocusState>((set, get) => ({
  selection: null,
  prev: null,
  selectAgent: (id) => set({ prev: get().selection, selection: { type: 'agent', id } }),
  selectProject: (id) => set({ prev: get().selection, selection: { type: 'project', id } }),
  selectHost: (id) => set({ prev: get().selection, selection: { type: 'host', id } }),
  back: () => set({ selection: get().prev, prev: null }),
  clear: () => set({ selection: null, prev: null }),
}));
