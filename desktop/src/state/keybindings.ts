import { create } from 'zustand';

/// User-rebindable **global keyboard shortcuts** (#460). The app-level chords
/// (command palette, assistant dock, terminal dock) used to be hardcoded in
/// AppShell's window keydown listener; they now live in this persisted store so
/// Settings → Keyboard can rebind them, and AppShell matches incoming events
/// against the *current* binding instead of a literal. The ⌘/Ctrl+<n> job
/// switching stays fixed — it is position-based (rail order), like VS Code.
///
/// `annotate` (D2.1) is registered with NO default chord — the user may bind
/// one in Settings → Keyboard, but no shipped hotkey arms the overlay (plan
/// §7 OQ3 keeps that product decision open). An empty-string combo never
/// matches an event (`comboFromEvent` returns null or ≥1 char).
///
/// A combo is a canonical lowercase string: modifiers first (`mod` = ⌘ on macOS
/// / Ctrl elsewhere, then `alt`, then `shift`), then `e.key.toLowerCase()` —
/// e.g. `mod+k`, `mod+shift+p`, `alt+f4`. Exact-match semantics: `mod+k` does
/// NOT fire for ⌘⇧K (shift is part of the combo), unlike the old hardcoded
/// handler which ignored extra modifiers.

export type BindingAction = 'palette' | 'assistant' | 'terminal' | 'splitToggle' | 'splitSwap' | 'annotate';

export const BINDING_ACTIONS: BindingAction[] = ['palette', 'assistant', 'terminal', 'splitToggle', 'splitSwap', 'annotate'];

export const DEFAULT_BINDINGS: Record<BindingAction, string> = {
  palette: 'mod+k',
  assistant: 'mod+.',
  terminal: 'mod+`',
  // VS Code's split-editor chord (`plans/desktop-shell-split-pane.md` §3.3).
  splitToggle: 'mod+\\',
  splitSwap: 'mod+shift+\\',
  // Unbound by default — see the header comment.
  annotate: '',
};

/// Layout-independent base characters for the punctuation keys, by `e.code`.
///
/// `e.key` reports the *shifted* character — Shift+Backslash is `'|'` on a US
/// layout and something else elsewhere — so a hand-written default like
/// `mod+shift+\` could never match, and a captured one would not survive a
/// layout change. `e.code` names the physical key, which is what a chord like
/// "Mod+Shift+Backslash" actually means. Letters and digits are deliberately
/// absent: `e.key` already gives their unshifted form (`'K'` → `'k'`), and
/// mapping them would invalidate bindings users captured under the old scheme.
const CODE_BASE_KEY: Record<string, string> = {
  Backslash: '\\',
  Backquote: '`',
  BracketLeft: '[',
  BracketRight: ']',
  Comma: ',',
  Equal: '=',
  Minus: '-',
  Period: '.',
  Quote: "'",
  Semicolon: ';',
  Slash: '/',
};

const LS_KEY = 'termipod.keybindings.v1';

/// The keyboard-event shape the pure helpers need (structural, so tests and the
/// DOM `KeyboardEvent` both fit).
export interface KeyEventLike {
  key: string;
  /** `KeyboardEvent.code` — the physical key. Optional: absent in tests and for
   *  synthetic callers, where `key` is the only signal. See `CODE_BASE_KEY`. */
  code?: string;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
}

/// Canonical combo for a key event, or `null` for a bare modifier press (the
/// user is still composing — a capture UI keeps listening).
export function comboFromEvent(e: KeyEventLike): string | null {
  const base = e.code !== undefined ? CODE_BASE_KEY[e.code] : undefined;
  const key = (base ?? e.key).toLowerCase();
  if (key === 'meta' || key === 'control' || key === 'alt' || key === 'shift') return null;
  const parts: string[] = [];
  if (e.metaKey || e.ctrlKey) parts.push('mod');
  if (e.altKey) parts.push('alt');
  if (e.shiftKey) parts.push('shift');
  parts.push(key);
  return parts.join('+');
}

/// Does this event exactly match a stored combo? (Extra modifiers break the
/// match — see the header comment.)
export function matchCombo(e: KeyEventLike, combo: string): boolean {
  return comboFromEvent(e) === combo;
}

/// Is a combo allowed as a binding? Bare printable keys are refused (they would
/// fire while typing into any input); ⌘/Ctrl+<digit> is refused (that family is
/// the fixed job-switching chord); function keys are safe bare.
export function isBindable(combo: string): boolean {
  const parts = combo.split('+');
  const key = parts[parts.length - 1];
  // Only the *unshifted* ⌘/Ctrl+<digit> family is taken (AppShell's job switch
  // requires no shift/alt); ⌘⇧1 etc. stay bindable.
  if (parts.includes('mod') && !parts.includes('shift') && !parts.includes('alt') && /^[0-9]$/.test(key)) return false;
  if (parts.includes('mod') || parts.includes('alt')) return true;
  return /^f(?:[1-9]|1[0-2])$/.test(key);
}

/// Display form: `mod+shift+k` → `⌘⇧K` on macOS, `Ctrl+Shift+K` elsewhere.
export function formatCombo(combo: string, mac: boolean): string {
  const mods: Record<string, string> = mac ? { mod: '⌘', alt: '⌥', shift: '⇧' } : { mod: 'Ctrl', alt: 'Alt', shift: 'Shift' };
  const keys: Record<string, string> = { ' ': 'Space', arrowup: '↑', arrowdown: '↓', arrowleft: '←', arrowright: '→', escape: 'Esc' };
  const parts = combo.split('+').map((p) => {
    if (p in mods) return mods[p];
    if (p in keys) return keys[p];
    return p.length === 1 ? p.toUpperCase() : p.charAt(0).toUpperCase() + p.slice(1);
  });
  return mac ? parts.join('') : parts.join('+');
}

function load(): Record<BindingAction, string> {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw !== null) {
      const obj: unknown = JSON.parse(raw);
      if (typeof obj === 'object' && obj !== null) {
        const out = { ...DEFAULT_BINDINGS };
        for (const a of BINDING_ACTIONS) {
          const v = (obj as Record<string, unknown>)[a];
          // Malformed / unbindable persisted values fall back to the default
          // rather than stranding the action on a dead chord.
          if (typeof v === 'string' && isBindable(v)) out[a] = v;
        }
        return out;
      }
    }
  } catch {
    /* no localStorage (node tests) or malformed JSON — defaults */
  }
  return { ...DEFAULT_BINDINGS };
}

interface KeybindingsState {
  bindings: Record<BindingAction, string>;
  setBinding: (action: BindingAction, combo: string) => void;
  resetBindings: () => void;
}

export const useKeybindings = create<KeybindingsState>((set, get) => ({
  bindings: load(),
  setBinding: (action, combo) => {
    const bindings = { ...get().bindings, [action]: combo };
    try {
      localStorage.setItem(LS_KEY, JSON.stringify(bindings));
    } catch {
      /* preference only */
    }
    set({ bindings });
  },
  resetBindings: () => {
    try {
      localStorage.removeItem(LS_KEY);
    } catch {
      /* preference only */
    }
    set({ bindings: { ...DEFAULT_BINDINGS } });
  },
}));
