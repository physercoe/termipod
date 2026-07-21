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
  // (.node) addons that can't be bundled — all resolve from node_modules at
  // runtime (electron-builder asarUnpacks + ABI-rebuilds the native addons in M3).
  external: ['electron', '@napi-rs/keyring', 'node-pty'],
  sourcemap: true,
  logLevel: 'info',
};

await Promise.all([
  build({ ...common, entryPoints: ['src/main.ts'], outfile: 'out/main.cjs' }),
  build({ ...common, entryPoints: ['src/preload.ts'], outfile: 'out/preload.cjs' }),
]);
