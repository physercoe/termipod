import { create } from 'zustand';

/// Web-tab bookmarks (the Read surface's in-app browser). Persisted to
/// localStorage so a saved site survives a restart; a bookmark is just a
/// `url` + `title`, small enough to inline. Shown on the browser's start-state
/// (empty tab) page and toggled from the browser bar's star button.

export interface Bookmark {
  url: string;
  title: string;
}

const LS_KEY = 'termipod.webtab.bookmarks';

function load(): Bookmark[] {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw === null) return [];
    const arr = JSON.parse(raw) as unknown;
    if (!Array.isArray(arr)) return [];
    return arr.filter(
      (b): b is Bookmark =>
        b !== null && typeof b === 'object' && typeof (b as Bookmark).url === 'string' && typeof (b as Bookmark).title === 'string',
    );
  } catch {
    return [];
  }
}

function persist(bookmarks: Bookmark[]): void {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(bookmarks));
  } catch {
    /* storage unavailable — state still drives the UI */
  }
}

interface BookmarksState {
  bookmarks: Bookmark[];
  /// Add a bookmark (no-op for an empty/blank URL or a URL already saved).
  add: (url: string, title: string) => void;
  remove: (url: string) => void;
}

export const useBookmarks = create<BookmarksState>((set, get) => ({
  bookmarks: load(),
  add: (url, title) => {
    if (url === '' || url === 'about:blank') return;
    if (get().bookmarks.some((b) => b.url === url)) return;
    const next = [...get().bookmarks, { url, title: title.trim() === '' ? url : title.trim() }];
    persist(next);
    set({ bookmarks: next });
  },
  remove: (url) => {
    const next = get().bookmarks.filter((b) => b.url !== url);
    persist(next);
    set({ bookmarks: next });
  },
}));
