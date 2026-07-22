import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import pkg from './package.json';

// The app version, injected at build time from package.json so the UI can paint
// it on the first frame instead of waiting on the async native `app_version`
// call (which caused a version "splash" in Settings). desktop/package.json is
// the source of truth; CI stamps it into desktop/electron/package.json for the
// packaged build.

// The generated shared design tokens live outside this package
// (../design-tokens/build), so allow Vite to serve one level up.
export default defineConfig({
  plugins: [react()],
  define: {
    __APP_VERSION__: JSON.stringify(pkg.version),
  },
  build: {
    // ADR-055 §7 row 13: the app runs on the pinned Chromium the Electron shell
    // bundles (Electron 43 ⇒ Chromium ~138), not a multi-engine matrix, so target
    // a modern floor well below that. esbuild keeps modern syntax instead of
    // down-levelling it → smaller output. (`chrome120` is also broadly supported
    // by the plain-browser degrade build.)
    target: 'chrome120',
  },
  server: {
    fs: { allow: ['..'] },
  },
});
