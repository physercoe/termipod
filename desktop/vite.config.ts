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
  server: {
    fs: { allow: ['..'] },
  },
});
