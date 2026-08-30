import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { Connection } from '../state/connections';
import { rememberConnection, type ConnectionDraft, type ConnectionDraftIO } from './connectionDraft.ts';

function draft(over: Partial<ConnectionDraft> = {}): ConnectionDraft {
  return {
    id: null,
    name: '',
    group: 'default',
    note: '',
    host: ' host.example.com ',
    port: '2222',
    user: ' wb ',
    auth: 'password',
    password: 'secret',
    keyId: '',
    useJump: false,
    jumpHost: '',
    jumpPort: '22',
    jumpUser: '',
    jumpAuth: 'password',
    jumpKeyId: '',
    jumpPassword: '',
    useProxy: false,
    proxyHost: '',
    proxyPort: '1080',
    proxyUser: '',
    proxyPassword: '',
    ...over,
  };
}

function stored(input: Parameters<ConnectionDraftIO['upsert']>[0]): Connection {
  return {
    id: input.id ?? 'new-id',
    name: input.name,
    host: input.host,
    port: input.port ?? 22,
    username: input.username,
    authMethod: input.authMethod ?? 'password',
    keyId: input.keyId ?? null,
    tmuxPath: null,
    group: input.group ?? null,
    createdAt: '2026-08-12T00:00:00Z',
    lastConnectedAt: null,
    deepLinkId: null,
  };
}

test('Connect persistence creates a reusable row and stores passwords through secret IO', async () => {
  let input: Parameters<ConnectionDraftIO['upsert']>[0] | null = null;
  const secrets: string[] = [];
  const conn = await rememberConnection(
    draft({
      useJump: true,
      jumpHost: ' bastion.example.com ',
      jumpPort: '2200',
      jumpUser: ' root ',
      jumpPassword: 'jump-secret',
      useProxy: true,
      proxyHost: ' proxy.example.com ',
      proxyPort: '1081',
      proxyUser: ' proxy-user ',
      proxyPassword: 'proxy-secret',
      note: ' GPU node; CUDA 13 and maintenance on Fridays. ',
    }),
    {
      defaultGroup: 'default',
      upsert: (next) => {
        input = next;
        return stored(next);
      },
      setPassword: (id, password) => {
        secrets.push(`main:${id}:${password}`);
        return Promise.resolve();
      },
      setJumpPassword: (id, password) => {
        secrets.push(`jump:${id}:${password}`);
        return Promise.resolve();
      },
    },
  );

  assert.equal(conn.id, 'new-id');
  assert.deepEqual(input, {
    id: undefined,
    name: 'host.example.com',
    group: 'default',
    note: 'GPU node; CUDA 13 and maintenance on Fridays.',
    host: 'host.example.com',
    port: 2222,
    username: 'wb',
    authMethod: 'password',
    keyId: null,
    jumpHost: 'bastion.example.com',
    jumpPort: 2200,
    jumpUsername: 'root',
    jumpAuthMethod: 'password',
    jumpKeyId: null,
    proxyHost: 'proxy.example.com',
    proxyPort: 1081,
    proxyUsername: 'proxy-user',
    proxyPassword: 'proxy-secret',
  });
  assert.deepEqual(secrets, ['main:new-id:secret', 'jump:new-id:jump-secret']);
});

test('updating key auth keeps the row id and clears disabled hop fields and password slots', async () => {
  const inputs: Parameters<ConnectionDraftIO['upsert']>[0][] = [];
  const secrets: string[] = [];
  await rememberConnection(draft({ id: 'existing', auth: 'key', keyId: 'key-1', password: 'ignored' }), {
    defaultGroup: 'default',
    upsert: (next) => {
      inputs.push(next);
      return stored(next);
    },
    setPassword: () => {
      throw new Error('key auth must not write a main password');
    },
    setJumpPassword: (id, password) => {
      secrets.push(`${id}:${password}`);
      return Promise.resolve();
    },
  });

  const input = inputs[0];
  assert.ok(input !== undefined);
  assert.equal(input?.id, 'existing');
  assert.equal(input?.keyId, 'key-1');
  assert.equal(input?.note, null);
  assert.equal(input?.jumpHost, null);
  assert.equal(input?.proxyHost, null);
  assert.deepEqual(secrets, ['existing:']);
});
