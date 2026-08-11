import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Attachment } from './library.ts';
import { attachmentsForPush, attachmentsFromHub, mergeSyncedAttachments, needsAttachmentBackfill, portableAttachmentBodies } from './attachmentManifest.ts';

const managed: Attachment = {
  id: 'local-id',
  file: 'paper.pdf',
  contentType: 'application/pdf',
  source: 'managed',
  key: 'paper-key',
  path: '/host-only/storage/paper-key/paper.pdf',
  srcUrl: 'https://example.test/paper.pdf',
  addedAt: 42,
};

test('portable attachment metadata never includes an absolute host path', () => {
  const bodies = portableAttachmentBodies({ attachments: [managed, { ...managed, id: 'missing-key', key: undefined }] });
  assert.deepEqual(bodies, [
    {
      id: 'local-id',
      file: 'paper.pdf',
      content_type: 'application/pdf',
      source: 'managed',
      key: 'paper-key',
      src_url: 'https://example.test/paper.pdf',
      added_at: 42,
    },
  ]);
  assert.doesNotMatch(JSON.stringify(bodies), /host-only|"path"/);
});

test('hub manifests decode all attachments and deduplicate the legacy Zotero coordinate', () => {
  const attachments = attachmentsFromHub({
    attachments: [
      { id: 'managed-remote', file: 'paper.pdf', source: 'managed', key: 'paper-key', added_at: 42 },
      { id: 'zotero-remote', file: 'z.pdf', source: 'zotero', key: 'ZKEY' },
      { file: '/invalid/absolute.pdf', source: 'managed', key: '' },
    ],
    zotero_storage: { key: 'ZKEY', file: 'z.pdf', content_type: 'application/pdf' },
  });
  assert.equal(attachments.length, 2);
  assert.deepEqual(attachments.map((attachment) => attachment.source), ['managed', 'zotero']);
});

test('metadata backfill is source-safe and receiver-safe', () => {
  assert.equal(needsAttachmentBackfill({ attachments: [managed] }, { attachments: [] }), true);
  assert.equal(needsAttachmentBackfill({ attachments: [] }, { attachments: [{ file: 'paper.pdf', source: 'managed', key: 'paper-key' }] }), false);
  assert.equal(needsAttachmentBackfill({ attachments: [managed] }, { attachments: [{ file: 'paper.pdf', source: 'managed', key: 'paper-key' }] }), false);
});
  const remoteHub = { attachments: [{ file: 'paper.pdf', source: 'managed', key: 'paper-key' }] };
  assert.equal(attachmentsForPush({ attachments: [] }, remoteHub)[0]?.key, 'paper-key');
  assert.deepEqual(
    attachmentsForPush({ attachments: [], attachmentManifestSynced: true }, remoteHub),
    [],
    'an initialized empty manifest represents an intentional remove-all',
  );


test('hub merge preserves local paths but honors remote additions and deletions', () => {
  const remoteMatch: Attachment = { ...managed, id: 'remote-id', path: undefined };
  const remoteNew: Attachment = {
    id: 'remote-new',
    file: 'second.pdf',
    source: 'managed',
    key: 'second-key',
    addedAt: 50,
  };
  const clean = mergeSyncedAttachments([managed], [remoteMatch, remoteNew], false);
  assert.equal(clean[0]?.id, 'local-id');
  assert.equal(clean[0]?.path, managed.path);
  assert.equal(clean[1]?.id, 'remote-new');
  assert.deepEqual(mergeSyncedAttachments([managed], [], false), []);
  assert.deepEqual(mergeSyncedAttachments([managed], [], true), [managed]);
});
