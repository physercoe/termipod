import { create } from 'zustand';

/// What the Focus (center) region is showing. WS4: an agent transcript; WS6: a
/// project board; null falls back to the activity console.
export type Selection =
  | { type: 'agent'; id: string }
  | { type: 'project'; id: string }
  | null;

interface FocusState {
  selection: Selection;
  selectAgent: (id: string) => void;
  selectProject: (id: string) => void;
  clear: () => void;
}

export const useFocus = create<FocusState>((set) => ({
  selection: null,
  selectAgent: (id) => set({ selection: { type: 'agent', id } }),
  selectProject: (id) => set({ selection: { type: 'project', id } }),
  clear: () => set({ selection: null }),
}));
