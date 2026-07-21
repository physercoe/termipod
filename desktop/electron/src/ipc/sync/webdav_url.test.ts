/// Tests for the pure WebDAV URL/XML helpers (ADR-055 M2.5b). Percent-encoding
/// and trailing-slash rules are interop-critical (the PUT/PROPFIND URL must
/// address the same object the server's href resolves to), so they are pinned
/// here. Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { baseUrl, childUrl, hasCollectionTag, authHeader } from './webdav_url.ts';

test('baseUrl: forces a trailing slash; rejects garbage', () => {
  assert.equal(baseUrl('https://h/dav').href, 'https://h/dav/');
  assert.equal(baseUrl('https://h/dav/').href, 'https://h/dav/');
  assert.throws(() => baseUrl('not a url'));
});

test('childUrl: joins + percent-encodes segments; dir adds trailing slash', () => {
  const base = baseUrl('https://h/dav/');
  assert.equal(childUrl(base, 'a/b.md', false), 'https://h/dav/a/b.md');
  assert.equal(childUrl(base, 'a/b', true), 'https://h/dav/a/b/');
  // spaces + unicode encode per-segment (not the '/')
  assert.equal(childUrl(base, 'my notes/café.md', false), 'https://h/dav/my%20notes/caf%C3%A9.md');
  // empty rel is the base itself, trailing slash preserved
  assert.equal(childUrl(base, '', true), 'https://h/dav/');
});

test('childUrl: preserves a non-root base path', () => {
  const base = baseUrl('https://h/remote.php/dav/files/me');
  assert.equal(childUrl(base, 'note.md', false), 'https://h/remote.php/dav/files/me/note.md');
});

test('hasCollectionTag: matches namespaced + self-closing, not files', () => {
  assert.equal(hasCollectionTag('<D:resourcetype><D:collection/></D:resourcetype>'), true);
  assert.equal(hasCollectionTag('<resourcetype><collection></collection></resourcetype>'), true);
  assert.equal(hasCollectionTag('<D:resourcetype/>'), false); // a plain file
  assert.equal(hasCollectionTag('<D:collectionitem/>'), false); // prefix-delimited, not "collection"
});

test('authHeader: Basic base64(user:pass)', () => {
  assert.equal(authHeader('aladdin', 'opensesame'), 'Basic YWxhZGRpbjpvcGVuc2VzYW1l');
});
