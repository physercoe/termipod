import type { ITheme } from '@xterm/xterm';

/// Bundled terminal face. Maple Mono NF CN carries Nerd Font private-use
/// glyphs plus complete Simplified/Traditional Chinese and Japanese coverage;
/// the remaining stack is a safe fallback while the web font finishes loading
/// or if a platform rejects it.
export const TERMINAL_FONT_FAMILY =
  '"Maple Mono NF CN", "JetBrains Mono Variable", "JetBrains Mono", "Cascadia Code", "SF Mono", ui-monospace, "Menlo", "Consolas", "DejaVu Sans Mono", monospace';

/// Catppuccin Mocha's official terminal palette. Keep all sixteen ANSI slots:
/// applications such as tmux, vim, htop and shell prompts use these directly,
/// so changing only foreground/background produces a mismatched half-theme.
export const CATPPUCCIN_MOCHA: ITheme = {
  background: '#1e1e2e',
  foreground: '#cdd6f4',
  cursor: '#f5e0dc',
  cursorAccent: '#1e1e2e',
  selectionBackground: '#585b70',
  black: '#45475a',
  red: '#f38ba8',
  green: '#a6e3a1',
  yellow: '#f9e2af',
  blue: '#89b4fa',
  magenta: '#f5c2e7',
  cyan: '#94e2d5',
  white: '#bac2de',
  brightBlack: '#585b70',
  brightRed: '#f38ba8',
  brightGreen: '#a6e3a1',
  brightYellow: '#f9e2af',
  brightBlue: '#89b4fa',
  brightMagenta: '#f5c2e7',
  brightCyan: '#94e2d5',
  brightWhite: '#a6adc8',
};
