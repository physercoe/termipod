/// Tests for the UIRef grammar (D6 — docs/plans/desktop-ui-context-and-pointing.md
/// §3.4b, ADR-062 D-2): the written form both directions of deixis share. Run
/// locally: `node --test src/state/uiRef.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { formatUiRefUri, linkifyUiRefs, parseUiRefUri, uiRefFromJson, uiRefLabel } from './uiRef.ts';

test('the URI form round-trips', () => {
  const ref = { surface: 'replay', params: { dataset_id: 'ds_1', episode_id: 'ep_2', cursor: '1234' } };
  const uri = formatUiRefUri(ref);
  assert.equal(uri, 'ui://replay?dataset_id=ds_1&episode_id=ep_2&cursor=1234');
  assert.deepEqual(parseUiRefUri(uri), ref);

  // A bare surface is a valid ref: "look at the terminal" needs no ids.
  assert.deepEqual(parseUiRefUri('ui://terminal'), { surface: 'terminal', params: {} });
  assert.equal(formatUiRefUri({ surface: 'terminal', params: {} }), 'ui://terminal');

  // Values that need escaping survive the round trip.
  const path = { surface: 'debug', params: { file: 'src/a b/c.ts', selection: '42,58' } };
  assert.deepEqual(parseUiRefUri(formatUiRefUri(path)), path);
});

test('the parser refuses what it cannot govern', () => {
  // The surface keys the policy table — a ref we cannot key is a ref we
  // cannot govern, so it is not a ref.
  assert.equal(parseUiRefUri('ui://'), null);
  assert.equal(parseUiRefUri('ui://Replay'), null);
  assert.equal(parseUiRefUri('ui://../etc'), null);
  assert.equal(parseUiRefUri('https://example.com'), null);
  assert.equal(parseUiRefUri(''), null);
  // Junk params are dropped, not fatal: a ref is a best-effort pointer.
  assert.deepEqual(parseUiRefUri('ui://read?&=x&novalue&ok=1'), { surface: 'read', params: { ok: '1' } });
  // Values are bounded — a ref is a reference, not a payload.
  const huge = `ui://read?x=${'y'.repeat(500)}`;
  assert.deepEqual(parseUiRefUri(huge), { surface: 'read', params: {} });
});

test('the JSON form (what ui_highlight takes) flattens to the same thing', () => {
  // The ADR's nested shape, as ui_get_focus emits it.
  assert.deepEqual(uiRefFromJson({ surface: 'replay', entity: { dataset_id: 'ds_1', cursor: 1234 } }), {
    surface: 'replay',
    params: { dataset_id: 'ds_1', cursor: '1234' },
  });
  assert.deepEqual(uiRefFromJson({ surface: 'debug', path: { file: 'src/foo.ts', selection: [42, 58] } }), {
    surface: 'debug',
    params: { file: 'src/foo.ts', selection: '42,58' },
  });
  // A string argument is the URI spelling — one grammar, two spellings.
  assert.deepEqual(uiRefFromJson('ui://read?tab_id=wt_3'), { surface: 'read', params: { tab_id: 'wt_3' } });
  // Unknown blocks are ignored rather than rejected: refusing the whole ref
  // over one stray key would make the round trip brittle.
  assert.deepEqual(uiRefFromJson({ surface: 'read', mystery: { a: 'b' } }), { surface: 'read', params: { a: 'b' } });
  for (const bad of [null, 42, [], { entity: {} }, { surface: 'Nope!' }]) {
    assert.equal(uiRefFromJson(bad), null, JSON.stringify(bad));
  }
});

test('the chip label reads as the plan writes it', () => {
  assert.equal(uiRefLabel({ surface: 'replay', params: { episode_id: 'ep_9', cursor: '1234' } }), 'replay · ep_9 @ 1234');
  assert.equal(uiRefLabel({ surface: 'debug', params: { file: 'src/foo.ts', selection: '42,58' } }), 'src/foo.ts:42');
  assert.equal(uiRefLabel({ surface: 'debug', params: { file: 'src/foo.ts' } }), 'src/foo.ts');
  // Never an empty chip.
  assert.equal(uiRefLabel({ surface: 'terminal', params: {} }), 'terminal');
});

test('linkifyUiRefs turns agent prose into chips — and leaves code alone', () => {
  const out = linkifyUiRefs('see ui://replay?episode_id=ep_9 for the drop');
  assert.equal(out, 'see [replay · ep_9](ui://replay?episode_id=ep_9) for the drop');

  // THE thing it must never do: an agent explaining a URI inside a fence is
  // SHOWING it, not pointing with it.
  const fenced = 'try:\n```\nui://replay?dataset_id=ds_1\n```\ndone';
  assert.equal(linkifyUiRefs(fenced), fenced);
  const inline = 'the form is `ui://replay?dataset_id=ds_1`, ok?';
  assert.equal(linkifyUiRefs(inline), inline);
  // A fence that never closes still swallows its tail rather than linkifying
  // half a code block.
  const unclosed = 'oops\n```\nui://read\n';
  assert.equal(linkifyUiRefs(unclosed), unclosed);

  // Sentence punctuation is not part of the ref.
  assert.match(linkifyUiRefs('go to ui://terminal.'), /\(ui:\/\/terminal\)\.$/);
  // Several in one message all become chips.
  const two = linkifyUiRefs('ui://read and ui://debug?file=a.ts');
  assert.equal((two.match(/\]\(ui:\/\//g) ?? []).length, 2);
  // An unparseable token stays prose — better a literal string than a chip
  // that points nowhere.
  assert.equal(linkifyUiRefs('ui://'), 'ui://');
  // Text with no refs is returned untouched.
  assert.equal(linkifyUiRefs('nothing here'), 'nothing here');
});

test('malformed percent escapes are junk params, never a throw', () => {
  // Regression: decodeURIComponent throws on a lone `%` / `%zz`, and this
  // grammar reads AGENT PROSE — "100% done" is a sentence. The throw
  // happened during Markdown render, so one such message crashed the
  // transcript surface on every re-mount (persistent, agent-controlled).
  assert.doesNotThrow(() => linkifyUiRefs('see ui://read?file=100% done'));
  assert.doesNotThrow(() => linkifyUiRefs('x ui://read?a=%zz y'));
  assert.doesNotThrow(() => parseUiRefUri('ui://read?a=%'));
  // The pair that failed to decode is dropped; the ref itself survives —
  // "a token we cannot parse stays prose" applies to the pair, not the ref.
  assert.deepEqual(parseUiRefUri('ui://read?a=%zz&tab_id=wt_3'), { surface: 'read', params: { tab_id: 'wt_3' } });
  // And the linkified output still renders as a chip for the valid part.
  assert.match(linkifyUiRefs('ui://read?a=%zz&tab_id=wt_3'), /^\[.*\]\(ui:\/\/read\?a=%zz&tab_id=wt_3\)$/);
});

test('a ref carries references only — never content', () => {
  // Stated as an invariant: the type has no content field and the parser
  // mints none. A future field addition has to argue with this line
  // (ADR-062 D-2).
  const ref = parseUiRefUri('ui://read?tab_id=wt_3');
  assert.deepEqual(Object.keys(ref ?? {}).sort(), ['params', 'surface']);
});
