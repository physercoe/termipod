import { create } from 'zustand';

/// The Read surface's open reader/browser/note tabs (coworking lane **G1**).
///
/// These lived in `ReadSurface`'s `useState` until the focus publisher needed
/// them. `ui_policy.ts` has reserved `tabs.*` and `tab.*` on the `read` row
/// since D1, but `assembleRawFocus` never populated either: an agent asked
/// "what am I looking at?" while the user read a paper got the surface name and
/// nothing else. The publisher reads stores synchronously (`sourcesNow()` in
/// uiContext.ts), so surface-local state is structurally unreachable to it —
/// lifting is the fix, not a wrapper.
///
/// Two consequences of the lift, both deliberate:
///
///   - **Tabs now survive a job switch.** `SurfaceView` is a switch, so
///     `ReadSurface` unmounts when the user visits Fleet — and every open PDF
///     went with it. Coming back to find the reader empty was a papercut with
///     no defender; the store fixes it as a side effect.
///   - **They do NOT survive an app restart.** No localStorage: a persisted
///     tab can name an attachment that was since deleted, and healing that is
///     its own wedge. Session lifetime is the honest scope.
///
/// One store for a surface the shell can render twice (split pane) is safe
/// because it cannot: `healPanes` drops a `secondary` equal to `job`, and
/// `pinJob` swaps rather than duplicating (workbench.ts). Read is never on
/// screen twice, so there is never a second tab set to keep apart.

export interface ReadTab {
  id: string;
  kind: 'pdf' | 'web' | 'note';
  refId?: string;
  /// Which attachment of the reference this tab opened.
  attId?: string;
  url?: string;
  title: string;
}

let seq = 0;
function nextTabId(): string {
  seq += 1;
  return `tab${Date.now().toString(36)}${seq}`;
}

interface ReadTabsState {
  tabs: ReadTab[];
  /// `null` = the library view, which is not a tab.
  activeId: string | null;
  /// Append a tab and focus it. Returns the minted id — callers that need to
  /// address the tab afterwards (re-title, close on `onGone`) read it here
  /// rather than re-finding the row.
  open: (tab: Omit<ReadTab, 'id'>) => string;
  close: (id: string) => void;
  setActive: (id: string | null) => void;
  /// Re-title in place — the web tab's guest reports its real page title after
  /// load. An empty title is ignored: a guest between navigations reports one,
  /// and blanking the strip mid-load reads as a broken tab.
  setTitle: (id: string, title: string) => void;
  /// Track the guest's real navigation, so a remount resumes where the user
  /// browsed to rather than snapping back to the URL the tab was opened with.
  setUrl: (id: string, url: string) => void;
}

export const useReadTabs = create<ReadTabsState>((set) => ({
  tabs: [],
  activeId: null,
  open: (tab) => {
    const id = nextTabId();
    set((s) => ({ tabs: [...s.tabs, { ...tab, id }], activeId: id }));
    return id;
  },
  // Closing the active tab falls back to the library rather than to a
  // neighbour: the library is Read's home, and guessing a neighbour would land
  // the user in a document they did not ask for.
  close: (id) =>
    set((s) => ({ tabs: s.tabs.filter((t) => t.id !== id), activeId: s.activeId === id ? null : s.activeId })),
  setActive: (activeId) => set({ activeId }),
  setTitle: (id, title) =>
    set((s) => (title === '' ? s : { tabs: s.tabs.map((t) => (t.id === id ? { ...t, title } : t)) })),
  setUrl: (id, url) =>
    set((s) => (url === '' ? s : { tabs: s.tabs.map((t) => (t.id === id && t.url !== url ? { ...t, url } : t)) })),
}));
