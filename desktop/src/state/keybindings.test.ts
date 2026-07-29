/// Tests for the keybinding helpers (#460): canonical combo strings, exact
/// matching, the bindability rules (no bare printable keys, no ⌘<digit>), and
/// the mac/other display forms. Run locally: `node --test src/state/keybindings.test.ts`
/// from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { comboFromEvent, formatCombo, isBindable, matchCombo } from './keybindings.ts';

const ev = (key: string, m: Partial<{ meta: boolean; ctrl: boolean; alt: boolean; shift: boolean }> = {}) => ({
  key,
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
