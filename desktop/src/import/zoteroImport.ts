import initSqlJs, { type Database, type SqlValue } from 'sql.js';
import wasmUrl from 'sql.js/dist/sql-wasm.wasm?url';
import { isInternalTag, type ImportItem, type RefType } from '../state/library';

/// Parse a Zotero `zotero.sqlite` file into library `ImportItem`s — entirely in
/// the WebView via sql.js (WASM SQLite), so importing needs no Rust and no
/// server round-trip. The bytes never leave the device. The wasm is a lazily
/// code-split asset (this whole module is dynamic-imported by the Read surface),
/// so it only downloads when the user actually imports.
///
/// Mapping notes: attachments/annotations/standalone notes and trashed items are
/// dropped (we import bibliographic references only); a Zotero item's collection
/// membership becomes `collectionNames` (the store finds-or-creates by name); the
/// Zotero item key becomes `externalId` (`zotero:<key>`) so re-import dedupes.

// Zotero itemType → our coarse RefType. Unmapped types fall back to 'article'.
const TYPE_MAP: Record<string, RefType> = {
  journalArticle: 'article',
  conferencePaper: 'article',
  magazineArticle: 'article',
  newspaperArticle: 'article',
  encyclopediaArticle: 'article',
  dictionaryEntry: 'article',
  interview: 'article',
  preprint: 'preprint',
  book: 'book',
  bookSection: 'book',
  manuscript: 'book',
  thesis: 'report',
  report: 'report',
  standard: 'report',
  patent: 'report',
  presentation: 'report',
  computerProgram: 'report',
  dataset: 'report',
  webpage: 'webpage',
  blogPost: 'webpage',
  forumPost: 'webpage',
  document: 'webpage',
  videoRecording: 'webpage',
  podcast: 'webpage',
};

type Row = Record<string, SqlValue>;

// db.exec returns one result set per statement; we run single statements.
function query(db: Database, sql: string): Row[] {
  let res;
  try {
    res = db.exec(sql);
  } catch {
    return []; // optional table absent in an old/partial DB — degrade, don't abort
  }
  if (res.length === 0) return [];
  const { columns, values } = res[0];
  return values.map((v) => {
    const o: Row = {};
    columns.forEach((c, i) => (o[c] = v[i]));
    return o;
  });
}

function s(v: SqlValue | undefined): string | undefined {
  return typeof v === 'string' && v !== '' ? v : undefined;
}

function parseYear(date: string | undefined): number | undefined {
  if (date === undefined) return undefined;
  const m = /(\d{4})/.exec(date);
  if (m === null) return undefined;
  const y = parseInt(m[1], 10);
  return y >= 1400 && y <= 2200 ? y : undefined;
}

// arXiv id from archiveID ("arXiv:2309.17421"), a url, or the extra field.
function parseArxiv(f: Record<string, string>): string | undefined {
  for (const c of [f.archiveID, f.url, f.extra]) {
    if (c === undefined || !/arxiv/i.test(c)) continue;
    const m = /(\d{4}\.\d{4,5})/.exec(c);
    if (m !== null) return m[1];
  }
  return undefined;
}

function authorName(first: string | undefined, last: string | undefined): string {
  return [first, last].filter((x) => x !== undefined && x.trim() !== '').join(' ').trim();
}

// Zotero fields already promoted to a first-class Reference field — kept out of
// the `details` bag so it holds only the remaining source metadata.
const PROMOTED = new Set([
  'title',
  'abstractNote',
  'date',
  'DOI',
  'url',
  'publicationTitle',
  'blogTitle',
  'websiteTitle',
]);

function detailsOf(f: Record<string, string>): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(f)) {
    if (PROMOTED.has(k) || v.trim() === '') continue;
    out[k] = v;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

function extract(db: Database): ImportItem[] {
  // Base bibliographic items (drop trash + attachment/annotation/note).
  const items = query(
    db,
    `SELECT i.itemID AS itemID, i.key AS key, it.typeName AS typeName, i.dateAdded AS dateAdded
     FROM items i JOIN itemTypes it ON it.itemTypeID = i.itemTypeID
     WHERE i.itemID NOT IN (SELECT itemID FROM deletedItems)
       AND it.typeName NOT IN ('attachment','annotation','note')`,
  );

  // itemID → { fieldName: value }
  const fields = new Map<number, Record<string, string>>();
  for (const r of query(
    db,
    `SELECT d.itemID AS itemID, f.fieldName AS fieldName, dv.value AS value
     FROM itemData d JOIN fields f ON f.fieldID = d.fieldID
     JOIN itemDataValues dv ON dv.valueID = d.valueID`,
  )) {
    const id = r.itemID as number;
    const fn = r.fieldName as string;
    const map = fields.get(id) ?? {};
    if (typeof r.value === 'string') map[fn] = r.value;
    fields.set(id, map);
  }

  // itemID → [{ name, type }] ordered
  const creators = new Map<number, { name: string; type: string }[]>();
  for (const r of query(
    db,
    `SELECT ic.itemID AS itemID, c.firstName AS firstName, c.lastName AS lastName,
            ct.creatorType AS creatorType, ic.orderIndex AS orderIndex
     FROM itemCreators ic JOIN creators c ON c.creatorID = ic.creatorID
     JOIN creatorTypes ct ON ct.creatorTypeID = ic.creatorTypeID
     ORDER BY ic.itemID, ic.orderIndex`,
  )) {
    const id = r.itemID as number;
    const name = authorName(s(r.firstName), s(r.lastName));
    if (name === '') continue;
    const list = creators.get(id) ?? [];
    list.push({ name, type: (r.creatorType as string) ?? 'author' });
    creators.set(id, list);
  }

  // collectionID → name; itemID → [collectionName]. Every real Zotero collection
  // is imported (including plugin buckets like "Recently Read") — the director
  // asked to see them; only the virtual libraries (Trash / Duplicates / Unfiled),
  // which aren't `collections` rows, never appear.
  const colName = new Map<number, string>();
  for (const r of query(db, `SELECT collectionID, collectionName FROM collections`)) {
    if (typeof r.collectionName === 'string') colName.set(r.collectionID as number, r.collectionName);
  }
  const itemCols = new Map<number, string[]>();
  for (const r of query(db, `SELECT collectionID, itemID FROM collectionItems`)) {
    const name = colName.get(r.collectionID as number);
    if (name === undefined) continue;
    const list = itemCols.get(r.itemID as number) ?? [];
    list.push(name);
    itemCols.set(r.itemID as number, list);
  }

  // parentItemID → primary stored attachment { key (=storage subdir), file }.
  // Zotero keeps imported files under storage/<attachment-key>/<filename>; the
  // attachment's `path` is `storage:<filename>`. Prefer a PDF when a parent has
  // several attachments.
  const attach = new Map<number, { key: string; file: string; contentType?: string }>();
  for (const r of query(
    db,
    `SELECT ia.parentItemID AS parentItemID, ai.key AS attachKey, ia.path AS path, ia.contentType AS contentType
     FROM itemAttachments ia JOIN items ai ON ai.itemID = ia.itemID
     WHERE ia.path LIKE 'storage:%' AND ia.parentItemID IS NOT NULL
       AND ia.itemID NOT IN (SELECT itemID FROM deletedItems)`,
  )) {
    const parent = r.parentItemID as number;
    const key = r.attachKey as string;
    const path = r.path as string;
    if (typeof key !== 'string' || typeof path !== 'string') continue;
    const file = path.slice('storage:'.length);
    const contentType = typeof r.contentType === 'string' ? r.contentType : undefined;
    const existing = attach.get(parent);
    const isPdf = contentType === 'application/pdf';
    // Keep the first attachment, but let a PDF override a non-PDF first pick.
    if (existing === undefined || (isPdf && existing.contentType !== 'application/pdf')) {
      attach.set(parent, { key, file, contentType });
    }
  }

  // itemID → [tag]. Only USER tags: Zotero marks each item↔tag link with a `type`
  // — 0 = added by the user, 1 = automatic (harvested by an import translator, the
  // "many English keyword" tags the director never chose and Zotero de-emphasizes).
  // Drop the automatics, plus reading-list plugin markers like "/unread" / "/read"
  // (a leading slash), which are functional flags, not content tags.
  const itemTags = new Map<number, string[]>();
  for (const r of query(
    db,
    `SELECT it.itemID AS itemID, it.type AS tagType, t.name AS name FROM itemTags it JOIN tags t ON t.tagID = it.tagID`,
  )) {
    if (typeof r.name !== 'string') continue;
    if (r.tagType === 1 || isInternalTag(r.name)) continue;
    const list = itemTags.get(r.itemID as number) ?? [];
    list.push(r.name);
    itemTags.set(r.itemID as number, list);
  }

  const out: ImportItem[] = [];
  for (const it of items) {
    const id = it.itemID as number;
    const key = (it.key as string) ?? String(id);
    const f = fields.get(id) ?? {};

    const cr = creators.get(id) ?? [];
    const authorsOnly = cr.filter((c) => c.type === 'author');
    const authors = (authorsOnly.length > 0 ? authorsOnly : cr).map((c) => c.name);

    const arxivId = parseArxiv(f);
    let pdfUrl: string | undefined;
    if (arxivId !== undefined) pdfUrl = `https://arxiv.org/pdf/${arxivId}`;
    else if (f.url !== undefined && /\.pdf($|\?)/i.test(f.url)) pdfUrl = f.url;

    const venue =
      f.publicationTitle ?? f.blogTitle ?? f.websiteTitle ?? f.repository ?? f.publisher ?? f.company;

    out.push({
      ref: {
        type: TYPE_MAP[it.typeName as string] ?? 'article',
        title: f.title ?? '',
        authors,
        year: parseYear(f.date),
        venue,
        doi: f.DOI,
        arxivId,
        url: f.url,
        pdfUrl,
        abstract: f.abstractNote,
        source: 'zotero',
        externalId: `zotero:${key}`,
        tags: itemTags.get(id) ?? [],
        notes: '',
        zoteroStorage: attach.get(id),
        details: detailsOf(f),
      },
      collectionNames: itemCols.get(id) ?? [],
    });
  }
  return out;
}

export async function parseZoteroSqlite(bytes: Uint8Array): Promise<ImportItem[]> {
  const SQL = await initSqlJs({ locateFile: () => wasmUrl });
  const db = new SQL.Database(bytes);
  try {
    return extract(db);
  } finally {
    db.close();
  }
}
