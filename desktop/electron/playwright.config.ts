import { defineConfig } from '@playwright/test';

/// Playwright E2E config for the Electron shell (ADR-055 §7 row 14).
///
/// These tests drive the REAL Electron app (via `_electron.launch`), not a
/// browser — so there are no browser `projects`. They run in CI under xvfb
/// (`.github/workflows/desktop.yml` → the `e2e` job); there is no local Electron
/// binary on the dev host, so CI is the gate. The harness's purpose is to let
/// the remaining M4 guard-deletions (blob-iframe, sizedSvg, base64→bytes) be
/// verified against real Chromium behaviour instead of by faith.
export default defineConfig({
  testDir: './e2e',
  // One Electron instance at a time — the app takes a single-instance lock.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : [['list']],
});
