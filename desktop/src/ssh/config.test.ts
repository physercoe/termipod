/// Unit tests for the OpenSSH config parser/exporter round-trip. Pure functions;
/// `node --test src/ssh/config.test.ts` from `desktop/`. tsc covers types.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { exportSshConfig, parseSshConfig } from './format.ts';
import type { Connection } from '../state/connections';

function conn(over: Partial<Connection>): Connection {
  return {
    id: over.id ?? 'c1',
    name: 'box',
    host: 'box.example.com',
    port: 22,
    username: 'ubuntu',
    authMethod: 'password',
    keyId: null,
    tmuxPath: null,
    createdAt: '2026-01-01T00:00:00Z',
    lastConnectedAt: null,
    deepLinkId: null,
    ...over,
  };
}

test('exportSshConfig: a password host round-trips through parseSshConfig', () => {
  const text = exportSshConfig([conn({ name: 'gpu box', host: '10.0.0.5', port: 2222, username: 'ml' })]);
  const [h] = parseSshConfig(text);
  assert.equal(h.name, 'gpu-box'); // whitespace sanitized (Host aliases can't hold spaces)
  assert.equal(h.host, '10.0.0.5');
  assert.equal(h.user, 'ml');
  assert.equal(h.port, 2222);
  assert.equal(h.identityFile, null); // secrets/keys never exported
});

test('exportSshConfig: default port omitted, defaults recovered on parse', () => {
  const text = exportSshConfig([conn({})]);
  assert.ok(!text.includes('Port'), 'port 22 should be omitted');
  const [h] = parseSshConfig(text);
  assert.equal(h.port, 22);
});

test('exportSshConfig: ProxyJump rendered for jump hosts, with user and non-default port', () => {
  const text = exportSshConfig([conn({ jumpHost: 'bastion.example.com', jumpUsername: 'jump', jumpPort: 2200 })]);
  assert.ok(text.includes('ProxyJump jump@bastion.example.com:2200'), text);
  const plain = exportSshConfig([conn({ jumpHost: 'bastion.example.com' })]);
  assert.ok(plain.includes('ProxyJump bastion.example.com\n'), plain);
});

test('exportSshConfig: key-auth host gets a vault comment, never an IdentityFile', () => {
  const text = exportSshConfig([conn({ authMethod: 'key', keyId: 'k1' })], { k1: 'work-ed25519' });
  assert.ok(text.includes('vault: work-ed25519'), text);
  assert.ok(!/^\s*IdentityFile/m.test(text), 'no IdentityFile line');
  const [h] = parseSshConfig(text);
  assert.equal(h.identityFile, null); // and the parser agrees the comment is inert
});

test('exportSshConfig: duplicate names disambiguated, blank name falls back to host', () => {
  const text = exportSshConfig([conn({ id: 'a', name: 'dev' }), conn({ id: 'b', name: 'dev' }), conn({ id: 'c', name: '  ' })]);
  const hosts = parseSshConfig(text);
  assert.deepEqual(
    hosts.map((h) => h.name),
    ['dev', 'dev-2', 'box.example.com'],
  );
});

test('parseSshConfig: ProxyJump variants — bare, user@host:port, brackets, chain and none skipped', () => {
  const hosts = parseSshConfig(`
Host plain
  HostName t1
  ProxyJump bastion.example.com
Host full
  HostName t2
  ProxyJump jump@bastion.example.com:2200
Host v6
  HostName t3
  ProxyJump [2001:db8::1]:2222
Host chain
  HostName t4
  ProxyJump a.example.com,b.example.com
Host disabled
  HostName t5
  ProxyJump none
`);
  const by = Object.fromEntries(hosts.map((h) => [h.name, h]));
  assert.deepEqual(
    [by.plain.jumpHost, by.plain.jumpPort, by.plain.jumpUser],
    ['bastion.example.com', null, null],
  );
  assert.deepEqual([by.full.jumpHost, by.full.jumpPort, by.full.jumpUser], ['bastion.example.com', 2200, 'jump']);
  assert.deepEqual([by.v6.jumpHost, by.v6.jumpPort], ['2001:db8::1', 2222]);
  // One jump slot in the model: chains and `none` import as no jump at all.
  assert.equal(by.chain.jumpHost, null);
  assert.equal(by.disabled.jumpHost, null);
});

test('exportSshConfig → parseSshConfig round-trips the jump hop', () => {
  const text = exportSshConfig([conn({ jumpHost: 'bastion.example.com', jumpUsername: 'jump', jumpPort: 2200 })]);
  const [h] = parseSshConfig(text);
  assert.equal(h.jumpHost, 'bastion.example.com');
  assert.equal(h.jumpPort, 2200);
  assert.equal(h.jumpUser, 'jump');
  // Default jump port is omitted on export and comes back null on parse.
  const [d] = parseSshConfig(exportSshConfig([conn({ jumpHost: 'b.example.com' })]));
  assert.deepEqual([d.jumpHost, d.jumpPort, d.jumpUser], ['b.example.com', null, null]);
});
