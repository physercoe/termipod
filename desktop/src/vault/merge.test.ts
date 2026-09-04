import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Connection } from '../state/connections.ts';
import type { VaultBundle } from './bundle.ts';
import { canonicalVaultValue, mergeVaultBundles, vaultReviewProjection } from './merge.ts';

function connection(id: string, overrides: Partial<Connection> = {}): Connection {
  return {
    id,
    name: `host-${id}`,
    host: `192.0.2.${id}`,
    port: 22,
    username: 'test',
    authMethod: 'password',
    keyId: null,
    tmuxPath: null,
    createdAt: '2026-08-01T00:00:00.000Z',
    updatedAt: '2026-08-01T00:00:00.000Z',
    lastConnectedAt: null,
    deepLinkId: null,
    ...overrides,
  };
}

function bundle(connections: Connection[] = []): VaultBundle {
  return {
    connections,
    sshKeys: { meta: [], privateKeys: {}, passphrases: {} },
    passwords: {},
    items: [],
    itemSecrets: {},
    app: { config: {}, secrets: {} },
    pinnedHostKeys: {},
  };
}

test('sync-down keeps 11 local connections when the hub snapshot has only 9', () => {
  const localConnections = Array.from({ length: 11 }, (_, i) => connection(String(i + 1)));
  const remoteConnections = localConnections.slice(0, 9).map((item) => ({ ...item }));
  const result = mergeVaultBundles(bundle(localConnections), bundle(remoteConnections));

  assert.equal(result.bundle.connections.length, 11);
  assert.deepEqual(result.bundle.connections.map((item) => item.id), localConnections.map((item) => item.id));
  assert.deepEqual(
    result.changes.map((change) => ({ id: change.id, relation: change.relation, defaultResolution: change.defaultResolution })),
    [
      { id: '10', relation: 'localOnly', defaultResolution: 'local' },
      { id: '11', relation: 'localOnly', defaultResolution: 'local' },
    ],
  );
});

test('hub-only connection is previewed and added', () => {
  const remote = connection('2');
  const result = mergeVaultBundles(bundle([connection('1')]), bundle([connection('1'), remote]));

  assert.deepEqual(result.bundle.connections.map((item) => item.id), ['1', '2']);
  assert.deepEqual(result.changes[0], {
    key: 'connections:2',
    section: 'connections',
    id: '2',
    label: 'host-2 (192.0.2.2:22)',
    relation: 'remoteOnly',
    defaultResolution: 'remote',
    localUpdatedAt: null,
    remoteUpdatedAt: '2026-08-01T00:00:00.000Z',
  });
});

test('same-ID connection and password use the side with the newer edit clock', () => {
  const local = bundle([
    connection('1', { name: 'local', updatedAt: '2026-08-01T00:00:00.000Z' }),
    connection('2', { name: 'local-newer', updatedAt: '2026-08-03T00:00:00.000Z' }),
  ]);
  local.passwords = { '1': 'local-secret-1', '2': 'local-secret-2' };
  const remote = bundle([
    connection('1', { name: 'remote-newer', updatedAt: '2026-08-02T00:00:00.000Z' }),
    connection('2', { name: 'remote', updatedAt: '2026-08-02T00:00:00.000Z' }),
  ]);
  remote.passwords = { '1': 'remote-secret-1', '2': 'remote-secret-2' };

  const result = mergeVaultBundles(local, remote);
  assert.deepEqual(result.bundle.connections.map((item) => item.name), ['remote-newer', 'local-newer']);
  assert.deepEqual(result.bundle.passwords, { '1': 'remote-secret-1', '2': 'local-secret-2' });
  assert.deepEqual(result.changes.map((change) => [change.id, change.relation, change.defaultResolution]), [
    ['1', 'remoteNewer', 'remote'],
    ['2', 'localNewer', 'local'],
  ]);
});

test('legacy same-ID conflict reports unknown age and can use the reviewed Hub choice', () => {
  const local = bundle([connection('1', { name: 'local', updatedAt: undefined, createdAt: '' })]);
  const remote = bundle([connection('1', { name: 'remote', updatedAt: undefined, createdAt: '' })]);
  const result = mergeVaultBundles(local, remote);

  assert.equal(result.bundle.connections[0]?.name, 'local');
  assert.equal(result.changes[0]?.relation, 'ageUnknown');
  assert.equal(result.changes[0]?.defaultResolution, 'local');

  const reviewed = mergeVaultBundles(local, remote, { 'connections:1': 'remote' });
  assert.equal(reviewed.bundle.connections[0]?.name, 'remote');
  assert.equal(reviewed.changes[0]?.defaultResolution, 'local');
});

test('equal edit clocks are explicit conflicts and runtime connection activity is ignored', () => {
  const local = bundle([connection('1', { name: 'local', lastConnectedAt: '2026-08-02T00:00:00.000Z' })]);
  const remote = bundle([connection('1', { name: 'remote', lastConnectedAt: '2026-08-03T00:00:00.000Z' })]);

  const conflict = mergeVaultBundles(local, remote);
  assert.equal(conflict.changes[0]?.relation, 'sameTime');

  remote.connections[0] = connection('1', { name: 'host-1', lastConnectedAt: '2026-08-03T00:00:00.000Z' });
  local.connections[0] = connection('1', { name: 'host-1', lastConnectedAt: '2026-08-02T00:00:00.000Z' });
  assert.deepEqual(mergeVaultBundles(local, remote).changes, []);
});

test('review can override a newer Hub record and keeps the exact local secret state', () => {
  const local = bundle([connection('1', {
    name: 'local-with-password-removed',
    updatedAt: '2026-08-01T00:00:00.000Z',
  })]);
  const remote = bundle([connection('1', {
    name: 'newer-hub',
    updatedAt: '2026-08-02T00:00:00.000Z',
  })]);
  remote.passwords = { '1': 'REMOTE-PASSWORD' };

  const result = mergeVaultBundles(local, remote, { 'connections:1': 'local' }, 'up');

  assert.equal(result.bundle.connections[0]?.name, 'local-with-password-removed');
  assert.deepEqual(result.bundle.passwords, {}, 'a rejected Hub password must not be resurrected as fallback data');
  assert.equal(result.changes[0]?.defaultResolution, 'remote');
});

test('automatic newer-wins resolution retains a missing secret conservatively', () => {
  const local = bundle([connection('1', {
    name: 'older-local',
    updatedAt: '2026-08-01T00:00:00.000Z',
  })]);
  local.passwords = { '1': 'LOCAL-PASSWORD' };
  const remote = bundle([connection('1', {
    name: 'newer-hub-without-password',
    updatedAt: '2026-08-02T00:00:00.000Z',
  })]);

  const result = mergeVaultBundles(local, remote);

  assert.equal(result.bundle.connections[0]?.name, 'newer-hub-without-password');
  assert.deepEqual(result.bundle.passwords, { '1': 'LOCAL-PASSWORD' });
});

test('sync-up can propagate a local connection deletion to the Hub', () => {
  const local = bundle();
  const remote = bundle([connection('1')]);
  remote.passwords = { '1': 'REMOTE-PASSWORD', '1_jump': 'REMOTE-JUMP-PASSWORD' };

  const preview = mergeVaultBundles(local, remote, {}, 'up');
  assert.equal(preview.changes[0]?.relation, 'remoteOnly');
  assert.equal(preview.changes[0]?.defaultResolution, 'remote');

  const result = mergeVaultBundles(local, remote, { 'connections:1': 'local' }, 'up');
  assert.deepEqual(result.bundle.connections, []);
  assert.deepEqual(result.bundle.passwords, {});
});

test('sync-down can accept Hub absence and delete a local-only connection', () => {
  const local = bundle([connection('1')]);
  local.passwords = { '1': 'LOCAL-PASSWORD' };

  const result = mergeVaultBundles(local, bundle(), { 'connections:1': 'remote' }, 'down');

  assert.deepEqual(result.bundle.connections, []);
  assert.deepEqual(result.bundle.passwords, {});
});

test('review projection ignores connection activity but pins merge-relevant clocks', () => {
  const reviewed = bundle([
    connection('1', { lastConnectedAt: '2026-08-02T00:00:00.000Z' }),
  ]);
  const active = structuredClone(reviewed);
  active.connections[0]!.lastConnectedAt = '2026-08-03T00:00:00.000Z';

  assert.equal(
    canonicalVaultValue(vaultReviewProjection(active)),
    canonicalVaultValue(vaultReviewProjection(reviewed)),
  );

  active.connections[0]!.updatedAt = '2026-08-04T00:00:00.000Z';
  assert.notEqual(
    canonicalVaultValue(vaultReviewProjection(active)),
    canonicalVaultValue(vaultReviewProjection(reviewed)),
  );
});

test('preview never exposes changed key or app secret values', () => {
  const local = bundle();
  local.sshKeys = {
    meta: [{
      id: 'key-1', name: 'deploy', type: 'ed25519', publicKey: 'pub', fingerprint: 'fp', hasPassphrase: true,
      createdAt: '2026-08-01T00:00:00.000Z', comment: null, source: 'imported',
    }],
    privateKeys: { 'key-1': 'LOCAL-PRIVATE-KEY' },
    passphrases: { 'key-1': 'LOCAL-PASSPHRASE' },
  };
  local.app = { config: {}, secrets: { voice_dashscope_api_key: 'LOCAL-API-KEY' } };
  const remote = structuredClone(local);
  remote.sshKeys.privateKeys['key-1'] = 'REMOTE-PRIVATE-KEY';
  remote.app!.secrets.voice_dashscope_api_key = 'REMOTE-API-KEY';

  const result = mergeVaultBundles(local, remote);
  const preview = JSON.stringify(result.changes);
  assert.doesNotMatch(preview, /PRIVATE-KEY|PASSPHRASE|API-KEY/);
  assert.deepEqual(result.bundle.sshKeys.privateKeys, { 'key-1': 'LOCAL-PRIVATE-KEY' });
  assert.deepEqual(result.bundle.app?.secrets, { voice_dashscope_api_key: 'LOCAL-API-KEY' });
});

test('timestamp-less app settings and host pins merge additively with local conflicts preserved', () => {
  const local = bundle();
  local.app = { config: { shared: 'local', local: 'yes' }, secrets: {} };
  local.pinnedHostKeys = { shared: 'local-pin', local: 'local-pin' };
  const remote = bundle();
  remote.app = { config: { shared: 'remote', remote: 'yes' }, secrets: {} };
  remote.pinnedHostKeys = { shared: 'remote-pin', remote: 'remote-pin' };

  const result = mergeVaultBundles(local, remote);
  assert.deepEqual(result.bundle.app?.config, { shared: 'local', local: 'yes', remote: 'yes' });
  assert.deepEqual(result.bundle.pinnedHostKeys, { shared: 'local-pin', local: 'local-pin', remote: 'remote-pin' });
  assert.deepEqual(result.changes.filter((change) => change.id === 'shared').map((change) => change.relation), [
    'ageUnknown',
    'ageUnknown',
  ]);

  const reviewed = mergeVaultBundles(local, remote, {
    'app:config:shared': 'remote',
    'hostPins:pin:shared': 'remote',
  });
  assert.equal(reviewed.bundle.app?.config.shared, 'remote');
  assert.equal(reviewed.bundle.pinnedHostKeys?.shared, 'remote-pin');
});

test('one-sided app settings and host pins can be explicitly deleted', () => {
  const local = bundle();
  local.app = { config: { localOnly: 'yes' }, secrets: {} };
  const remote = bundle();
  remote.pinnedHostKeys = { remoteOnly: 'hub-pin' };

  const result = mergeVaultBundles(local, remote, {
    'app:config:localOnly': 'remote',
    'hostPins:pin:remoteOnly': 'local',
  }, 'up');

  assert.deepEqual(result.bundle.app?.config, {});
  assert.deepEqual(result.bundle.pinnedHostKeys, {});
});

test('unknown sections from both sides survive merge and Hub wins a collision', () => {
  const local = bundle() as VaultBundle & Record<string, unknown>;
  local.localFutureSection = { version: 1 };
  local.sharedFutureSection = { source: 'local' };
  const remote = bundle() as VaultBundle & Record<string, unknown>;
  remote.futureSection = { version: 2 };
  remote.sharedFutureSection = { source: 'hub' };

  const result = mergeVaultBundles(local, remote);
  const merged = result.bundle as VaultBundle & Record<string, unknown>;

  assert.deepEqual(merged.localFutureSection, { version: 1 });
  assert.deepEqual(merged.futureSection, { version: 2 });
  assert.deepEqual(merged.sharedFutureSection, { source: 'hub' });
});

test('mobile-authored snapshot previews omitted desktop sections as local-only additions', () => {
  const local = bundle();
  local.items = [{
    id: 'item-1', title: 'Registry', type: 'login', favorite: false,
    username: '', url: '', endpoint: '', format: '', interpreter: '', secretSlots: ['password'],
    createdAt: '2026-08-01T00:00:00.000Z', updatedAt: '2026-08-01T00:00:00.000Z',
  }];
  local.itemSecrets = { 'item-1': { password: 'LOCAL-ITEM-SECRET' } };
  local.app = { config: { sync_provider: 'webdav' }, secrets: { sync_password: 'LOCAL-APP-SECRET' } };
  local.pinnedHostKeys = { 'host-1': 'LOCAL-PIN' };

  const remote = bundle();
  remote.items = undefined;
  remote.itemSecrets = undefined;
  remote.app = undefined;
  remote.pinnedHostKeys = undefined;

  const down = mergeVaultBundles(local, remote);
  assert.deepEqual(down.changes, [], 'sync-down must not list local sections an older Hub bundle cannot change');

  const result = mergeVaultBundles(local, remote, {}, 'up');
  assert.equal(result.bundle.items?.length, 1);
  assert.deepEqual(result.bundle.itemSecrets, local.itemSecrets);
  assert.deepEqual(result.bundle.app, local.app);
  assert.deepEqual(result.bundle.pinnedHostKeys, local.pinnedHostKeys);
  assert.deepEqual(
    result.changes.map((change) => [change.section, change.id, change.relation, change.defaultResolution]),
    [
      ['items', 'item-1', 'localOnly', 'local'],
      ['app', 'sync_provider', 'localOnly', 'local'],
      ['app', 'sync_password', 'localOnly', 'local'],
      ['hostPins', 'host-1', 'localOnly', 'local'],
    ],
  );
  assert.doesNotMatch(JSON.stringify(result.changes), /LOCAL-(?:ITEM|APP)-SECRET|LOCAL-PIN/);
});
