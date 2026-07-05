import { useEffect } from 'react';
import { create } from 'zustand';

export type ThemePref = 'dark' | 'light' | 'system';

const LS_KEY = 'termipod.theme';

function initial(): ThemePref {
  try {
    const v = localStorage.getItem(LS_KEY);
    if (v === 'dark' || v === 'light' || v === 'system') return v;
  } catch {
    /* ignore */
  }
  return 'dark';
}

interface ThemeState {
  pref: ThemePref;
  setPref: (p: ThemePref) => void;
}

export const useTheme = create<ThemeState>((set) => ({
  pref: initial(),
  setPref: (pref) => {
    try {
      localStorage.setItem(LS_KEY, pref);
    } catch {
      /* ignore */
    }
    set({ pref });
  },
}));

function resolve(pref: ThemePref): 'dark' | 'light' {
  if (pref === 'system') {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }
  return pref;
}

/// Applies the current theme to `<html data-theme>` and tracks the OS setting
/// when the pref is `system`. Call once at the app root.
export function useApplyTheme(): void {
  const pref = useTheme((s) => s.pref);
  useEffect(() => {
    const apply = (): void => {
      document.documentElement.dataset.theme = resolve(pref);
    };
    apply();
    if (pref === 'system') {
      const mq = window.matchMedia('(prefers-color-scheme: dark)');
      mq.addEventListener('change', apply);
      return () => mq.removeEventListener('change', apply);
    }
    return undefined;
  }, [pref]);
}
