/// R4 tool-row media + live-output extraction checks. The image fixtures are
/// the shapes our producers actually emit, not invented ones:
///   - the Anthropic block is a verbatim capture from `claude --print
///     --output-format stream-json` reading a PNG (tool_result.content is a
///     LIST, and is_error is absent rather than false);
///   - the MCP block matches driver_acp.go:1752 and the desktop bridge results
///     E4 forwards unwrapped (hub/internal/server/mcp_browser_bridge_test.go).
/// The frontend package has no CI test runner; run locally with
/// `node --test src/ui/toolMedia.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BLOB_REF_PREFIX, imageRefsOf, mediaRefFrom, resultTextOf, streamedOutputOf, tailLines } from './toolMedia.ts';

// Captured from claude-code's own stream-json output (2x2 red PNG).
const REAL_PNG =
  'iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAEElEQVR4nGP4z8AARAwQCgAf7gP9i18U1AAAAABJRU5ErkJggg==';

const anthropicBlock = { type: 'image', source: { type: 'base64', data: REAL_PNG, media_type: 'image/png' } };
const mcpBlock = { type: 'image', data: REAL_PNG, mimeType: 'image/png' };

test('the measured claude tool_result image block paints inline', () => {
  assert.deepEqual(imageRefsOf([anthropicBlock]), [{ source: 'inline', mime: 'image/png', data: REAL_PNG }]);
});

test('the MCP/ACP dialect paints inline too', () => {
  assert.deepEqual(imageRefsOf([mcpBlock]), [{ source: 'inline', mime: 'image/png', data: REAL_PNG }]);
});

test('an ACP content wrapper is unwrapped one level', () => {
  assert.deepEqual(imageRefsOf([{ type: 'content', content: mcpBlock }]), [
    { source: 'inline', mime: 'image/png', data: REAL_PNG },
  ]);
});

test('an externalized image becomes a blob ref, in either dialect', () => {
  const sha = 'a'.repeat(64);
  const ref = `${BLOB_REF_PREFIX}${sha}`;
  assert.deepEqual(imageRefsOf([{ type: 'image', source: { type: 'base64', media_type: 'image/png', data: ref } }]), [
    { source: 'blob', mime: 'image/png', sha },
  ]);
  assert.deepEqual(imageRefsOf([{ type: 'image', mimeType: 'image/jpeg', data: ref }]), [
    { source: 'blob', mime: 'image/jpeg', sha },
  ]);
});

test('the mime rides the BLOCK, not the blob', () => {
  // The hub stores an externalized leaf as application/octet-stream, so the
  // block's own media_type is the only surviving statement of what the bytes
  // are. If this ever regressed to the blob's mime the <img> would paint
  // nothing.
  const refs = imageRefsOf([
    { type: 'image', source: { type: 'base64', media_type: 'image/svg+xml', data: `${BLOB_REF_PREFIX}${'b'.repeat(64)}` } },
  ]);
  assert.equal(refs[0]?.mime, 'image/svg+xml');
});

test('shapes that cannot be painted are skipped, not guessed at', () => {
  // A plain-string content (the common tool_result) has no blocks at all.
  assert.deepEqual(imageRefsOf('file contents'), []);
  assert.deepEqual(imageRefsOf(undefined), []);
  assert.deepEqual(imageRefsOf([]), []);
  // Text blocks are not images.
  assert.deepEqual(imageRefsOf([{ type: 'text', text: 'hi' }]), []);
  // A url source would make the renderer fetch an agent-chosen host.
  assert.deepEqual(imageRefsOf([{ type: 'image', source: { type: 'url', url: 'https://evil.example/x.png' } }]), []);
  // ...and it must be the source-TYPE check that rejects it, not the happy
  // accident that a url block carries no `data`. A block claiming both is the
  // only input that tells those two guards apart (a mutation that dropped the
  // type check survived the case above).
  assert.deepEqual(
    imageRefsOf([{ type: 'image', source: { type: 'url', url: 'https://evil.example/x.png', data: REAL_PNG, media_type: 'image/png' } }]),
    [],
  );
  // A non-image mime must not reach an <img>.
  assert.deepEqual(imageRefsOf([{ type: 'image', mimeType: 'text/html', data: 'PHNjcmlwdD4=' }]), []);
  // Empty data is not a picture.
  assert.deepEqual(imageRefsOf([{ type: 'image', mimeType: 'image/png', data: '' }]), []);
  // A bare ref prefix with no sha resolves to nothing fetchable.
  assert.deepEqual(imageRefsOf([{ type: 'image', mimeType: 'image/png', data: BLOB_REF_PREFIX }]), []);
  // Junk in the array does not throw or poison the good blocks.
  assert.deepEqual(imageRefsOf([null, 'x', 42, mcpBlock]), [{ source: 'inline', mime: 'image/png', data: REAL_PNG }]);
});

test('a missing mime defaults to png rather than dropping the image', () => {
  assert.deepEqual(mediaRefFrom(undefined, REAL_PNG), { source: 'inline', mime: 'image/png', data: REAL_PNG });
  assert.equal(mediaRefFrom('image/png', undefined), undefined);
  assert.equal(mediaRefFrom('application/pdf', REAL_PNG), undefined);
});

test('multiple image blocks all render, in order', () => {
  const refs = imageRefsOf([anthropicBlock, { type: 'text', text: 'between' }, mcpBlock]);
  assert.equal(refs.length, 2);
});

// --- live output -----------------------------------------------------------

test("E3's payload shape yields the whole cumulative buffer", () => {
  // Byte-identical to what driver_appserver.go flushOutputStream posts.
  const update = {
    toolCallId: 'item_1',
    content: [{ type: 'content', content: { type: 'text', text: 'row 1\nrow 2\n' } }],
    partial: true,
  };
  assert.equal(streamedOutputOf(update), 'row 1\nrow 2\n');
});

test('text blocks join with no separator (a byte stream, not paragraphs)', () => {
  const update = {
    content: [
      { type: 'content', content: { type: 'text', text: 'abc' } },
      { type: 'content', content: { type: 'text', text: 'def\n' } },
    ],
  };
  assert.equal(streamedOutputOf(update), 'abcdef\n');
});

test('an update with no text content yields empty string', () => {
  assert.equal(streamedOutputOf(undefined), '');
  assert.equal(streamedOutputOf({}), '');
  assert.equal(streamedOutputOf({ content: 'not an array' }), '');
  assert.equal(streamedOutputOf({ content: [] }), '');
  assert.equal(streamedOutputOf({ content: [{ type: 'content', content: mcpBlock }] }), '');
  // A terminal ACP update carrying only a status must not print as output.
  assert.equal(streamedOutputOf({ toolCallId: 'x', status: 'completed' }), '');
});

test('a bare text block (no ACP wrapper) is still read', () => {
  assert.equal(streamedOutputOf({ content: [{ type: 'text', text: 'bare' }] }), 'bare');
});

// --- result text -----------------------------------------------------------

test('resultTextOf passes a plain string content straight through', () => {
  assert.equal(resultTextOf('file contents'), 'file contents');
  assert.equal(resultTextOf(undefined), '');
  assert.equal(resultTextOf(42), '');
});

test('resultTextOf joins discrete text blocks with newlines', () => {
  assert.equal(resultTextOf([{ type: 'text', text: 'first' }, { type: 'text', text: 'second' }]), 'first\nsecond');
});

test('resultTextOf never returns image bytes', () => {
  // This is the point of the function: an image-bearing result must not put a
  // megabyte of base64 into the DOM, even inside a collapsed <details>.
  const out = resultTextOf([anthropicBlock, { type: 'text', text: 'the screenshot' }]);
  assert.equal(out, 'the screenshot');
  assert.ok(!out.includes(REAL_PNG));
  assert.equal(resultTextOf([anthropicBlock]), '');
});

// --- tail cap --------------------------------------------------------------

test('tailLines keeps the newest lines and admits the clip', () => {
  const text = Array.from({ length: 10 }, (_, i) => `line ${String(i)}`).join('\n');
  const out = tailLines(text, 3);
  assert.equal(out.text, 'line 7\nline 8\nline 9');
  assert.equal(out.clipped, true);
});

test('tailLines leaves short output exactly alone', () => {
  assert.deepEqual(tailLines('a\nb', 3), { text: 'a\nb', clipped: false });
  assert.deepEqual(tailLines('a\nb\nc', 3), { text: 'a\nb\nc', clipped: false });
  assert.deepEqual(tailLines('', 3), { text: '', clipped: false });
  assert.deepEqual(tailLines('a\nb', 0), { text: 'a\nb', clipped: false });
});
