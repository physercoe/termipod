export type StructuredSectionStatus = 'empty' | 'draft' | 'ratified';

export interface StructuredDocumentSection {
  slug: string;
  title: string;
  body: string;
  status: StructuredSectionStatus;
}

export interface StructuredDocumentBody {
  schemaVersion: number;
  schemaId: string;
  sections: StructuredDocumentSection[];
}

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

/// Decode the JSON envelope stored in `documents.content_inline` for a typed
/// document. Section rows are narrowed defensively: an unknown state degrades
/// to draft (or empty when there is no body) instead of making the entire
/// document unreadable.
export function parseStructuredDocument(source: string): StructuredDocumentBody | null {
  let decoded: unknown;
  try {
    decoded = JSON.parse(source);
  } catch {
    return null;
  }
  const root = record(decoded);
  if (root === null || !Array.isArray(root['sections'])) return null;

  const sections = root['sections'].flatMap((value, index): StructuredDocumentSection[] => {
    const row = record(value);
    if (row === null) return [];
    const body = text(row['body']);
    const rawStatus = text(row['status']);
    const status: StructuredSectionStatus =
      rawStatus === 'empty' || rawStatus === 'draft' || rawStatus === 'ratified'
        ? rawStatus
        : body.trim() === ''
          ? 'empty'
          : 'draft';
    return [
      {
        slug: text(row['slug']) || `section-${index + 1}`,
        title: text(row['title']),
        body,
        status,
      },
    ];
  });

  const rawVersion = root['schema_version'];
  return {
    schemaVersion: typeof rawVersion === 'number' && Number.isInteger(rawVersion) ? rawVersion : 1,
    schemaId: text(root['schema_id']),
    sections,
  };
}

/// Typed bodies commonly repeat the section title as their first Markdown
/// heading. The viewer already supplies a semantic heading, so remove only an
/// exact normalized duplicate and leave every other authored heading intact.
export function withoutDuplicateTitle(body: string, title: string): string {
  const match = body.match(/^\s{0,3}#{1,6}\s+(.+?)\s*#*\s*(?:\r?\n|$)/);
  if (match === null || title.trim() === '') return body;
  const normalize = (value: string): string => value.trim().replace(/\s+/g, ' ').toLocaleLowerCase();
  return normalize(match[1]) === normalize(title) ? body.slice(match[0].length).replace(/^\s+/, '') : body;
}
