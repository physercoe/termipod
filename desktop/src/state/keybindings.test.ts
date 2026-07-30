/// Tests for the keybinding helpers (#460): canonical combo strings, exact
/// matching, the bindability rules (no bare printable keys, no ⌘<digit>), and
/// the mac/other display forms. Run locally: `node --test src/state/keybindings.test.ts`
/// from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { comboFromEvent, DEFAULT_BINDINGS, formatCombo, isBindable, matchCombo } from './keybindings.ts';

const ev = (key: string, m: Partial<{ meta: boolean; ctrl: boolean; alt: boolean; shift: boolean; code: string }> = {}) => ({
  key,
  code: m.code,
  metaKey: m.meta === true,
  ctrlKey: m.ctrl === true,
  altKey: m.alt === true,
  shiftKey: m.shift === true,
});

test('comboFromEvent: canonical order mod, alt, shift, key', () => {
  assert.equal(comboFromEvent(ev('k', { meta: true })), 'mod+k');
  assert.equal(comboFromEvent(ev('k', { ctrl: true })), 'mod+k'); // Ctrl ≡ mod
  assert.equal(comboFromEvent(ev('K', { meta: true, shift: true })), 'mod+shift+k'); // key lowercased
  assert.equal(comboFromEvent(ev('P', { shift: true, alt: true, ctrl: true })), 'mod+alt+shift+p');
  assert.equal(comboFromEvent(ev('`', { meta: true })), 'mod+`');
  assert.equal(comboFromEvent(ev('.', { meta: true })), 'mod+.');
});

test('comboFromEvent: bare modifier press is not a combo', () => {
  assert.equal(comboFromEvent(ev('Meta', { meta: true })), null);
  assert.equal(comboFromEvent(ev('Control', { ctrl: true })), null);
  assert.equal(comboFromEvent(ev('Shift', { shift: true })), null);
  assert.equal(comboFromEvent(ev('Alt', { alt: true })), null);
});

test('matchCombo: exact — extra modifiers break the match', () => {
  assert.equal(matchCombo(ev('k', { meta: true }), 'mod+k'), true);
  assert.equal(matchCombo(ev('k', { meta: true, shift: true }), 'mod+k'), false);
  assert.equal(matchCombo(ev('k', { meta: true, alt: true }), 'mod+k'), false);
  assert.equal(matchCombo(ev('k'), 'mod+k'), false);
});

test('isBindable: refuses bare printable keys and ⌘<digit>, allows fn keys', () => {
  assert.equal(isBindable('k'), false); // would fire while typing
  assert.equal(isBindable('shift+k'), false);
  assert.equal(isBindable('mod+k'), true);
  assert.equal(isBindable('alt+f4'), true);
  assert.equal(isBindable('mod+1'), false); // job-switching family is fixed
  assert.equal(isBindable('mod+shift+1'), true); // only unshifted digits switch jobs
  assert.equal(isBindable('f5'), true);
  assert.equal(isBindable('f12'), true);
  assert.equal(isBindable('f13'), false);
});

test('formatCombo: mac glyphs joined, others joined with +', () => {
  assert.equal(formatCombo('mod+k', true), '⌘K');
  assert.equal(formatCombo('mod+`', true), '⌘`');
  assert.equal(formatCombo('mod+.', true), '⌘.');
  assert.equal(formatCombo('mod+shift+p', true), '⌘⇧P');
  assert.equal(formatCombo('mod+k', false), 'Ctrl+K');
  assert.equal(formatCombo('alt+f4', false), 'Alt+F4');
  assert.equal(formatCombo('mod+ ', true), '⌘Space');
});

// ── Physical-key normalization (split-pane S2) ────────────────────────────────
// `e.key` reports the SHIFTED character, so Shift+Backslash arrives as '|' on a
// US layout and as something else elsewhere. Without `e.code`, the shipped
// default `mod+shift+\\` could never fire — the bug these tests pin.

test('comboFromEvent: punctuation resolves by code, so a shifted chord is reachable', () => {
  assert.equal(comboFromEvent(ev('|', { ctrl: true, shift: true, code: 'Backslash' })), 'mod+shift+\\');
  assert.equal(comboFromEvent(ev('\\', { ctrl: true, code: 'Backslash' })), 'mod+\\');
  // A layout where Shift+Backslash is not '|' lands on the same combo — that is
  // the point of resolving by physical key.
  assert.equal(comboFromEvent(ev('°', { ctrl: true, shift: true, code: 'Backslash' })), 'mod+shift+\\');
});

test('comboFromEvent: code never changes the existing letter/digit chords', () => {
  assert.equal(comboFromEvent(ev('k', { meta: true, code: 'KeyK' })), 'mod+k');
  assert.equal(comboFromEvent(ev('K', { meta: true, shift: true, code: 'KeyK' })), 'mod+shift+k');
  // Digits are deliberately NOT mapped: `e.key` already gives the unshifted form
  // and remapping them would invalidate combos users captured before this change.
  assert.equal(comboFromEvent(ev('!', { meta: true, shift: true, code: 'Digit1' })), 'mod+shift+!');
  // The pre-existing punctuation chords keep their combos.
  assert.equal(comboFromEvent(ev('.', { meta: true, code: 'Period' })), 'mod+.');
  assert.equal(comboFromEvent(ev('`', { meta: true, code: 'Backquote' })), 'mod+`');
});

test('every non-empty default binding is bindable and matches its own key event', () => {
  // A hand-written default is the one combo no capture UI ever validated, so
  // check each is legal AND actually reachable from the keyboard. `annotate`
  // (D2.1) ships UNBOUND — its '' default is exempt and pinned separately.
  const events: Record<string, ReturnType<typeof ev>> = {
    palette: ev('k', { meta: true, code: 'KeyK' }),
    assistant: ev('.', { meta: true, code: 'Period' }),
    terminal: ev('`', { meta: true, code: 'Backquote' }),
    splitToggle: ev('\\', { meta: true, code: 'Backslash' }),
    splitSwap: ev('|', { meta: true, shift: true, code: 'Backslash' }),
  };
  for (const [action, combo] of Object.entries(DEFAULT_BINDINGS)) {
    if (combo === '') continue;
    assert.equal(isBindable(combo), true, `${action} (${combo}) must be bindable`);
    assert.equal(matchCombo(events[action], combo), true, `${action} (${combo}) must be reachable`);
  }
});

test('annotate ships unbound (D2.1): the empty combo never matches an event', () => {
  assert.equal(DEFAULT_BINDINGS.annotate, '');
  // comboFromEvent yields null (bare modifier) or ≥1 char — never '' — so the
  // unset binding is inert until the user captures a chord.
  assert.equal(matchCombo(ev('a', { meta: true }), ''), false);
  assert.equal(matchCombo(ev('a'), ''), false);
  assert.equal(formatCombo('', true), '');
});

test('formatCombo: the split chords read as backslash', () => {
  assert.equal(formatCombo('mod+\\', true), '⌘\\');
  assert.equal(formatCombo('mod+shift+\\', false), 'Ctrl+Shift+\\');
});
