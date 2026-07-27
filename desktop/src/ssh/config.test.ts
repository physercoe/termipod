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
