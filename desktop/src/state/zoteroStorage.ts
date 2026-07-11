import { create } from 'zustand';

/// An in-memory index of a user-linked Zotero `storage/` folder, so the Read
/// surface can open a reference's PDF locally. Deliberately NOT persisted: it
/// holds live `File` handles (from a directory `<input>`), the bytes never leave
/// the device, and PDFs are far too large for localStorage. The user re-links
/// the folder each session (cheap — indexing reads only filenames, not bytes).
///
/// Zotero lays files out as `storage/<attachment-key>/<filename>`; we key the
/// index by `<attachment-key>/<filename>` (the last two path segments of each
/// picked file), which is exactly what a Reference's `zoteroStorage` resolves to.

interface ZoteroStorageState {
  files: Map<string, File>;
  folderName: string | null;
  count: number;
  linkFolder: (list: FileList) => void;
  clear: () => void;
}

export const useZoteroStorage = create<ZoteroStorageState>((set) => ({
  files: new Map(),
  folderName: null,
  count: 0,

  linkFolder: (list) => {
    const files = new Map<string, File>();
    let root: string | null = null;
    for (const f of Array.from(list)) {
      // webkitRelativePath e.g. "storage/VLABQTMC/Paper.pdf" — index by the last
      // two segments (<key>/<filename>) so it matches regardless of the picked
      // folder's own name.
      const rel = (f as File & { webkitRelativePath?: string }).webkitRelativePath ?? f.name;
      const parts = rel.split('/').filter((p) => p !== '');
      if (root === null && parts.length > 0) root = parts[0];
      if (parts.length >= 2) {
        const keyed = `${parts[parts.length - 2]}/${parts[parts.length - 1]}`;
        files.set(keyed, f);
      }
    }
    set({ files, folderName: root, count: files.size });
  },

  clear: () => set({ files: new Map(), folderName: null, count: 0 }),
}));

/// Resolve a reference's attachment to a live File, or undefined if the folder
/// isn't linked or the file isn't present under it.
export function resolveAttachment(
  files: Map<string, File>,
  att: { key: string; file: string } | undefined,
): File | undefined {
  if (att === undefined) return undefined;
  return files.get(`${att.key}/${att.file}`);
}
