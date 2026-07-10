import { useEffect, useState } from 'react';

/// Device-local scratch persistence for the authoring surfaces (J1 notes, J2
/// drafts, J6 records). Round-1 storage is deliberately `localStorage`, not the
/// hub: these are private, in-progress artifacts; promoting them to hub
/// Documents / Deliverables (with run provenance, per
/// `research-tooling-landscape.md`) is a later round. `useDraft` behaves like
/// `useState` but mirrors every change to `localStorage` under a namespaced key.
export function useDraft(key: string, initial = ''): [string, (v: string) => void] {
  const full = `termipod.draft.${key}`;
  const [value, setValue] = useState<string>(() => {
    try {
      return localStorage.getItem(full) ?? initial;
    } catch {
      return initial;
    }
  });
  useEffect(() => {
    try {
      localStorage.setItem(full, value);
    } catch {
      /* ignore */
    }
  }, [full, value]);
  return [value, setValue];
}

/// JSON-backed variant for structured drafts (e.g. the J6 records list).
export function useJsonDraft<T>(key: string, initial: T): [T, (v: T) => void] {
  const full = `termipod.draft.${key}`;
  const [value, setValue] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(full);
      return raw !== null ? (JSON.parse(raw) as T) : initial;
    } catch {
      return initial;
    }
  });
  useEffect(() => {
    try {
      localStorage.setItem(full, JSON.stringify(value));
    } catch {
      /* ignore */
    }
  }, [full, value]);
  return [value, setValue];
}
