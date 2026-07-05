import { create } from 'zustand';

/// What the Focus (center) region is showing. WS3/WS4: a selected agent drives
/// the transcript; null falls back to the activity console.
interface FocusState {
  selectedAgentId: string | null;
  select: (id: string | null) => void;
}

export const useFocus = create<FocusState>((set) => ({
  selectedAgentId: null,
  select: (id) => set({ selectedAgentId: id }),
}));
