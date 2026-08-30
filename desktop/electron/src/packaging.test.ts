/// Static package-contract checks. Run with `node --test`.
///
/// These guard metadata that only becomes observable after electron-builder
/// produces an OS bundle. Omitting it does not fail TypeScript or the Electron
/// runtime: macOS simply rejects direct LAN sockets before SSH starts.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

const ELECTRON_DIR = join(dirname(fileURLToPath(import.meta.url)), '..');
const BUILDER_CONFIG = readFileSync(join(ELECTRON_DIR, 'electron-builder.yml'), 'utf8');

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
