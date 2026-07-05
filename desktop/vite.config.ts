import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The generated shared design tokens live outside this package
// (../design-tokens/build), so allow Vite to serve one level up.
export default defineConfig({
  plugins: [react()],
  server: {
    fs: { allow: ['..'] },
  },
});
