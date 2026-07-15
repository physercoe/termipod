import { loadJson, newId, saveJson, secretDelete, secretGet, secretSet } from './persist';

/// Generic vault items — the "mini-1Password" store that sits alongside the
/// specialised SSH-key and connection stores. Modelled on 1Password / Bitwarden
/// item categories: a small set of typed items, each with a few structured
/// fields plus free-form notes.
///
/// Split of concerns, matching the rest of the app (connections.ts / keys.ts):
///   • Non-secret display metadata (title, username, url, endpoint, timestamps)
///     is plain JSON in localStorage under `vault_items`.
///   • Every sensitive value (password, TOTP seed, API token, note body, notes)
///     lives in the OS keychain under `vaultitem_<id>_<slot>` — never on disk in
///     the clear — reusing the chunked `secretSet` so long tokens are safe on
///     Windows Credential Manager.
///
/// The whole store seals into the zero-knowledge vault for cross-device sync
/// (see vault/bundle.ts): the metadata list plus a per-item map of its secrets.

export type VaultItemType = 'login' | 'api' | 'note';

/// The secret slots each item type can hold. `notes` (free-form) is common to
/// every type; the rest are type-specific. Kept as a plain list so assemble /
/// import / delete know exactly which keychain entries to touch without being
/// able to enumerate the keychain.
export const SECRET_SLOTS: Record<VaultItemType, string[]> = {
  login: ['password', 'totp', 'notes'],
  api: ['token', 'notes'],
  note: ['content', 'notes'],
};

export interface VaultItemMeta {
  id: string;
  type: VaultItemType;
  title: string;
  favorite: boolean;
  // Structured, non-secret display fields (used by the relevant types only).
  username: string; // login
  url: string; // login — the website
  endpoint: string; // api — the base URL / host the token is for
  // Which secret slots actually hold a value in the keychain right now.
  secretSlots: string[];
  createdAt: string; // ISO-8601
  updatedAt: string; // ISO-8601
}

const STORAGE_KEY = 'vault_items';

function slotKey(id: string, slot: string): string {
  return `vaultitem_${id}_${slot}`;
}

export function listItems(): VaultItemMeta[] {
  return loadJson<VaultItemMeta[]>(STORAGE_KEY, []);
}

/** One secret slot's value ('' when absent), for the reveal / copy affordances. */
export async function getItemSecret(id: string, slot: string): Promise<string> {
  return (await secretGet(slotKey(id, slot))) ?? '';
}

export interface SaveItemInput {
  id?: string;
  type: VaultItemType;
  title: string;
  favorite?: boolean;
  username?: string;
  url?: string;
  endpoint?: string;
  /** slot → plaintext value; '' (or omitted) clears/removes that slot. */
  secrets?: Record<string, string>;
}

/// Create or update an item: write its secrets to the keychain, then persist the
/// non-secret metadata. Slots set to '' are removed. Returns the stored record.
export async function saveItem(input: SaveItemInput): Promise<VaultItemMeta> {
  const list = listItems();
  const id = input.id ?? newId();
  const existing = list.find((i) => i.id === id);

  const slots = new Set(existing?.secretSlots ?? []);
  const allowed = new Set(SECRET_SLOTS[input.type]);
  for (const [slot, valueRaw] of Object.entries(input.secrets ?? {})) {
    if (!allowed.has(slot)) continue; // ignore slots not valid for this type
    const value = valueRaw ?? '';
    const k = slotKey(id, slot);
    if (value === '') {
      await secretDelete(k);
      slots.delete(slot);
    } else {
      await secretSet(k, value);
      slots.add(slot);
    }
  }
  // If the type changed, drop any secret slots no longer valid for it.
  for (const slot of [...slots]) {
    if (!allowed.has(slot)) {
      await secretDelete(slotKey(id, slot));
      slots.delete(slot);
    }
  }

  const now = new Date().toISOString();
  const meta: VaultItemMeta = {
    id,
    type: input.type,
    title: input.title,
    favorite: input.favorite ?? existing?.favorite ?? false,
    username: input.username ?? existing?.username ?? '',
    url: input.url ?? existing?.url ?? '',
    endpoint: input.endpoint ?? existing?.endpoint ?? '',
    secretSlots: [...slots],
    createdAt: existing?.createdAt ?? now,
    updatedAt: now,
  };
  const next = existing ? list.map((i) => (i.id === id ? meta : i)) : [...list, meta];
  saveJson(STORAGE_KEY, next);
  return meta;
}

/** Flip an item's favorite flag in place (metadata-only, no secret I/O). */
export function toggleFavorite(id: string): VaultItemMeta[] {
  const list = listItems().map((i) =>
    i.id === id ? { ...i, favorite: !i.favorite, updatedAt: new Date().toISOString() } : i,
  );
  saveJson(STORAGE_KEY, list);
  return list;
}

export async function deleteItem(id: string): Promise<void> {
  const item = listItems().find((i) => i.id === id);
  if (item !== undefined) {
    for (const slot of item.secretSlots) await secretDelete(slotKey(id, slot));
  }
  saveJson(
    STORAGE_KEY,
    listItems().filter((i) => i.id !== id),
  );
}

// ── Sync helpers (used by vault/bundle.ts) ──────────────────────────────────

export interface VaultItemsExport {
  items: VaultItemMeta[];
  itemSecrets: Record<string, Record<string, string>>; // id → (slot → value)
}

/** Gather every item's metadata + secrets for sealing into the vault bundle. */
export async function exportItems(): Promise<VaultItemsExport> {
  const items = listItems();
  const itemSecrets: Record<string, Record<string, string>> = {};
  for (const it of items) {
    const m: Record<string, string> = {};
    for (const slot of it.secretSlots) {
      const v = await secretGet(slotKey(it.id, slot));
      if (v !== null) m[slot] = v;
    }
    itemSecrets[it.id] = m;
  }
  return { items, itemSecrets };
}

/** Restore items pulled from the vault: overwrite the metadata list and scatter
 * the secrets back into the keychain. The vault is source-of-truth on a pull. */
export async function importItems(
  items: VaultItemMeta[],
  itemSecrets: Record<string, Record<string, string>>,
): Promise<void> {
  saveJson(STORAGE_KEY, items);
  for (const [id, slots] of Object.entries(itemSecrets ?? {})) {
    for (const [slot, val] of Object.entries(slots)) await secretSet(slotKey(id, slot), val);
  }
}
