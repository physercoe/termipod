/// Regression tests for the ssh2 interop-namespace gap (see ssh2mod.ts's
/// header): through a real dynamic `import('ssh2')` — what the esbuild bundle
/// emits — the cjs-module-lexer namespace lacks `utils`, which used to send
/// every TOFU host-key check down the verify(false) path ("Host denied
/// (verification failed)" on first contact, no pin ever written). These pin
/// the normalized loader plus the exact raw-wire-blob line-build the
/// hostVerifier performs. Run with `node --test` (Node 22+ strips the TS
/// types natively).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync } from 'node:crypto';
import { loadSsh2 } from './ssh2mod.ts';

test('loadSsh2 exposes Client and utils.parseKey through the interop namespace', async () => {
  const ssh2 = await loadSsh2();
  assert.equal(typeof ssh2.Client, 'function');
  assert.equal(typeof ssh2.utils, 'object');
  assert.equal(typeof ssh2.utils.parseKey, 'function');
});

test('host-key line builds from a raw wire-format blob (the TOFU compare value)', async () => {
  const ssh2 = await loadSsh2();
  // What ssh2's kex hands the hostVerifier at connect time:
  // string(algorithm) || string(raw public key), each uint32-length-prefixed.
  const { publicKey } = generateKeyPairSync('ed25519');
  const raw = publicKey.export({ format: 'der', type: 'spki' }).subarray(-32);
  const alg = Buffer.from('ssh-ed25519');
  const len = (b: Buffer): Buffer => {
    const l = Buffer.alloc(4);
    l.writeUInt32BE(b.length);
    return l;
  };
  const blob = Buffer.concat([len(alg), alg, len(raw), raw]);

  const parsed = ssh2.utils.parseKey(blob);
  if (parsed instanceof Error) assert.fail(`parseKey rejected a raw host-key blob: ${parsed.message}`);
  const k = Array.isArray(parsed) ? parsed[0] : parsed;
  const line = `${k.type} ${k.getPublicSSH().toString('base64')}`;
  assert.match(line, /^ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA/);
});
