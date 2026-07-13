import type { HubClient } from '../hub/client';
import { arr, num, obj, str, type Entity } from '../hub/types';
import { useLibrary, type ImportItem, type Reference } from './library';

/// Sync the device-local reference library with the hub's Reference entity
/// (ADR-053, table `reference_items`) so agents and the desktop see one library.
///
/// v1 semantics — **local-wins union**:
///   1. PUSH every local reference to the hub (link by `hubId`, else match a hub
///      row by DOI / arXiv / external id, else create). The hub ends up mirroring
///      local for shared items.
///   2. PULL the hub list back and import it (merges by strong key, preserving the
///      reader's own notes/body/tags; adds any hub-only rows — e.g. an agent
///      created one). Because we pushed first, the pull is a no-op for shared rows.
///
/// So on a field conflict the desktop value wins (it's the primary author). A
/// timestamped three-way merge is the follow-up. Enrichment fields (citations,
/// journal metrics, code/data links) are NOT synced yet — the hub schema has no
/// columns for them; they stay device-local until the migration lands.

function strArray(e: Entity, key: string): string[] {
  return arr(e, key).filter((x): x is string => typeof x === 'string');
}

// The scraper enrichment as one opaque blob (hub column enrichment_json, added in
// migration 0063 / PR #306). Sent only when present; forward-compatible — a hub
// without the column just ignores the unknown `enrichment` field.
function buildEnrichment(r: Reference): Record<string, unknown> | undefined {
  const e: Record<string, unknown> = {};
  if (r.referenceCount !== undefined) e.referenceCount = r.referenceCount;
  if (r.citedByCount !== undefined) e.citedByCount = r.citedByCount;
  if (r.influentialCitationCount !== undefined) e.influentialCitationCount = r.influentialCitationCount;
  if (r.references !== undefined) e.references = r.references;
  if (r.citations !== undefined) e.citations = r.citations;
  if (r.journal !== undefined) e.journal = r.journal;
  if (r.openAccess !== undefined) e.openAccess = r.openAccess;
  if (r.topics !== undefined) e.topics = r.topics;
  if (r.resourceLinks !== undefined) e.resourceLinks = r.resourceLinks;
  if (r.enrichedAt !== undefined) e.enrichedAt = r.enrichedAt;
  if (r.enrichSource !== undefined) e.enrichSource = r.enrichSource;
  return Object.keys(e).length > 0 ? e : undefined;
}

// Map a hub `enrichment` blob back onto a reference. Only keys actually present
// are assigned (so an older/partial hub blob never wipes richer local data).
function applyEnrichment(ref: Partial<Reference>, enr: Entity): void {
  if ('referenceCount' in enr) ref.referenceCount = num(enr, 'referenceCount');
  if ('citedByCount' in enr) ref.citedByCount = num(enr, 'citedByCount');
  if ('influentialCitationCount' in enr) ref.influentialCitationCount = num(enr, 'influentialCitationCount');
  if (Array.isArray(enr.references)) ref.references = enr.references as Reference['references'];
  if (Array.isArray(enr.citations)) ref.citations = enr.citations as Reference['citations'];
  if (enr.journal !== undefined && enr.journal !== null) ref.journal = enr.journal as Reference['journal'];
  if (enr.openAccess !== undefined && enr.openAccess !== null) ref.openAccess = enr.openAccess as Reference['openAccess'];
  if (Array.isArray(enr.topics)) ref.topics = (enr.topics as unknown[]).filter((x): x is string => typeof x === 'string');
  if (Array.isArray(enr.resourceLinks)) ref.resourceLinks = enr.resourceLinks as Reference['resourceLinks'];
  if ('enrichedAt' in enr) ref.enrichedAt = num(enr, 'enrichedAt');
  if ('enrichSource' in enr) ref.enrichSource = str(enr, 'enrichSource');
}

// Desktop Reference → hub reference body (snake_case wire shape). collectionIds
// are mapped to collection *names* (the hub stores names, not local ids).
function refToHubBody(r: Reference, collName: Map<string, string>): Record<string, unknown> {
  return {
    type: r.type,
    title: r.title,
    authors: r.authors,
    year: r.year,
    venue: r.venue,
    doi: r.doi,
    arxiv_id: r.arxivId,
    url: r.url,
    pdf_url: r.pdfUrl,
    abstract: r.abstract,
    tldr: r.tldr,
    citation_count: r.citationCount ?? r.citedByCount,
    source: r.source,
    external_id: r.externalId,
    tags: r.tags,
    collections: r.collectionIds.map((id) => collName.get(id)).filter((n): n is string => n !== undefined),
    notes: r.notes,
    body_markdown: r.bodyMarkdown,
    details: r.details,
    // The hub schema carries a single attachment; sync the first Zotero-indexed
    // one (managed/local-path attachments stay host-local — bytes never leave).
    zotero_storage: (() => {
      const z = (r.attachments ?? []).find((a) => a.source === 'zotero' && a.key !== undefined);
      return z !== undefined ? { key: z.key ?? '', file: z.file, content_type: z.contentType } : undefined;
    })(),
    enrichment: buildEnrichment(r),
  };
}

// Hub reference row → an ImportItem the library store can merge (carrying the
// hub id as `hubId` so the row links, and collection *names* to find-or-create).
function hubToImportItem(h: Entity): ImportItem {
  const zs = obj(h, 'zotero_storage');
  const ref: ImportItem['ref'] = {
    type: (str(h, 'type') as Reference['type'] | undefined) ?? 'article',
    title: str(h, 'title') ?? '',
    authors: strArray(h, 'authors'),
    year: num(h, 'year'),
    venue: str(h, 'venue'),
    doi: str(h, 'doi'),
    arxivId: str(h, 'arxiv_id'),
    url: str(h, 'url'),
    pdfUrl: str(h, 'pdf_url'),
    abstract: str(h, 'abstract'),
    tldr: str(h, 'tldr'),
    citationCount: num(h, 'citation_count'),
    source: str(h, 'source') as Reference['source'] | undefined,
    externalId: str(h, 'external_id'),
    tags: strArray(h, 'tags'),
    notes: str(h, 'notes') ?? '',
    bodyMarkdown: str(h, 'body_markdown'),
    details: obj(h, 'details') as Record<string, string> | undefined,
    zoteroStorage:
      zs !== undefined && str(zs, 'key') !== undefined
        ? { key: str(zs, 'key') ?? '', file: str(zs, 'file') ?? '', contentType: str(zs, 'content_type') }
        : undefined,
    hubId: str(h, 'id'),
    syncedAt: Date.now(),
  };
  const enr = obj(h, 'enrichment');
  if (enr !== undefined) applyEnrichment(ref, enr);
  return { ref, collectionNames: strArray(h, 'collections') };
}

// Strong-match a local reference against the hub index (used to link a local row
// that has no hubId yet to an existing hub row instead of duplicating it).
function matchHubId(r: Reference, byKey: Map<string, string>): string | undefined {
  const keys: string[] = [];
  if (r.externalId !== undefined && r.externalId !== '') keys.push(`x:${r.externalId}`);
  if (r.doi !== undefined && r.doi !== '') keys.push(`doi:${r.doi.toLowerCase()}`);
  if (r.arxivId !== undefined && r.arxivId !== '') keys.push(`ax:${r.arxivId.toLowerCase()}`);
  for (const k of keys) {
    const id = byKey.get(k);
    if (id !== undefined) return id;
  }
  return undefined;
}

function indexHub(hubRefs: Entity[]): Map<string, string> {
  const m = new Map<string, string>();
  for (const h of hubRefs) {
    const id = str(h, 'id');
    if (id === undefined) continue;
    const ext = str(h, 'external_id');
    const doi = str(h, 'doi');
    const ax = str(h, 'arxiv_id');
    if (ext !== undefined && ext !== '') m.set(`x:${ext}`, id);
    if (doi !== undefined && doi !== '') m.set(`doi:${doi.toLowerCase()}`, id);
    if (ax !== undefined && ax !== '') m.set(`ax:${ax.toLowerCase()}`, id);
  }
  return m;
}

export interface SyncResult {
  pushed: number; // existing hub rows updated from local
  created: number; // new hub rows created from local
  pulledAdded: number; // hub rows new to the device
  pulledMerged: number; // shared rows reconciled
  collectionsCreated: number;
  failed: number; // per-row push errors (sync continues)
}

export async function syncLibrary(client: HubClient): Promise<SyncResult> {
  const lib = useLibrary.getState();
  const collName = new Map(lib.collections.map((c) => [c.id, c.name] as const));

  // 1) PUSH — snapshot locals first; the loop mutates the store as it links ids.
  const locals = lib.references;
  const hubRefs = await client.listReferences();
  const byKey = indexHub(hubRefs);
  let pushed = 0;
  let created = 0;
  let failed = 0;
  for (const r of locals) {
    try {
      const body = refToHubBody(r, collName);
      const linkId = r.hubId ?? matchHubId(r, byKey);
      if (linkId !== undefined) {
        await client.updateReference(linkId, body);
        useLibrary.getState().updateReference(r.id, { hubId: linkId, syncedAt: Date.now() });
        pushed += 1;
      } else {
        const createdRow = await client.createReference(body);
        const newId = str(createdRow, 'id');
        if (newId !== undefined) {
          useLibrary.getState().updateReference(r.id, { hubId: newId, syncedAt: Date.now() });
          created += 1;
        }
      }
    } catch {
      failed += 1;
    }
  }

  // 2) PULL — re-list (now including anything we just created) and merge in.
  const finalHub = await client.listReferences();
  const res = useLibrary.getState().importReferences(finalHub.map(hubToImportItem));

  return {
    pushed,
    created,
    pulledAdded: res.added,
    pulledMerged: res.updated,
    collectionsCreated: res.collectionsCreated,
    failed,
  };
}
