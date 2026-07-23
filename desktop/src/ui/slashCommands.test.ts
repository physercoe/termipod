/// P3 slash-picker matching checks (agent-transcript-redesign §6 P3), ported
/// from mobile `agent_compose.dart` `_activeMatch` / `isSlashCommandBody`:
///  - token boundaries: `/` must lead the whitespace-delimited token at the
///    caret; a `/` mid-token (path-like) is not a trigger.
///  - `/` vs `@` non-conflict: an `@` token never fires the slash pool.
///  - slash-stripping normalization: claude ships "/help", ACP hubs "help".
///  - case-insensitive prefix, 8-cap, empty-pool / no-match → null.
///  - the raw-send gate (`isSlashCommandBody`) shape rules.
/// The frontend package has no CI test runner; run locally with
/// `node --test src/ui/slashCommands.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { activeSlashMatch, filterSlashCommands, isSlashCommandBody } from './slashCommands.ts';

const POOL = ['help', 'clear', 'compact', 'model', '/init', '/review'];

test('activeSlashMatch: token at start of input matches', () => {
  const m = activeSlashMatch('/he', 3, POOL);
  assert.ok(m !== null);
  assert.equal(m.query, 'he');
  assert.equal(m.tokenStart, 0);
  assert.equal(m.tokenEnd, 3);
  assert.deepEqual(m.matches, ['help']);
});

test('activeSlashMatch: token after whitespace matches', () => {
  const m = activeSlashMatch('please /cle', 11, POOL);
  assert.ok(m !== null);
  assert.equal(m.tokenStart, 7);
  assert.deepEqual(m.matches, ['clear']);
});

test('activeSlashMatch: bare slash with empty query lists the pool (capped order)', () => {
  const m = activeSlashMatch('/', 1, POOL);
  assert.ok(m !== null);
  assert.equal(m.query, '');
  assert.deepEqual(m.matches, ['help', 'clear', 'compact', 'model', 'init', 'review']);
});

test('activeSlashMatch: slash mid-token (path-like) is not a trigger', () => {
  assert.equal(activeSlashMatch('cat /etc/fo', 11, POOL), null);
  assert.equal(activeSlashMatch('/etc/fo', 7, POOL), null); // no pool prefix match either
});

test('activeSlashMatch: text after the caret does not break the token', () => {
  // Caret sits right after "/he"; trailing args are beyond the cursor.
  const m = activeSlashMatch('/he some args', 3, POOL);
  assert.ok(m !== null);
  assert.equal(m.query, 'he');
  assert.equal(m.tokenEnd, 3);
});

test('activeSlashMatch: whitespace inside the would-be token ends it', () => {
  // Caret after a completed token + space: the token at the caret is empty.
  assert.equal(activeSlashMatch('/help ', 6, POOL), null);
});

test('activeSlashMatch: an @ token belongs to the mention picker, never slash', () => {
  assert.equal(activeSlashMatch('@he', 3, POOL), null);
  assert.equal(activeSlashMatch('mail @a', 7, POOL), null);
});

test('activeSlashMatch: pool entries keep NO leading slash in suggestions', () => {
  // claude shape: "/init" must match "/in" and suggest "init", not "//init".
  const m = activeSlashMatch('/in', 3, POOL);
  assert.ok(m !== null);
  assert.deepEqual(m.matches, ['init']);
});

test('activeSlashMatch: prefix compare is case-insensitive', () => {
  const m = activeSlashMatch('/HE', 3, POOL);
  assert.ok(m !== null);
  assert.deepEqual(m.matches, ['help']);
});

test('activeSlashMatch: matches cap at 8', () => {
  const many = Array.from({ length: 12 }, (_, i) => `cmd${String(i).padStart(2, '0')}`);
  const m = activeSlashMatch('/cmd', 4, many);
  assert.ok(m !== null);
  assert.equal(m.matches.length, 8);
  assert.equal(m.matches[7], 'cmd07');
});

test('activeSlashMatch: empty pool or no prefix match → null', () => {
  assert.equal(activeSlashMatch('/he', 3, []), null);
  assert.equal(activeSlashMatch('/zzz', 4, POOL), null);
});

test('filterSlashCommands: strips one leading slash only, empty query lists all', () => {
  assert.deepEqual(filterSlashCommands('', ['/a', 'b']), ['a', 'b']);
  assert.deepEqual(filterSlashCommands('A', ['/a']), ['a']);
});

test('isSlashCommandBody: accepts engine commands, with and without args', () => {
  assert.equal(isSlashCommandBody('/clear'), true);
  assert.equal(isSlashCommandBody('/compact focus'), true);
  assert.equal(isSlashCommandBody('/model claude-sonnet-4'), true);
  assert.equal(isSlashCommandBody('  /help  '), true);
  assert.equal(isSlashCommandBody('/compact line1\nline2'), true); // multi-line focus
});

test('isSlashCommandBody: accepts dotted catalog names (kimi-code skills)', () => {
  // kimi-code 0.28.1's ACP catalog namespaces skill sub-commands with a
  // dot — the picker offers them, so the raw-send gate must accept them.
  assert.equal(isSlashCommandBody('/sub-skill.review'), true);
  assert.equal(isSlashCommandBody('/sub-skill.consolidate some args'), true);
});

test('isSlashCommandBody: rejects paths, prose, and list markers', () => {
  assert.equal(isSlashCommandBody(''), false);
  assert.equal(isSlashCommandBody('/etc/foo'), false);
  assert.equal(isSlashCommandBody('/ - item'), false);
  assert.equal(isSlashCommandBody('/9lives'), false); // must start with a letter
  assert.equal(isSlashCommandBody('hello /clear'), false);
  assert.equal(isSlashCommandBody('//comment'), false);
});
