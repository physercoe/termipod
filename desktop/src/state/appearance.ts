import { useEffect } from 'react';
import { create } from 'zustand';

export type UiFontFamily = 'inter' | 'system' | 'mono';

export const UI_FONT_SCALE_MIN = 0.8;
export const UI_FONT_SCALE_MAX = 1.3;
export const UI_FONT_SCALE_STEP = 0.05;

const FONT_KEY = 'termipod.uiFont';
const SCALE_KEY = 'termipod.uiFontScale';

const FONT_STACKS: Record<UiFontFamily, string> = {
  inter: '"Inter Variable", "Inter", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
  system: 'system-ui, -apple-system, "Segoe UI", Roboto, "Noto Sans", sans-serif',
  mono: '"JetBrains Mono Variable", ui-monospace, "SF Mono", "Cascadia Code", Menlo, monospace',
};

const BASE_FONT_SIZES = {
  '--font-size-label': 11,
  '--font-size-caption': 12,
  '--font-size-body-small': 13,
  '--font-size-body': 14,
  '--font-size-subtitle': 16,
  '--font-size-title': 18,
  '--font-size-title-large': 20,
} as const;

function isUiFontFamily(value: unknown): value is UiFontFamily {
  return value === 'inter' || value === 'system' || value === 'mono';
}

export function normalizeUiFontScale(value: unknown): number {
  const parsed = typeof value === 'number' ? value : Number.parseFloat(String(value));
  if (!Number.isFinite(parsed)) return 1;
  const clamped = Math.min(UI_FONT_SCALE_MAX, Math.max(UI_FONT_SCALE_MIN, parsed));
  const steps = Math.round((clamped - UI_FONT_SCALE_MIN) / UI_FONT_SCALE_STEP);
  return Number((UI_FONT_SCALE_MIN + steps * UI_FONT_SCALE_STEP).toFixed(2));
}

export function uiFontStack(font: UiFontFamily): string {
  return FONT_STACKS[font];
}

export function scaledUiFontSizes(scale: number): Record<keyof typeof BASE_FONT_SIZES, string> {
  const normalized = normalizeUiFontScale(scale);
  return Object.fromEntries(
    Object.entries(BASE_FONT_SIZES).map(([token, px]) => [
      token,
      `${(px * normalized).toFixed(2).replace(/\.00$/, '')}px`,
    ]),
  ) as Record<keyof typeof BASE_FONT_SIZES, string>;
}

function initialFont(): UiFontFamily {
  try {
    const value = localStorage.getItem(FONT_KEY);
    if (isUiFontFamily(value)) return value;
  } catch {
    /* ignore */
  }
  return 'inter';
}

function initialScale(): number {
  try {
    return normalizeUiFontScale(localStorage.getItem(SCALE_KEY) ?? 1);
  } catch {
    return 1;
  }
}

interface AppearanceState {
  font: UiFontFamily;
  fontScale: number;
  setFont: (font: UiFontFamily) => void;
  setFontScale: (scale: number) => void;
}

export const useAppearance = create<AppearanceState>((set) => ({
  font: initialFont(),
  fontScale: initialScale(),
  setFont: (font) => {
    try {
      localStorage.setItem(FONT_KEY, font);
    } catch {
      /* ignore */
    }
    set({ font });
  },
  setFontScale: (value) => {
    const fontScale = normalizeUiFontScale(value);
    try {
      localStorage.setItem(SCALE_KEY, String(fontScale));
    } catch {
      /* ignore */
    }
    set({ fontScale });
  },
}));

export function applyAppearance(
  root: HTMLElement,
  font: UiFontFamily,
  fontScale: number,
): void {
  root.dataset.uiFont = font;
  root.dataset.uiFontScale = String(Math.round(normalizeUiFontScale(fontScale) * 100));
  root.style.setProperty('--sans', uiFontStack(font));
  for (const [token, value] of Object.entries(scaledUiFontSizes(fontScale))) {
    root.style.setProperty(token, value);
  }
}

/// Applies persisted typography choices to the semantic CSS token layer.
/// theme-init.js mirrors this before first paint; this hook owns live updates.
export function useApplyAppearance(): void {
  const font = useAppearance((state) => state.font);
  const fontScale = useAppearance((state) => state.fontScale);
  useEffect(() => {
    applyAppearance(document.documentElement, font, fontScale);
  }, [font, fontScale]);
}
