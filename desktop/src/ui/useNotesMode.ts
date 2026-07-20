import { useState } from 'react';

/// The notes editor's three view modes (Rich / Source / Preview), persisted to
/// `termipod.read.notesMode` so the choice follows the reader everywhere — the
/// Inspector "Notes" tab (ReadSurface) and the full-width note tab (NoteTab)
/// share the one key. Both used to carry a verbatim copy of this state +
/// pick/persist logic (#322).

export type NotesMode = 'wysiwyg' | 'source' | 'preview';

export function useNotesMode(): [NotesMode, (m: NotesMode) => void] {
  const [mode, setMode] = useState<NotesMode>(
    () => (localStorage.getItem('termipod.read.notesMode') as NotesMode) || 'wysiwyg',
  );
  function pick(m: NotesMode): void {
    setMode(m);
    try {
      localStorage.setItem('termipod.read.notesMode', m);
    } catch {
      /* ignore */
    }
  }
  return [mode, pick];
}
