import type { Connection } from '../state/connections';
import type { SshKeyMeta } from '../state/keys';
import type { VaultItemMeta } from '../state/vaultItems';
import type { VaultBundle } from './bundle';

export type VaultChangeSection = 'connections' | 'sshKeys' | 'items' | 'app' | 'hostPins';
export type VaultChangeRelation =
  | 'localOnly'
  | 'remoteOnly'
  | 'localNewer'
  | 'remoteNewer'
  | 'sameTime'
  | 'ageUnknown';
export type VaultChangeAction = 'keepLocal' | 'useRemote';
export type VaultResolution = 'local' | 'remote';
export type VaultResolutions = Readonly<Record<string, VaultResolution>>;

/** A secret-free description of one difference shown before sync-down. */
export interface VaultChange {
  key: string;
  section: VaultChangeSection;
  id: string;
  label: string;
  relation: VaultChangeRelation;
  action: VaultChangeAction;
  localUpdatedAt: string | null;
  remoteUpdatedAt: string | null;
}

export interface VaultMergeResult {
  bundle: VaultBundle;
  changes: VaultChange[];
}

type Side = 'local' | 'remote';

export function canonicalVaultValue(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalVaultValue).join(',')}]`;
  if (value !== null && typeof value === 'object') {
    return `{${Object.entries(value as Record<string, unknown>)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([key, item]) => `${JSON.stringify(key)}:${canonicalVaultValue(item)}`)
      .join(',')}}`;
  }
  return JSON.stringify(value) ?? 'undefined';
}

/**
 * The local snapshot pinned by a review, minus runtime-only activity that does
 * not affect either the displayed changes or the merge result. Edit clocks
 * stay in this projection because they determine which side wins.
 */
export function vaultReviewProjection(bundle: VaultBundle): unknown {
  return {
    ...bundle,
    connections: bundle.connections.map((connection) => {
      const { lastConnectedAt: _lastConnectedAt, ...reviewed } = connection;
      return reviewed;
    }),
  };
}

function validIso(value: string | null | undefined): string | null {
  if (value === null || value === undefined || Number.isNaN(Date.parse(value))) return null;
  return value;
}

function changedRelation(
  localUpdatedAt: string | null,
  remoteUpdatedAt: string | null,
): { relation: VaultChangeRelation; winner: Side } {
  if (localUpdatedAt !== null && remoteUpdatedAt !== null) {
    const localMs = Date.parse(localUpdatedAt);
    const remoteMs = Date.parse(remoteUpdatedAt);
    if (remoteMs > localMs) return { relation: 'remoteNewer', winner: 'remote' };
    if (localMs > remoteMs) return { relation: 'localNewer', winner: 'local' };
    return { relation: 'sameTime', winner: 'local' };
  }
  // Legacy records and non-record maps have no trustworthy edit clock. Keeping
  // local is the only non-destructive answer; the preview calls the uncertainty
  // out instead of pretending the hub blob's upload time dates every field.
  return { relation: 'ageUnknown', winner: 'local' };
}

interface MergeEntitiesOptions<T extends { id: string }> {
  section: VaultChangeSection;
  local: T[];
  remote: T[];
  label: (item: T) => string;
  updatedAt: (item: T) => string | null;
  compare: (item: T, side: Side) => unknown;
}

interface EntityMerge<T> {
  items: T[];
  winners: Map<string, Side>;
  changes: VaultChange[];
}

function mergeEntities<T extends { id: string }>(
  opts: MergeEntitiesOptions<T>,
  resolutions: VaultResolutions,
): EntityMerge<T> {
  const remoteById = new Map(opts.remote.map((item) => [item.id, item] as const));
  const localIds = new Set(opts.local.map((item) => item.id));
  const winners = new Map<string, Side>();
  const changes: VaultChange[] = [];

  const items = opts.local.map((local) => {
    const remote = remoteById.get(local.id);
    if (remote === undefined) {
      const key = `${opts.section}:${local.id}`;
      winners.set(local.id, 'local');
      changes.push({
        key,
        section: opts.section,
        id: local.id,
        label: opts.label(local),
        relation: 'localOnly',
        action: 'keepLocal',
        localUpdatedAt: opts.updatedAt(local),
        remoteUpdatedAt: null,
      });
      return local;
    }
    if (canonicalVaultValue(opts.compare(local, 'local')) === canonicalVaultValue(opts.compare(remote, 'remote'))) {
      winners.set(local.id, 'local');
      return local;
    }
    const localUpdatedAt = opts.updatedAt(local);
    const remoteUpdatedAt = opts.updatedAt(remote);
    const changed = changedRelation(localUpdatedAt, remoteUpdatedAt);
    const key = `${opts.section}:${local.id}`;
    // Unequal valid clocks are authoritative. A reviewed resolution can only
    // decide equal-clock or clockless conflicts where ordering is ambiguous.
    const winner = changed.relation === 'sameTime' || changed.relation === 'ageUnknown'
      ? resolutions[key] ?? changed.winner
      : changed.winner;
    winners.set(local.id, winner);
    const localLabel = opts.label(local);
    const remoteLabel = opts.label(remote);
    changes.push({
      section: opts.section,
      key,
      id: local.id,
      label: localLabel === remoteLabel ? localLabel : `${localLabel} → ${remoteLabel}`,
      relation: changed.relation,
      action: winner === 'remote' ? 'useRemote' : 'keepLocal',
      localUpdatedAt,
      remoteUpdatedAt,
    });
    return winner === 'remote' ? remote : local;
  });

  for (const remote of opts.remote) {
    if (localIds.has(remote.id)) continue;
    winners.set(remote.id, 'remote');
    items.push(remote);
    changes.push({
      key: `${opts.section}:${remote.id}`,
      section: opts.section,
      id: remote.id,
      label: opts.label(remote),
      relation: 'remoteOnly',
      action: 'useRemote',
      localUpdatedAt: null,
      remoteUpdatedAt: opts.updatedAt(remote),
    });
  }
  return { items, winners, changes };
}

function connectionUpdatedAt(connection: Connection): string | null {
  return validIso(connection.updatedAt) ?? validIso(connection.createdAt);
}

function keyUpdatedAt(key: SshKeyMeta): string | null {
  return validIso(key.createdAt);
}

function itemUpdatedAt(item: VaultItemMeta): string | null {
  return validIso(item.updatedAt) ?? validIso(item.createdAt);
}

function connectionLabel(connection: Connection): string {
  return `${connection.name} (${connection.host}:${connection.port})`;
}

function connectionIdForPassword(key: string): string {
  // Jump-host passwords use `${connectionId}_jump`. This suffix mapping relies
  // on connection IDs being UUIDs (mobile) or base36 newId() values (desktop),
  // neither of which can contain an underscore.
  return key.replace(/_jump$/, '');
}

function comparableConnection(
  connection: Connection,
): Omit<Connection, 'createdAt' | 'lastConnectedAt' | 'updatedAt'> {
  const {
    createdAt: _createdAt,
    lastConnectedAt: _lastConnectedAt,
    updatedAt: _updatedAt,
    ...comparable
  } = connection;
  return comparable;
}

function comparableKey(key: SshKeyMeta): Omit<SshKeyMeta, 'createdAt'> {
  const { createdAt: _createdAt, ...comparable } = key;
  return comparable;
}

function comparableItem(item: VaultItemMeta): Omit<VaultItemMeta, 'createdAt' | 'updatedAt'> {
  const { createdAt: _createdAt, updatedAt: _updatedAt, ...comparable } = item;
  return comparable;
}

function chooseFlatSecrets(
  local: Record<string, string>,
  remote: Record<string, string>,
  winners: Map<string, Side>,
  owner: (key: string) => string,
): Record<string, string> {
  const merged: Record<string, string> = {};
  for (const key of new Set([...Object.keys(local), ...Object.keys(remote)])) {
    const winner = winners.get(owner(key)) ?? 'local';
    const preferred = winner === 'remote' ? remote[key] : local[key];
    const fallback = winner === 'remote' ? local[key] : remote[key];
    const value = preferred ?? fallback;
    if (value !== undefined) merged[key] = value;
  }
  return merged;
}

function chooseItemSecrets(
  local: Record<string, Record<string, string>>,
  remote: Record<string, Record<string, string>>,
  winners: Map<string, Side>,
): Record<string, Record<string, string>> {
  const merged: Record<string, Record<string, string>> = {};
  for (const id of new Set([...Object.keys(local), ...Object.keys(remote)])) {
    const winner = winners.get(id) ?? 'local';
    const preferred = winner === 'remote' ? remote[id] : local[id];
    const fallback = winner === 'remote' ? local[id] : remote[id];
    merged[id] = { ...(fallback ?? {}), ...(preferred ?? {}) };
  }
  return merged;
}

function mergeRecord(
  section: VaultChangeSection,
  kind: string,
  local: Record<string, string>,
  remote: Record<string, string>,
  label: (key: string) => string,
  resolutions: VaultResolutions,
): { value: Record<string, string>; changes: VaultChange[] } {
  const value: Record<string, string> = {};
  const changes: VaultChange[] = [];
  for (const key of new Set([...Object.keys(local), ...Object.keys(remote)])) {
    const changeKey = `${section}:${kind}:${key}`;
    if (!(key in remote)) {
      value[key] = local[key]!;
      changes.push({ key: changeKey, section, id: key, label: label(key), relation: 'localOnly', action: 'keepLocal', localUpdatedAt: null, remoteUpdatedAt: null });
    } else if (!(key in local)) {
      value[key] = remote[key]!;
      changes.push({ key: changeKey, section, id: key, label: label(key), relation: 'remoteOnly', action: 'useRemote', localUpdatedAt: null, remoteUpdatedAt: null });
    } else if (local[key] !== remote[key]) {
      const winner = resolutions[changeKey] ?? 'local';
      value[key] = winner === 'remote' ? remote[key]! : local[key]!;
      changes.push({ key: changeKey, section, id: key, label: label(key), relation: 'ageUnknown', action: winner === 'remote' ? 'useRemote' : 'keepLocal', localUpdatedAt: null, remoteUpdatedAt: null });
    } else {
      value[key] = local[key]!;
    }
  }
  return { value, changes };
}

/**
 * Non-destructively merge a decrypted hub bundle into the device bundle.
 * Remote-only records are added, local-only records survive, and a same-ID
 * change uses the newer record only when both sides carry trustworthy clocks.
 * The returned change list contains labels and dates only, never secret values.
 */
export function mergeVaultBundles(
  local: VaultBundle,
  remote: VaultBundle,
  resolutions: VaultResolutions = {},
): VaultMergeResult {
  const connections = mergeEntities({
    section: 'connections',
    local: local.connections,
    remote: remote.connections,
    label: connectionLabel,
    updatedAt: connectionUpdatedAt,
    compare: (connection, side) => ({
      // updatedAt is the edit clock, not content. lastConnectedAt is runtime
      // activity and must not turn every successful SSH session into a sync
      // conflict.
      meta: comparableConnection(connection),
      password: (side === 'local' ? local.passwords : remote.passwords)[connection.id],
      jumpPassword: (side === 'local' ? local.passwords : remote.passwords)[`${connection.id}_jump`],
    }),
  }, resolutions);
  const keys = mergeEntities({
    section: 'sshKeys',
    local: local.sshKeys.meta,
    remote: remote.sshKeys.meta,
    label: (key) => key.name,
    updatedAt: keyUpdatedAt,
    compare: (key, side) => {
      const source = side === 'local' ? local.sshKeys : remote.sshKeys;
      return {
        meta: comparableKey(key),
        privateKey: source.privateKeys[key.id],
        passphrase: source.passphrases[key.id],
      };
    },
  }, resolutions);

  const remoteCarriesItems = Array.isArray(remote.items);
  const items = remoteCarriesItems
    ? mergeEntities({
        section: 'items',
        local: local.items ?? [],
        remote: remote.items ?? [],
        label: (item) => item.title,
        updatedAt: itemUpdatedAt,
        compare: (item, side) => ({
          meta: comparableItem(item),
          secrets: (side === 'local' ? local.itemSecrets : remote.itemSecrets)?.[item.id],
        }),
      }, resolutions)
    : { items: local.items ?? [], winners: new Map<string, Side>(), changes: [] };

  const appChanges: VaultChange[] = [];
  let app = local.app;
  if (remote.app !== undefined) {
    const config = mergeRecord('app', 'config', local.app?.config ?? {}, remote.app.config ?? {}, (key) => key, resolutions);
    const secrets = mergeRecord('app', 'secret', local.app?.secrets ?? {}, remote.app.secrets ?? {}, (key) => `secret:${key}`, resolutions);
    app = { config: config.value, secrets: secrets.value };
    appChanges.push(...config.changes, ...secrets.changes);
  }

  const pinChanges: VaultChange[] = [];
  let pinnedHostKeys = local.pinnedHostKeys;
  if (remote.pinnedHostKeys !== undefined) {
    const pins = mergeRecord('hostPins', 'pin', local.pinnedHostKeys ?? {}, remote.pinnedHostKeys, (key) => key, resolutions);
    pinnedHostKeys = pins.value;
    pinChanges.push(...pins.changes);
  }

  return {
    bundle: {
      ...local,
      ...remote,
      connections: connections.items,
      sshKeys: {
        meta: keys.items,
        privateKeys: chooseFlatSecrets(local.sshKeys.privateKeys, remote.sshKeys.privateKeys, keys.winners, (id) => id),
        passphrases: chooseFlatSecrets(local.sshKeys.passphrases, remote.sshKeys.passphrases, keys.winners, (id) => id),
      },
      passwords: chooseFlatSecrets(local.passwords, remote.passwords, connections.winners, connectionIdForPassword),
      items: remoteCarriesItems ? items.items : local.items,
      itemSecrets: remoteCarriesItems
        ? chooseItemSecrets(local.itemSecrets ?? {}, remote.itemSecrets ?? {}, items.winners)
        : local.itemSecrets,
      app,
      pinnedHostKeys,
    },
    changes: [...connections.changes, ...keys.changes, ...items.changes, ...appChanges, ...pinChanges],
  };
}
