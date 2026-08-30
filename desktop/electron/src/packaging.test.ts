/// Static package-contract checks. Run with `node --test`.
///
/// These guard metadata that only becomes observable after electron-builder
/// produces an OS bundle. Omitting it does not fail TypeScript or the Electron
/// runtime: macOS simply rejects direct LAN sockets before SSH starts.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

const ELECTRON_DIR = join(dirname(fileURLToPath(import.meta.url)), '..');
const BUILDER_CONFIG = readFileSync(join(ELECTRON_DIR, 'electron-builder.yml'), 'utf8');
const require = createRequire(import.meta.url);
const { rewriteMachOUuids } = require('../scripts/macho-uuid.cjs') as {
  rewriteMachOUuids: (
    buffer: Buffer,
    seed: string,
  ) => { buffer: Buffer; rewritten: Array<{ original: Buffer; replacement: Buffer }> };
};
const { rewriteExecutableUuid } = require('../scripts/after-pack.cjs') as {
  rewriteExecutableUuid: (
    executable: string,
    seed: string,
    signer?: (executable: string) => void,
  ) => Promise<{ buffer: Buffer }>;
};

function electronLikeMachO(): Buffer {
  const buffer = Buffer.alloc(56);
  buffer.writeUInt32LE(0xfeedfacf, 0); // MH_MAGIC_64
  buffer.writeUInt32LE(1, 16); // ncmds
  buffer.writeUInt32LE(24, 20); // sizeofcmds
  buffer.writeUInt32LE(0x1b, 32); // LC_UUID
  buffer.writeUInt32LE(24, 36); // cmdsize
  Buffer.from('4c4c44f555553144a19dc3d9793c6ada', 'hex').copy(buffer, 40);
  return buffer;
}

test('macOS package explains direct local-network access', () => {
  const macBlock = BUILDER_CONFIG.match(/^mac:\n([\s\S]*?)(?=^win:)/m)?.[0];
  assert.ok(macBlock, 'electron-builder.yml must contain a mac block');
  assert.match(macBlock, /^  extendInfo:\n/m);
  assert.match(
    macBlock,
    /^    NSLocalNetworkUsageDescription: .*(SSH|local network)/m,
    'macOS must be able to prompt before TermiPod connects to a LAN SSH host',
  );
});

test('macOS packaging replaces Electron shared executable UUID deterministically', () => {
  const input = electronLikeMachO();
  const first = rewriteMachOUuids(input, 'app.termipod.desktop\u00002026.817.322');
  const again = rewriteMachOUuids(input, 'app.termipod.desktop\u00002026.817.322');
  const nextVersion = rewriteMachOUuids(input, 'app.termipod.desktop\u00002026.817.323');

  assert.equal(first.rewritten.length, 1);
  assert.equal(first.rewritten[0].original.toString('hex'), '4c4c44f555553144a19dc3d9793c6ada');
  assert.notDeepEqual(first.rewritten[0].replacement, first.rewritten[0].original);
  assert.deepEqual(first.buffer, again.buffer);
  assert.notDeepEqual(first.buffer, nextVersion.buffer);
  assert.equal(first.rewritten[0].replacement[6] >> 4, 8, 'replacement must be an RFC 9562 UUIDv8');
  assert.equal(first.rewritten[0].replacement[8] >> 6, 2, 'replacement must use the RFC UUID variant');
});

test('macOS packaging signs the executable after rewriting LC_UUID', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'termipod-after-pack-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const executable = join(directory, 'TermiPod');
  const original = electronLikeMachO();
  await writeFile(executable, original);

  let signedPath: string | undefined;
  let bytesAtSigning: Buffer | undefined;
  const patched = await rewriteExecutableUuid(executable, 'app.termipod.desktop\0test', (path) => {
    signedPath = path;
    bytesAtSigning = readFileSync(path);
  });

  assert.equal(signedPath, executable);
  assert.deepEqual(bytesAtSigning, patched.buffer, 'the signer must observe the rewritten bytes');
  assert.deepEqual(await readFile(executable), patched.buffer);
  assert.notDeepEqual(patched.buffer, original);
});
