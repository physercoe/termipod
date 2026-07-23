// Copy the Model Explorer visualizer's self-hosted runtime into the app's static
// assets so the Inspect (J3) interactive model-graph view works fully offline. The
// element (`<model-explorer-visualizer>`, registered by main_browser.js) needs three
// things served same-origin (plan §5, W4 Model Explorer graph):
//   /model-explorer/main_browser.js  — the IIFE that registers the custom element
//   /model-explorer/worker.js        — the layout web worker (must be same-origin)
//   /model-explorer/static_files/*   — font textures + styles the WebGL renderer reads
// `state/modelExplorer.ts` injects the script and points `window.modelExplorer`'s
// `workerScriptPath` / `assetFilesBaseUrl` at these absolute paths. Vite emits
// `public/` at the dist root, so they resolve under both the dev server and the
// packaged `app://` scheme — the same pattern as sync-treesitter-assets.mjs.
//
// The copy is gitignored and regenerated on every build, so the runtime always
// matches the installed `ai-edge-model-explorer-visualizer` — no stale committed blob.
import { copyFile, mkdir, readdir, rm, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const dest = path.join(root, 'public', 'model-explorer');
const src = path.join(root, 'node_modules', 'ai-edge-model-explorer-visualizer', 'dist');

const srcOk = await stat(src).then((s) => s.isDirectory()).catch(() => false);
if (!srcOk) {
  console.error(`[model-explorer-assets] dist not found at ${src} — is ai-edge-model-explorer-visualizer installed?`);
  process.exit(1);
}

await rm(dest, { recursive: true, force: true });
await mkdir(path.join(dest, 'static_files'), { recursive: true });

for (const f of ['main_browser.js', 'worker.js']) {
  const s = path.join(src, f);
  if (!(await stat(s).then((x) => x.isFile()).catch(() => false))) {
    console.error(`[model-explorer-assets] required file missing: ${f}`);
    process.exit(1);
  }
  await copyFile(s, path.join(dest, f));
}

const staticSrc = path.join(src, 'static_files');
let n = 0;
for (const f of await readdir(staticSrc)) {
  const s = path.join(staticSrc, f);
  if (await stat(s).then((x) => x.isFile()).catch(() => false)) {
    await copyFile(s, path.join(dest, 'static_files', f));
    n += 1;
  }
}
console.log(`[model-explorer-assets] copied main_browser.js + worker.js + ${n} static files → ${path.relative(root, dest)}`);
