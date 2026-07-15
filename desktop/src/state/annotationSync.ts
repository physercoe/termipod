import type { HubClient } from '../hub/client';
import { num, obj, str, type Entity } from '../hub/types';
import { useLibrary } from './library';
import { useAnnotations, type Annotation, type AnnotationPosition, type AnnotationType } from './annotations';

/// Sync device-local PDF annotations with the hub's `reference_annotations` child
/// entity (ADR-053 companion, migration 0064) so agents and the desktop see one
/// set of highlights/notes per reference.
///
/// v1 semantics mirror `librarySync.ts` — **local-wins union**, but annotations
/// hang off a *parent* reference:
///   1. PUSH each local annotation to the hub under its parent reference (link by
///      `hubId`, else create), writing back `hubId`/`syncedAt`. An annotation
///      whose parent reference has not synced yet (no `Reference.hubId`) is
///      DEFERRED — run `syncLibrary` first so the parents exist.
///   2. PULL each synced reference's hub annotations and add any the device is
///      missing (e.g. an agent drew one). Because we pushed first, shared rows are
///      already linked and skipped.
///
/// The wire shape is snake_case (`page_index`/`sort_index`); the geometry
/// `position` is sent verbatim (the hub stores it opaque as `position_json`), so
/// the Zotero-shaped points survive a round-trip unchanged.

// Desktop annotation → hub annotation body (snake_case wire; position verbatim).
function annToHubBody(a: Annotation): Record<string, unknown> {
  return {
    type: a.type,
    color: a.color,
    page_index: a.pageIndex,
    sort_index: a.sortIndex,
    comment: a.comment,
    text: a.text,
    author: a.author,
    position: a.position,
    tags: a.tags,
  };
}

// Hub annotation row → a local Annotation payload (the parent's *local* id is
// resolved by the caller from the reference's hubId). `hubId`/`syncedAt` link the
// row so a later push updates rather than duplicates.
function hubToAnnotation(h: Entity, referenceId: string): Omit<Annotation, 'id' | 'createdAt' | 'updatedAt'> {
  const pageIndex = num(h, 'page_index') ?? 0;
  const rawPos = obj(h, 'position');
  const position: AnnotationPosition =
    rawPos !== undefined
      ? ({ pageIndex, ...(rawPos as Record<string, unknown>) } as unknown as AnnotationPosition)
      : { pageIndex };
  position.pageIndex = pageIndex; // authoritative from the column
  const tagsRaw = h['tags'];
  const tags = Array.isArray(tagsRaw) ? tagsRaw.filter((x): x is string => typeof x === 'string') : [];
  return {
    referenceId,
    type: (str(h, 'type') as AnnotationType | undefined) ?? 'highlight',
    color: str(h, 'color'),
    pageIndex,
    sortIndex: str(h, 'sort_index'),
    comment: str(h, 'comment'),
    text: str(h, 'text'),
    author: str(h, 'author'),
    position,
    tags,
    hubId: str(h, 'id'),
    syncedAt: Date.now(),
  };
}

export interface AnnotationSyncResult {
  pushed: number; // existing hub rows updated from local
  created: number; // new hub rows created from local
  pulledAdded: number; // hub rows new to the device
  deferred: number; // parent reference not yet synced — skipped this pass
  failed: number; // per-row errors (sync continues)
}

export async function syncAnnotations(client: HubClient): Promise<AnnotationSyncResult> {
  // Parent reference id maps — annotations key on the *local* reference id, the
  // hub keys on the parent's hub id (only present once the reference itself synced).
  const localToHub = new Map<string, string>();
  const hubToLocal = new Map<string, string>();
  for (const r of useLibrary.getState().references) {
    if (r.hubId !== undefined && r.hubId !== '') {
      localToHub.set(r.id, r.hubId);
      hubToLocal.set(r.hubId, r.id);
    }
  }

  // 1) PUSH — snapshot locals first; the loop mutates the store as it links ids.
  const locals = useAnnotations.getState().items;
  let pushed = 0;
  let created = 0;
  let deferred = 0;
  let failed = 0;
  for (const a of locals) {
    const parentHub = localToHub.get(a.referenceId);
    if (parentHub === undefined) {
      deferred += 1; // parent reference has no hubId yet — sync the library first
      continue;
    }
    try {
      const body = annToHubBody(a);
      if (a.hubId !== undefined && a.hubId !== '') {
        await client.updateAnnotation(parentHub, a.hubId, body);
        useAnnotations.getState().update(a.id, { syncedAt: Date.now() });
        pushed += 1;
      } else {
        const row = await client.createAnnotation(parentHub, body);
        const newId = str(row, 'id');
        if (newId !== undefined) {
          useAnnotations.getState().update(a.id, { hubId: newId, syncedAt: Date.now() });
          created += 1;
        }
      }
    } catch {
      failed += 1;
    }
  }

  // 2) PULL — for each synced reference, add any hub annotation the device lacks.
  let pulledAdded = 0;
  const haveHubIds = new Set(
    useAnnotations
      .getState()
      .items.map((a) => a.hubId)
      .filter((x): x is string => x !== undefined && x !== ''),
  );
  for (const [hubRefId, localRefId] of hubToLocal) {
    try {
      const rows = await client.listAnnotations(hubRefId);
      for (const h of rows) {
        const hid = str(h, 'id');
        if (hid === undefined || haveHubIds.has(hid)) continue;
        useAnnotations.getState().add(hubToAnnotation(h, localRefId));
        haveHubIds.add(hid);
        pulledAdded += 1;
      }
    } catch {
      failed += 1;
    }
  }

  return { pushed, created, pulledAdded, deferred, failed };
}
