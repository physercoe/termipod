import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Connection } from '../state/connections.ts';
import type { VaultBundle } from './bundle.ts';
import { mergeVaultBundles } from './merge.ts';

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
    result.changes.map((change) => ({ id: change.id, relation: change.relation, action: change.action })),
    [
      { id: '10', relation: 'localOnly', action: 'keepLocal' },
      { id: '11', relation: 'localOnly', action: 'keepLocal' },
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
    action: 'useRemote',
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
  assert.deepEqual(result.changes.map((change) => [change.id, change.relation, change.action]), [
    ['1', 'remoteNewer', 'useRemote'],
    ['2', 'localNewer', 'keepLocal'],
  ]);
});

test('legacy same-ID conflict reports unknown age and can use the reviewed Hub choice', () => {
  const local = bundle([connection('1', { name: 'local', updatedAt: undefined, createdAt: '' })]);
  const remote = bundle([connection('1', { name: 'remote', updatedAt: undefined, createdAt: '' })]);
  const result = mergeVaultBundles(local, remote);

  assert.equal(result.bundle.connections[0]?.name, 'local');
  assert.equal(result.changes[0]?.relation, 'ageUnknown');
  assert.equal(result.changes[0]?.action, 'keepLocal');

  const reviewed = mergeVaultBundles(local, remote, { 'connections:1': 'remote' });
  assert.equal(reviewed.bundle.connections[0]?.name, 'remote');
  assert.equal(reviewed.changes[0]?.action, 'useRemote');
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

test('unknown Hub sections survive merge for future clients', () => {
  const remote = bundle();
  remote.futureSection = { version: 2 };

  const result = mergeVaultBundles(bundle(), remote);

  assert.deepEqual(result.bundle.futureSection, { version: 2 });
});
