// Copy web-tree-sitter's core WASM + the curated grammar WASMs into the app's
// static assets so the Inspect (J3) code outline parses fully offline. The
// runtime loads them by absolute path — `/tree-sitter/web-tree-sitter.wasm`
// (the parser core, via Parser.init's locateFile) and
// `/tree-sitter/grammars/tree-sitter-<lang>.wasm` (per-language, lazily fetched
// on first use). Vite emits `public/` at the dist root, so those paths resolve
// under both the dev server and the packaged `app://` scheme — the same pattern
// as sync-excalidraw-assets.mjs.
//
// The copy is gitignored and regenerated on every build, so the grammars always
// match the installed web-tree-sitter ABI — no stale committed binaries.
import { copyFile, mkdir, rm, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const dest = path.join(root, 'public', 'tree-sitter');
const grammarsDest = path.join(dest, 'grammars');

// Keep in lockstep with the registry in src/state/treeSitter.ts — every grammar
// the outline queries reference must be copied here (and nothing else, to keep
// the shipped asset set lean).
const GRAMMARS = ['python', 'javascript', 'typescript', 'tsx', 'go', 'rust', 'java', 'ruby', 'bash', 'cpp', 'c-sharp', 'php'];

const core = path.join(root, 'node_modules', 'web-tree-sitter', 'web-tree-sitter.wasm');
const grammarSrc = path.join(root, 'node_modules', '@vscode', 'tree-sitter-wasm', 'wasm');

const coreOk = await stat(core).then((s) => s.isFile()).catch(() => false);
if (!coreOk) {
  console.error(`[tree-sitter-assets] core wasm not found at ${core} — is web-tree-sitter installed?`);
  process.exit(1);
}

await rm(dest, { recursive: true, force: true });
await mkdir(grammarsDest, { recursive: true });
await copyFile(core, path.join(dest, 'web-tree-sitter.wasm'));

let n = 0;
for (const g of GRAMMARS) {
  const name = `tree-sitter-${g}.wasm`;
  const src = path.join(grammarSrc, name);
  const ok = await stat(src).then((s) => s.isFile()).catch(() => false);
  if (!ok) {
    console.error(`[tree-sitter-assets] grammar not found: ${name} (in @vscode/tree-sitter-wasm)`);
    process.exit(1);
  }
  await copyFile(src, path.join(grammarsDest, name));
  n += 1;
}
console.log(`[tree-sitter-assets] copied core + ${n} grammars → ${path.relative(root, dest)}`);
