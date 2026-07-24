/// Tests for the forge URL parser (round-3 T3): GitHub / Hugging Face URLs, the
/// `tree`/`blob` ref+subpath forms, and the ambiguous `owner/repo[@ref]`
/// shorthand the add-root dialog's forge selector disambiguates. Run locally:
/// `node --test src/state/forge.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseForgeUrl } from './forgeUrl.ts';

test('parseForgeUrl: GitHub repo URLs, with and without a ref', () => {
  assert.deepEqual(parseForgeUrl('https://github.com/torvalds/linux'), { forge: 'github', id: 'torvalds/linux', ref: undefined, subpath: undefined });
  assert.deepEqual(parseForgeUrl('github.com/openai/whisper.git'), { forge: 'github', id: 'openai/whisper', ref: undefined, subpath: undefined });
  assert.deepEqual(parseForgeUrl('https://github.com/a/b/tree/dev'), { forge: 'github', id: 'a/b', ref: 'dev', subpath: undefined });
  assert.deepEqual(parseForgeUrl('https://github.com/a/b/tree/v1.2/src/core'), { forge: 'github', id: 'a/b', ref: 'v1.2', subpath: 'src/core' });
  assert.deepEqual(parseForgeUrl('https://github.com/a/b/blob/main/README.md'), { forge: 'github', id: 'a/b', ref: 'main', subpath: 'README.md' });
});

test('parseForgeUrl: strips query/hash and trailing slashes', () => {
  assert.deepEqual(parseForgeUrl('https://github.com/a/b/?tab=readme#x'), { forge: 'github', id: 'a/b', ref: undefined, subpath: undefined });
  assert.deepEqual(parseForgeUrl('https://github.com/a/b/'), { forge: 'github', id: 'a/b', ref: undefined, subpath: undefined });
});

test('parseForgeUrl: Hugging Face URLs (org/name and single-segment) with rev', () => {
  assert.deepEqual(parseForgeUrl('https://huggingface.co/meta-llama/Llama-3-8B'), { forge: 'hf', id: 'meta-llama/Llama-3-8B', ref: undefined, subpath: undefined });
  assert.deepEqual(parseForgeUrl('https://hf.co/gpt2'), { forge: 'hf', id: 'gpt2', ref: undefined, subpath: undefined });
  assert.deepEqual(parseForgeUrl('https://huggingface.co/org/m/tree/refs%2Fpr%2F1'), { forge: 'hf', id: 'org/m', ref: 'refs%2Fpr%2F1', subpath: undefined });
});

test('parseForgeUrl: shorthand owner/repo[@ref] uses the forge hint', () => {
  assert.deepEqual(parseForgeUrl('torvalds/linux'), { forge: 'github', id: 'torvalds/linux', ref: undefined });
  assert.deepEqual(parseForgeUrl('a/b@dev'), { forge: 'github', id: 'a/b', ref: 'dev' });
  assert.deepEqual(parseForgeUrl('org/model@main', 'hf'), { forge: 'hf', id: 'org/model', ref: 'main' });
});

test('parseForgeUrl: rejects junk / incomplete refs', () => {
  assert.equal(parseForgeUrl(''), null);
  assert.equal(parseForgeUrl('   '), null);
  assert.equal(parseForgeUrl('https://github.com/onlyowner'), null); // GitHub needs owner/repo
  assert.equal(parseForgeUrl('not a url'), null);
  assert.equal(parseForgeUrl('https://gitlab.com/a/b'), null); // unsupported forge host
});
