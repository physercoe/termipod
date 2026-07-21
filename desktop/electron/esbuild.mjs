// Build the Electron shell's main + preload processes (ADR-055 M1).
//
// Two CommonJS bundles under out/: main.cjs (main process) and preload.cjs
// (a sandboxed preload — must be CJS). `electron` is external (provided by the
// runtime); everything else is bundled so the shell is a self-contained pair of
// files. Node built-ins stay external under platform:node.
import { build } from 'esbuild';

const common = {
  bundle: true,
  platform: 'node',
  target: 'node22', // Electron 43 bundles Node 22
  format: 'cjs',
  // `electron` is the runtime; `@napi-rs/keyring` and `node-pty` are native
  // (.node) addons that can't be bundled (electron-builder asarUnpacks +
  // ABI-rebuilds them in M3); `ws` and `ssh2` are pure JS but pull optional
  // native deps (bufferutil/utf-8-validate, cpu-features) best left unbundled.
  // `sshpk` (optional native ecc-jsbn) and `jszip` (pure JS) are similarly left
  // external. `electron-updater` (pure JS, but large + dynamic requires) too.
  // All resolve from node_modules at runtime.
  external: ['electron', '@napi-rs/keyring', 'node-pty', 'ws', 'ssh2', 'sshpk', 'jszip', 'electron-updater'],
  sourcemap: true,
  logLevel: 'info',
};

await Promise.all([
  build({ ...common, entryPoints: ['src/main.ts'], outfile: 'out/main.cjs' }),
  build({ ...common, entryPoints: ['src/preload.ts'], outfile: 'out/preload.cjs' }),
]);
