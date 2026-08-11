import { arr, num, obj, str, type Entity } from '../hub/types.ts';
import type { Attachment, Reference } from './library.ts';

export interface PortableAttachmentBody {
  id?: string;
  file: string;
  content_type?: string;
  source: Attachment['source'];
  key: string;
  src_url?: string;
  added_at?: number;
}

function attachmentIdentity(a: Pick<Attachment, 'source' | 'key' | 'file'>): string {
  return `${a.source}\u0000${a.key ?? ''}\u0000${a.file}`;
}

/** Strip host-local paths and keep only coordinates another device can resolve. */
export function portableAttachmentBodies(ref: Pick<Reference, 'attachments'>): PortableAttachmentBody[] {
  return (ref.attachments ?? [])
    .filter((a): a is Attachment & { key: string } => a.key !== undefined && a.key !== '' && a.file !== '')
    .map((a) => ({
      id: a.id,
      file: a.file,
      content_type: a.contentType,
      source: a.source,
      key: a.key,
      src_url: a.srcUrl,
      added_at: a.addedAt,
    }));
}

/** Decode the portable manifest, falling back to the legacy Zotero coordinate. */
export function attachmentsFromHub(h: Entity): Attachment[] {
  const out: Attachment[] = [];
  const seen = new Set<string>();
  const add = (attachment: Attachment): void => {
    const identity = attachmentIdentity(attachment);
    if (seen.has(identity)) return;
    seen.add(identity);
    out.push(attachment);
  };

  for (const value of arr(h, 'attachments')) {
    if (typeof value !== 'object' || value === null || Array.isArray(value)) continue;
    const item = value as Entity;
    const file = str(item, 'file');
    const key = str(item, 'key');
    const source = str(item, 'source');
    if (file === undefined || file === '' || key === undefined || key === '') continue;
    if (source !== 'zotero' && source !== 'managed') continue;
    add({
      id: str(item, 'id') ?? `attremote:${source}:${key}/${file}`,
      file,
      contentType: str(item, 'content_type'),
      source,
      key,
      srcUrl: str(item, 'src_url'),
      addedAt: num(item, 'added_at') ?? 0,
    });
  }

  const legacy = obj(h, 'zotero_storage');
  const legacyKey = legacy !== undefined ? str(legacy, 'key') : undefined;
  const legacyFile = legacy !== undefined ? str(legacy, 'file') : undefined;
  if (legacyKey !== undefined && legacyKey !== '' && legacyFile !== undefined && legacyFile !== '') {
    add({
      id: `attremote:zotero:${legacyKey}/${legacyFile}`,
      file: legacyFile,
      contentType: legacy !== undefined ? str(legacy, 'content_type') : undefined,
      source: 'zotero',
      key: legacyKey,
      addedAt: 0,
    });
  }
  return out;
}

/** Existing clean rows need one metadata push after upgrading from the old design. */
export function needsAttachmentBackfill(ref: Pick<Reference, 'attachments'>, hub: Entity | undefined): boolean {
  return hub !== undefined && portableAttachmentBodies(ref).length > 0 && attachmentsFromHub(hub).length === 0;
}


/** Protect a pre-upgrade receiver from replacing an already-populated manifest. */
export function attachmentsForPush(
  ref: Pick<Reference, 'attachments' | 'attachmentManifestSynced'>,
  hub: Entity | undefined,
): Attachment[] {
  const local = ref.attachments ?? [];
  if (portableAttachmentBodies(ref).length > 0 || ref.attachmentManifestSynced === true || hub === undefined) {
    return local;
  }
  const remote = attachmentsFromHub(hub);
  return remote.length > 0 ? remote : local;
}
/**
 * Make the hub manifest authoritative while retaining coordinates that are valid
 * only on this host. Dirty rows keep local extras until their push succeeds.
 */
export function mergeSyncedAttachments(
  current: Attachment[],
  incoming: Attachment[],
  preserveLocalExtras: boolean,
): Attachment[] {
  const localByIdentity = new Map(current.map((attachment) => [attachmentIdentity(attachment), attachment] as const));
  const seen = new Set<string>();
  const merged: Attachment[] = [];

  for (const remote of incoming) {
    const identity = attachmentIdentity(remote);
    if (seen.has(identity)) continue;
    seen.add(identity);
    const local = localByIdentity.get(identity);
    merged.push(
      local === undefined
        ? remote
        : {
            ...remote,
            ...local,
            contentType: local.contentType ?? remote.contentType,
            srcUrl: local.srcUrl ?? remote.srcUrl,
            key: local.key ?? remote.key,
          },
    );
  }

  if (preserveLocalExtras) {
    for (const local of current) {
      const identity = attachmentIdentity(local);
      if (!seen.has(identity)) merged.push(local);
    }
  }
  return merged;
}
