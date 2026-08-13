import assert from 'node:assert/strict';
import test from 'node:test';
import { CATPPUCCIN_MOCHA, TERMINAL_FONT_FAMILY } from './appearance.ts';

test('terminal font prefers the bundled Maple Mono Nerd Font CN face', () => {
  assert.match(TERMINAL_FONT_FAMILY, /^"Maple Mono NF CN"/);
  assert.match(TERMINAL_FONT_FAMILY, /monospace$/);
});

test('Catppuccin Mocha defines the complete ANSI palette', () => {
  assert.equal(CATPPUCCIN_MOCHA.background, '#1e1e2e');
  assert.equal(CATPPUCCIN_MOCHA.foreground, '#cdd6f4');
  for (const name of [
    'black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white',
    'brightBlack', 'brightRed', 'brightGreen', 'brightYellow', 'brightBlue', 'brightMagenta', 'brightCyan', 'brightWhite',
  ] as const) {
    assert.match(CATPPUCCIN_MOCHA[name] ?? '', /^#[0-9a-f]{6}$/);
  }
});
