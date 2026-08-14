import assert from 'node:assert/strict';
import test from 'node:test';
import { parseStructuredDocument, withoutDuplicateTitle } from './structuredDocumentModel.ts';

test('typed document JSON decodes section Markdown instead of exposing the envelope', () => {
  const body = parseStructuredDocument(
    JSON.stringify({
      schema_version: 1,
      schema_id: 'research-lit-review-v1',
      sections: [
        {
          slug: 'domain-overview',
          title: 'Domain overview',
          body: '## Domain overview\n\nThe closest peers are **Lion** and Cramming.',
          status: 'ratified',
        },
      ],
    }),
  );

  assert.equal(body?.schemaId, 'research-lit-review-v1');
  assert.equal(body?.sections[0]?.status, 'ratified');
  assert.match(body?.sections[0]?.body ?? '', /\*\*Lion\*\*/);
});

test('plain Markdown and malformed JSON do not masquerade as typed documents', () => {
  assert.equal(parseStructuredDocument('# Review\n\nReadable prose.'), null);
  assert.equal(parseStructuredDocument('{"sections":'), null);
});

test('unknown section state degrades from its content without losing the section', () => {
  const body = parseStructuredDocument(
    '{"sections":[{"body":"Draft prose","status":"future"},{"body":"","status":"future"}]}',
  );
  assert.deepEqual(
    body?.sections.map((section) => section.status),
    ['draft', 'empty'],
  );
});

test('a duplicated leading Markdown title is removed but a different heading is preserved', () => {
  assert.equal(withoutDuplicateTitle('## Domain overview\n\nBody', 'Domain overview'), 'Body');
  assert.equal(withoutDuplicateTitle('## Findings\n\nBody', 'Domain overview'), '## Findings\n\nBody');
});
