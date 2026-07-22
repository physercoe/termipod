// Copy Excalidraw's bundled fonts into the app's static assets so the editor
// renders fully offline (ADR-055 figure-plan Phase C). Excalidraw loads its
// fonts at runtime from `${window.EXCALIDRAW_ASSET_PATH}fonts/…` and falls back
// to the esm.sh CDN when that path is unset or empty — a network fetch we must
// not make. We serve them locally instead: this copies the package's
// `dist/prod/fonts` tree into `public/excalidraw-assets/fonts`, which Vite emits
// at the dist root, so the runtime path `/excalidraw-assets/` resolves on-disk
// under both the dev server and the packaged `app://` scheme.
//
// The copy is gitignored (14 MB of woff2) and regenerated on every build, so the
// fonts always match the installed package version — no stale committed binaries.
import { cp, mkdir, rm, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const src = path.join(root, 'node_modules', '@excalidraw', 'excalidraw', 'dist', 'prod', 'fonts');
const dest = path.join(root, 'public', 'excalidraw-assets', 'fonts');

const ok = await stat(src).then((s) => s.isDirectory()).catch(() => false);
if (!ok) {
  console.error(`[excalidraw-assets] source fonts not found at ${src} — is @excalidraw/excalidraw installed?`);
  process.exit(1);
}

await rm(dest, { recursive: true, force: true });
await mkdir(path.dirname(dest), { recursive: true });
await cp(src, dest, { recursive: true });
console.log(`[excalidraw-assets] copied fonts → ${path.relative(root, dest)}`);
