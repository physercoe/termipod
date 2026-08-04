/// Silent-reconnect request assembly (savedConnect.ts). Pure of the stores —
/// secrets arrive through the injected IO — so every branch runs under
/// `node --test src/terminal/savedConnect.test.ts` from `desktop/`.
///
/// The load-bearing assertions are the `null`s: a `null` is what sends the
/// caller to the connect form, and a builder that guessed instead (an empty
/// password, a blank key) would attempt a connect that can only fail — or
/// worse, succeed against the wrong credential source.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Connection } from '../state/connections';
import { buildSavedConnectReq, connectTimeoutMs, type SavedConnectIO } from './savedConnect.ts';

function conn(over: Partial<Connection>): Connection {
  return {
    id: 'c1',
    name: 'box',
    host: 'host.example.com',
    port: 22,
    username: 'wb',
    authMethod: 'password',
    keyId: null,
    tmuxPath: null,
    group: null,
    createdAt: '2026-08-04T00:00:00Z',
    lastConnectedAt: null,
    deepLinkId: null,
    ...over,
  };
}

function io(over: Partial<SavedConnectIO>): SavedConnectIO {
  return {
    getPassword: () => Promise.resolve(null),
    getJumpPassword: () => Promise.resolve(null),
    getKey: () => Promise.resolve({ pem: null, passphrase: null }),
    ...over,
  };
}

test('password auth replays the vault slot', async () => {
  const req = await buildSavedConnectReq(conn({}), 'r1', io({ getPassword: () => Promise.resolve('pw') }));
  assert.deepEqual(req, {
    host: 'host.example.com',
    port: 22,
    user: 'wb',
    cols: 80,
    rows: 24,
    connect_id: 'r1',
    password: 'pw',
  });
});

test('an empty or missing vault slot is a null, not an empty-password attempt', async () => {
  assert.equal(await buildSavedConnectReq(conn({}), 'r1', io({})), null);
  assert.equal(await buildSavedConnectReq(conn({}), 'r1', io({ getPassword: () => Promise.resolve('') })), null);
});

test('key auth replays the key store; a pasted-key connection cannot be replayed', async () => {
  const withKey = io({ getKey: () => Promise.resolve({ pem: 'PEM', passphrase: 'pp' }) });
  const req = await buildSavedConnectReq(conn({ authMethod: 'key', keyId: 'k1' }), 'r1', withKey);
  assert.equal(req?.private_key, 'PEM');
  assert.equal(req?.passphrase, 'pp');
  assert.equal(req?.password, undefined);
  // keyId null = the key was pasted into the form, never stored.
  assert.equal(await buildSavedConnectReq(conn({ authMethod: 'key', keyId: null }), 'r1', withKey), null);
  // A key row whose material is gone (vault cleared) is a null too.
  assert.equal(await buildSavedConnectReq(conn({ authMethod: 'key', keyId: 'k1' }), 'r1', io({})), null);
});

test('a passphrase-less key omits the field rather than sending null', async () => {
  const req = await buildSavedConnectReq(
    conn({ authMethod: 'key', keyId: 'k1' }),
    'r1',
    io({ getKey: () => Promise.resolve({ pem: 'PEM', passphrase: null }) }),
  );
  assert.ok(req !== null && !('passphrase' in req));
});

test('jump: empty jump password reuses the main password (mobile parity); blank jump user is omitted', async () => {
  const req = await buildSavedConnectReq(
    conn({ jumpHost: 'bastion', jumpPort: 2222, jumpUsername: '' }),
    'r1',
    io({ getPassword: () => Promise.resolve('pw') }),
  );
  assert.equal(req?.jump_host, 'bastion');
  assert.equal(req?.jump_port, 2222);
  assert.equal(req?.jump_password, 'pw');
  assert.ok(!('jump_user' in (req ?? {})));
});

test('jump: its own stored password wins over the main one', async () => {
  const req = await buildSavedConnectReq(
    conn({ jumpHost: 'bastion', jumpUsername: 'root' }),
    'r1',
    io({ getPassword: () => Promise.resolve('pw'), getJumpPassword: () => Promise.resolve('jpw') }),
  );
  assert.equal(req?.jump_password, 'jpw');
  assert.equal(req?.jump_user, 'root');
});

test('jump: key-auth main + password jump with NO stored jump password cannot be replayed', async () => {
  // The form refuses this too (canConnect): there is no password anywhere to
  // reuse, so the silent path must hand over to the form, not attempt.
  const req = await buildSavedConnectReq(
    conn({ authMethod: 'key', keyId: 'k1', jumpHost: 'bastion' }),
    'r1',
    io({ getKey: () => Promise.resolve({ pem: 'PEM', passphrase: null }) }),
  );
  assert.equal(req, null);
});

test('jump: key-auth jump replays its own key; a missing jump key is a null', async () => {
  const keys: Record<string, { pem: string | null; passphrase: string | null }> = {
    k1: { pem: 'MAIN', passphrase: null },
    jk: { pem: 'JUMP', passphrase: 'jp' },
  };
  const both = io({ getKey: (id) => Promise.resolve(keys[id] ?? { pem: null, passphrase: null }) });
  const req = await buildSavedConnectReq(
    conn({ authMethod: 'key', keyId: 'k1', jumpHost: 'bastion', jumpAuthMethod: 'key', jumpKeyId: 'jk' }),
    'r1',
    both,
  );
  assert.equal(req?.private_key, 'MAIN');
  assert.equal(req?.jump_private_key, 'JUMP');
  assert.equal(req?.jump_passphrase, 'jp');
  const noJumpKey = await buildSavedConnectReq(
    conn({ authMethod: 'key', keyId: 'k1', jumpHost: 'bastion', jumpAuthMethod: 'key', jumpKeyId: null }),
    'r1',
    both,
  );
  assert.equal(noJumpKey, null);
});

test('proxy fields ride along; blank optional ones are omitted', async () => {
  const req = await buildSavedConnectReq(
    conn({ proxyHost: '127.0.0.1', proxyPort: 9050, proxyUsername: '', proxyPassword: null }),
    'r1',
    io({ getPassword: () => Promise.resolve('pw') }),
  );
  assert.equal(req?.proxy_host, '127.0.0.1');
  assert.equal(req?.proxy_port, 9050);
  assert.ok(!('proxy_username' in (req ?? {})));
  assert.ok(!('proxy_password' in (req ?? {})));
});

test('the attempt ceiling matches the form: base 20s, +15s jump, +10s proxy', () => {
  assert.equal(connectTimeoutMs({}), 20_000);
  assert.equal(connectTimeoutMs({ jumpHost: 'b' }), 35_000);
  assert.equal(connectTimeoutMs({ proxyHost: 'p' }), 30_000);
  assert.equal(connectTimeoutMs({ jumpHost: 'b', proxyHost: 'p' }), 45_000);
});
