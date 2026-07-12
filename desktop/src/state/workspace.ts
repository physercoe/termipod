import { create } from 'zustand';

/// The Author (J2) workspace folder — an on-disk directory the director opens to
/// browse and open files as documents (the left file tree). Only the folder
/// *path* is persisted (localStorage); the tree itself is listed on demand via
/// the Rust `workspace_list` command so it always reflects what's on disk.

const LS_KEY = 'termipod.author.workspace';

interface WorkspaceState {
  folder: string | null;
  setFolder: (folder: string | null) => void;
}

function load(): string | null {
  try {
    const v = localStorage.getItem(LS_KEY);
    return v !== null && v !== '' ? v : null;
  } catch {
    return null;
  }
}

export const useWorkspace = create<WorkspaceState>((set) => ({
  folder: load(),
  setFolder: (folder) => {
    try {
      if (folder !== null) localStorage.setItem(LS_KEY, folder);
      else localStorage.removeItem(LS_KEY);
    } catch {
      /* ignore */
    }
    set({ folder });
  },
}));
