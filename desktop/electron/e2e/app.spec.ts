import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';
import http from 'node:http';
import protobuf from 'protobufjs';
import type { AddressInfo } from 'node:net';

// The preload injects this on the renderer's `window` (see src/preload.ts). It
// is declared in the frontend package, not here, so mirror the minimal surface
// these tests touch.
declare global {
  interface Window {
    __ELECTRON_BRIDGE__?: {
      invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T>;
      listen<T>(name: string, cb: (e: { payload: T }) => void): Promise<() => void>;
    };
  }
}

/// Smoke suite for the Electron shell (ADR-055 §7 row 14).
///
/// Proves the harness works end-to-end and pins the invariants the M4 paydowns
/// rely on:
///   - the app boots and paints (no blank/black screen — the WebView2 render
///     bugs the guards compensated for do not exist under Chromium),
///   - the preload bridge is injected and a native command round-trips,
///   - the renderer is a secure `app://` context (so `crypto.randomUUID` is
///     available — the assumption behind §7 row 12).
///
/// Runs UNPACKAGED: launch `out/main.cjs`, which loads the frontend from
/// `desktop/dist` (`TERMIPOD_DIST`). Native addons (node-pty, keyring) are
/// lazily imported, so boot needs no ABI rebuild — a terminal/SSH suite that
/// exercises them would (add `electron-builder install-app-deps` then).

// Playwright runs test files as CommonJS (the electron package is not ESM), so
// `__dirname` is the e2e/ dir.
const MAIN_ENTRY = path.resolve(__dirname, '..', 'out', 'main.cjs');
const DIST_DIR = path.resolve(__dirname, '..', '..', 'dist');

// `--no-sandbox` (the Chromium setuid sandbox can't run in an unprivileged CI
// container) and `--disable-gpu` (xvfb has no real GPU — force SwiftShader).
const CI_FLAGS = ['--no-sandbox', '--disable-gpu'];

let app: ElectronApplication;
let page: Page;

test.beforeAll(async () => {
  app = await electron.launch({
    args: [...CI_FLAGS, MAIN_ENTRY],
    env: { ...process.env, TERMIPOD_DIST: DIST_DIR, TERMIPOD_E2E: '1' },
  });
  page = await app.firstWindow();
  await page.waitForLoadState('domcontentloaded');
});

test.afterAll(async () => {
  await app?.close();
});

test.afterEach(async ({}, testInfo) => {
  // A screenshot on failure is the only forensic signal for a CI-only run.
  if (testInfo.status !== testInfo.expectedStatus && page) {
    await testInfo.attach('screenshot', { body: await page.screenshot(), contentType: 'image/png' });
  }
});

// The "Add a hub" connect modal auto-opens once when `init()` settles offline
// (AppShell.tsx) — at a non-deterministic time, so it can pop up during any late
// test and its backdrop then blocks clicks. Dismiss it by CLICKING its close
// button (Escape is unreliable through the focus trap) in a `toPass` loop that
// absorbs the open/animation race; a no-op when the modal is absent (count 0).
// Same pattern as the excalidraw smoke below.
async function dismissConnectModal(): Promise<void> {
  await expect(async () => {
    const closeBtn = page.locator('.connect .connect-head button');
    if ((await closeBtn.count()) > 0) await closeBtn.click({ timeout: 2000 });
    await expect(page.locator('.connect')).toHaveCount(0);
  }).toPass({ timeout: 15_000 });
}

function onePagePdfBytes(): number[] {
  const content = 'BT /F1 18 Tf 72 720 Td (Selectable PDF text) Tj ET';
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R /Outlines 6 0 R /PageMode /UseOutlines >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
    `<< /Length ${Buffer.byteLength(content, 'ascii')} >>\nstream\n${content}\nendstream`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
    '<< /Type /Outlines /First 7 0 R /Last 10 0 R /Count 4 >>',
    '<< /Title (Chapter 1) /Parent 6 0 R /First 8 0 R /Last 8 0 R /Count 2 /Next 10 0 R /Dest [3 0 R /Fit] >>',
    '<< /Title (Section 1.1) /Parent 7 0 R /First 9 0 R /Last 9 0 R /Count 1 /Dest [3 0 R /Fit] >>',
    '<< /Title (Detail 1.1.1) /Parent 8 0 R /Dest [3 0 R /Fit] >>',
    '<< /Title (Chapter 2) /Parent 6 0 R /Prev 7 0 R /Dest [3 0 R /Fit] >>',
  ];
  let source = '%PDF-1.4\n';
  const offsets = [0];
  for (const [index, object] of objects.entries()) {
    offsets.push(Buffer.byteLength(source, 'ascii'));
    source += `${index + 1} 0 obj\n${object}\nendobj\n`;
  }
  const xrefOffset = Buffer.byteLength(source, 'ascii');
  source += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  for (const offset of offsets.slice(1)) source += `${String(offset).padStart(10, '0')} 00000 n \n`;
  source += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xrefOffset}\n%%EOF\n`;
  return Array.from(Buffer.from(source, 'ascii'));
}

test('window opens with the app title', async () => {
  expect(await page.title()).toContain('TermiPod');
});

test('the preload bridge is injected and a native command round-trips', async () => {
  const hasBridge = await page.evaluate(() => typeof window.__ELECTRON_BRIDGE__ !== 'undefined');
  expect(hasBridge).toBe(true);
  // `app_version` is a native command — the round-trip through the bridge is what
  // this asserts. The VALUE is `app.getVersion()`: the CalVer only in a packaged
  // build; unpackaged (as here) it's Electron's own version. Either is semver-shaped.
  const version = await page.evaluate(() => window.__ELECTRON_BRIDGE__!.invoke<string>('app_version'));
  expect(version).toMatch(/^\d+\.\d+\.\d+/);
});

test('renderer is a secure context — crypto.randomUUID works (§7 row 12)', async () => {
  expect(await page.evaluate(() => window.isSecureContext)).toBe(true);
  const uuid = await page.evaluate(() => crypto.randomUUID());
  expect(uuid).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
});

test('the app shell renders (no blank screen)', async () => {
  const root = page.locator('#root');
  await expect(root).toBeAttached();
  // The React tree mounted at least one node — a blank/black-screen render
  // (the WebView2 failure class M4 pays down) would leave #root empty.
  await expect.poll(async () => root.locator('> *').count(), { timeout: 15_000 }).toBeGreaterThan(0);
});

test('linux: app menus share one title row and native controls reserve their edge', async () => {
  const os = await page.evaluate(() => window.__ELECTRON_BRIDGE__!.invoke<string>('platform_os'));
  const titlebar = page.locator('.linux-titlebar');

  if (os !== 'linux') {
    await expect(titlebar).toHaveCount(0);
    return;
  }

  await expect(titlebar).toBeVisible();
  await expect(titlebar.locator('.linux-titlebar-icon svg')).toHaveCount(1);
  await expect(titlebar.locator('.linux-titlebar-icon .job-icon')).toHaveCount(0);
  await expect(titlebar.locator('.linux-titlebar-menu-item')).toHaveText([
    'File', 'Edit', 'View', 'Window',
  ]);

  const metrics = await page.evaluate(() => {
    const bar = document.querySelector<HTMLElement>('.linux-titlebar')!;
    const inner = document.querySelector<HTMLElement>('.linux-titlebar-inner')!;
    const workbench = document.querySelector<HTMLElement>('.workbench-row')!;
    const barRect = bar.getBoundingClientRect();
    const innerRect = inner.getBoundingClientRect();
    const workbenchRect = workbench.getBoundingClientRect();
    return {
      top: barRect.top,
      height: barRect.height,
      bottom: barRect.bottom,
      workbenchTop: workbenchRect.top,
      reservedControlWidth: window.innerWidth - innerRect.right,
    };
  });
  expect(metrics.top).toBe(0);
  expect(metrics.height).toBe(32);
  expect(metrics.workbenchTop).toBeGreaterThanOrEqual(metrics.bottom);
  expect(metrics.reservedControlWidth).toBeGreaterThan(0);

  await page.evaluate(() =>
    window.__ELECTRON_BRIDGE__!.invoke('menu_show_application', { section: 'invalid' }),
  );

});

test('appearance font and size update live and persist across reload', async () => {
  await dismissConnectModal();
  const original = await page.evaluate(() => ({
    font: localStorage.getItem('termipod.uiFont'),
    scale: localStorage.getItem('termipod.uiFontScale'),
    category: localStorage.getItem('termipod.settings.cat'),
  }));

  try {
    await page.locator('[data-job="settings"]').click();
    await page.locator('.settings-cat').nth(1).click();

    await page.locator('#settings-ui-font').selectOption('mono');
    await page.locator('#settings-ui-font-size').fill('120');
    await expect.poll(() => page.evaluate(() => ({
      font: document.documentElement.dataset.uiFont,
      scale: document.documentElement.dataset.uiFontScale,
      sans: document.documentElement.style.getPropertyValue('--sans'),
      body: document.documentElement.style.getPropertyValue('--font-size-body'),
    }))).toEqual({
      font: 'mono',
      scale: '120',
      sans: '"JetBrains Mono Variable", ui-monospace, "SF Mono", "Cascadia Code", Menlo, monospace',
      body: '16.80px',
    });

    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect.poll(() => page.evaluate(() => ({
      font: document.documentElement.dataset.uiFont,
      scale: document.documentElement.dataset.uiFontScale,
    }))).toEqual({ font: 'mono', scale: '120' });
  } finally {
    await page.evaluate(({ font, scale, category }) => {
      const restore = (key: string, value: string | null): void => {
        if (value === null) localStorage.removeItem(key);
        else localStorage.setItem(key, value);
      };
      restore('termipod.uiFont', font);
      restore('termipod.uiFontScale', scale);
      restore('termipod.settings.cat', category);
    }, original);
    await page.reload({ waitUntil: 'domcontentloaded' });
  }
});

// ── Terminal flow ──────────────────────────────────────────────────────────
// A real local shell over node-pty. This is the layer the M4 base64→bytes IPC
// paydown (§7 row 4 / §6 row 6) will change, so pin the whole PTY round-trip:
// open → start → write → the `pty-data` byte stream carries the command output.
// (Requires node-pty rebuilt for the Electron ABI — the CI job does that with
// plain node-gyp; see desktop.yml.)
test('terminal: a local PTY round-trips through the bridge', async () => {
  const output = await page.evaluate(async () => {
    const b = window.__ELECTRON_BRIDGE__!;
    const { id } = await b.invoke<{ id: string; shell: string }>('pty_open', { req: { cols: 80, rows: 24 } });
    let text = '';
    // `pty-data` carries raw bytes (a Buffer → Uint8Array over structured clone),
    // NOT base64 — PTY is already bytes end-to-end.
    const un = await b.listen<{ id: string; bytes: ArrayLike<number> }>('pty-data', (e) => {
      if (e.payload.id === id) text += new TextDecoder().decode(new Uint8Array(e.payload.bytes));
    });
    await b.invoke('pty_start', { id }); // flushes buffered output; gates the reader
    await b.invoke('pty_write', { id, data: 'echo E2E_PTY_OK_MARKER\n' });
    await new Promise((r) => setTimeout(r, 3000));
    un();
    await b.invoke('pty_close', { id });
    return text;
  });
  expect(output).toContain('E2E_PTY_OK_MARKER');
});

test('terminal UI: saved hosts expose quick connect without crowding session chrome', async () => {
  const original = await page.evaluate(() => localStorage.getItem('connections'));
  try {
    await page.evaluate(() => {
      localStorage.setItem('connections', JSON.stringify([{
        id: 'e2e-quick-connect',
        name: 'E2E host',
        host: '127.0.0.1',
        port: 22,
        username: 'tester',
        authMethod: 'password',
        keyId: null,
        tmuxPath: null,
        group: 'default',
        createdAt: '2026-08-15T00:00:00.000Z',
        lastConnectedAt: null,
        deepLinkId: null,
      }]));
    });
    await page.reload({ waitUntil: 'domcontentloaded' });
    await dismissConnectModal();
    await page.locator('[data-job="terminal"]').click();

    const row = page.locator('.term-nav-conn', { hasText: 'E2E host' });
    await expect(row).toBeVisible();
    await expect(row.locator('.term-nav-quick')).toBeVisible();
    await expect(row.locator('.term-nav-quick')).toHaveAccessibleName('Connect: E2E host');
    await expect(page.locator('.term-surface-actions .term-nav-new')).toHaveCount(0);
    await expect(page.locator('.term-surface-actions .term-nav-import')).toHaveCount(0);

    // Management actions remain discoverable from the connection-list menu;
    // only their competing header placement was removed.
    await page.locator('.term-nav-list').dispatchEvent('contextmenu', { button: 2, clientX: 100, clientY: 200 });
    await expect(page.locator('.term-nav-ctxmenu')).toBeVisible();
    await expect(page.locator('.term-nav-ctxmenu')).toContainText('New connection');
  } finally {
    await page.evaluate((value) => {
      if (value === null) localStorage.removeItem('connections');
      else localStorage.setItem('connections', value);
    }, original);
    await page.reload({ waitUntil: 'domcontentloaded' });
    await dismissConnectModal();
  }
});

test('terminal UI: opening a local shell mounts an xterm screen', async () => {
  // On boot with no hub configured the "Add a hub" connect modal is open; its
  // backdrop intercepts clicks, so dismiss it before touching anything else
  // (its close button lives in the modal head). Conditional — it isn't always
  // present. This now has to happen FIRST: the old version reached the surface
  // with a keyboard shortcut, which the backdrop does not block.
  const connectClose = page.locator('.connect-head button');
  if (await connectClose.isVisible().catch(() => false)) {
    await connectClose.click();
    await expect(page.locator('.connect')).toHaveCount(0);
  }
  // Then click the Terminal rail tab BY IDENTITY. Not the Ctrl+<n> shortcut:
  // that is positional, so every job added to the activity bar renumbers it and
  // silently lands this test on a different surface. Adding J8 Replay between
  // Compare and Record moved Terminal from 8 to 9, and the breakage surfaced as
  // a 30s click timeout on `.term-add-btn` — a failure that names neither the
  // rail nor the shortcut.
  await page.locator('[data-job="terminal"]').click();
  await page.locator('.term-add-btn').first().click(); // the "+" new-session menu
  await page.locator('.term-add-menu button').first().click(); // "Local shell"
  // xterm mounted its screen — proves the UI PTY path renders without crashing.
  await expect(page.locator('.xterm').first()).toBeVisible({ timeout: 15_000 });
  const os = await page.evaluate(() => window.__ELECTRON_BRIDGE__!.invoke<string>('platform_os'));
  if (os === 'macos') {
    // macOS deliberately keeps xterm's DOM renderer for sharper Retina text.
    await expect(page.locator('.xterm .xterm-rows').first()).toBeVisible();
    const edge = await page.locator('.term-screen').first().evaluate(async (host) => {
      await document.fonts.ready;
      const root = host.querySelector<HTMLElement>(':scope > .xterm')!;
      const screen = root.querySelector<HTMLElement>('.xterm-screen')!;
      const row = root.querySelector<HTMLElement>('.xterm-rows > div')!;
      return {
        hostRight: host.getBoundingClientRect().right,
        screenRight: screen.getBoundingClientRect().right,
        reservedRight: Number.parseFloat(getComputedStyle(root).paddingRight),
        rowClipRight: row.getBoundingClientRect().right,
        rowClipPadding: Number.parseFloat(getComputedStyle(row).paddingRight),
      };
    });
    // The explicit reserve is part of FitAddon's width calculation, keeping the
    // final DOM cell (notably a right-aligned `%`) inside the clipped host.
    expect(edge.reservedRight).toBeGreaterThanOrEqual(8);
    expect(edge.screenRight).toBeLessThan(edge.hostRight);
    expect(edge.rowClipPadding).toBeGreaterThanOrEqual(8);
    expect(edge.rowClipRight).toBeGreaterThan(edge.screenRight);

    // Paint a tmux-style status line through the real PTY with its final digit
    // in the final terminal column. Maple's fractional DOM advances put that
    // digit beyond xterm's logical row box; it must remain visible in the outer
    // gutter while vertical row overflow stays clipped.
    await page.waitForTimeout(500); // let the debounced PTY resize settle
    await page.keyboard.type(
      "printf '\\033[2J\\033[H\\033[48;2;166;227;161m%*s\\033[0m' \"$(tput cols)\" '14-Aug-26'; sleep 3",
    );
    await page.keyboard.press('Enter');
    await page.waitForTimeout(300);
    const painted = await page.locator('.term-screen').first().evaluate((host) => {
      const row = [...host.querySelectorAll<HTMLElement>('.xterm-rows > div')]
        .find((candidate) => candidate.textContent?.endsWith('14-Aug-26'))!;
      const ink = [...row.querySelectorAll<HTMLElement>('span')]
        .filter((span) => span.textContent?.trim() !== '')
        .at(-1)!;
      const style = getComputedStyle(row);
      return {
        hostRight: host.getBoundingClientRect().right,
        rowRight: row.getBoundingClientRect().right,
        inkRight: ink.getBoundingClientRect().right,
        overflowX: style.overflowX,
        overflowY: style.overflowY,
      };
    });
    expect(painted.inkRight).toBeGreaterThan(painted.rowRight);
    expect(painted.inkRight).toBeLessThan(painted.hostRight);
    expect(painted.overflowX).toBe('visible');
    expect(painted.overflowY).toBe('clip');
  }
});

// ── draw.io embed ──────────────────────────────────────────────────────────
// The offline draw.io webapp (~50 MB) is not bundled, so in CI it is not
// installed — a full iframe-embed test would need the download + a diagram doc.
// Pin the command family the surface drives: `drawio_status` round-trips and
// reports the not-installed state (which is what DiagramEditor renders its
// download CTA from).
test('draw.io: drawio_status round-trips (not installed in CI)', async () => {
  const status = await page.evaluate(() =>
    window.__ELECTRON_BRIDGE__!.invoke<{ installed: boolean; version: string }>('drawio_status'),
  );
  expect(status.installed).toBe(false);
  expect(typeof status.version).toBe('string');
});

// ── Figure export ──────────────────────────────────────────────────────────
// PNG export (`save_image_as`) rasterizes the rendered figure SVG to a canvas in
// the renderer, then hands the bytes to a native save (a dialog — not headlessly
// drivable). Pin the Chromium-behaviour half the export depends on: SVG → <img>
// → canvas → PNG. This is exactly the rasterization WebKit mishandled (the row-3
// sizedSvg concern); Chromium does it cleanly.
test('figure export: Chromium rasterizes an SVG to a PNG via canvas', async () => {
  const png = await page.evaluate(async () => {
    const svg =
      '<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64">' +
      '<rect width="64" height="64" fill="#123456"/><text x="6" y="36" fill="#fff">E2E</text></svg>';
    const img = new Image();
    await new Promise<void>((res, rej) => {
      img.onload = () => res();
      img.onerror = () => rej(new Error('svg image load failed'));
      img.src = 'data:image/svg+xml;base64,' + btoa(svg);
    });
    const canvas = document.createElement('canvas');
    canvas.width = 64;
    canvas.height = 64;
    canvas.getContext('2d')!.drawImage(img, 0, 0);
    return canvas.toDataURL('image/png'); // throws if the canvas tainted; blank if raster failed
  });
  expect(png.startsWith('data:image/png;base64,')).toBe(true);
  expect(png.length).toBeGreaterThan(200); // real pixels, not a blank 1×1
});

// ── blob-URL iframe (§6 row 2 / §7 row 2 guard-deletion) ─────────────────────
// The reader's HTML attachment viewer (`ReadSurface` → `HtmlDoc`) loads a
// same-origin `blob:` URL in an iframe and drives zoom through its
// `contentDocument`. WebView2 REFUSED `<iframe src=blob:>` outright ("此页面已被
// Microsoft Edge 阻止") — the reason the codebase carried WebView2 avoidance
// comments. Chromium loads it and keeps it same-origin scriptable; this test pins
// that so those comments can be removed and the behaviour can't silently regress.
test('blob-URL iframe loads and stays same-origin scriptable (the pattern WebView2 refused)', async () => {
  const result = await page.evaluate(async () => {
    const html = '<!doctype html><html><body><p id="marker">BLOB_IFRAME_OK</p></body></html>';
    const url = URL.createObjectURL(new Blob([html], { type: 'text/html' }));
    const iframe = document.createElement('iframe');
    document.body.appendChild(iframe);
    await new Promise<void>((res, rej) => {
      iframe.onload = () => res();
      iframe.onerror = () => rej(new Error('blob iframe failed to load'));
      iframe.src = url;
    });
    // Same-origin read (HtmlDoc reads the marker's document) …
    const text = iframe.contentDocument?.getElementById('marker')?.textContent ?? '';
    // … and same-origin WRITE (HtmlDoc applies zoom via documentElement.style.zoom).
    let zoomable = false;
    try {
      (iframe.contentDocument!.documentElement.style as CSSStyleDeclaration & { zoom: string }).zoom = '1.5';
      zoomable = true;
    } catch {
      /* cross-origin — the WebView2 failure mode */
    }
    iframe.remove();
    URL.revokeObjectURL(url);
    return { text, zoomable };
  });
  expect(result.text).toBe('BLOB_IFRAME_OK');
  expect(result.zoomable).toBe(true);
});

// ── sizedSvg WebKit shim (§6 row 3 / §7 row 3 guard-deletion) ────────────────
// mermaid/vega emit a `viewBox` but often no explicit width/height (just a CSS
// max-width). WebKit reported `naturalWidth === 0` for such an SVG and drew a
// blank PNG, so `FigureEditor.sizedSvg` injected explicit dimensions before
// rasterizing. This mirrors the SIMPLIFIED path (no injection): load the
// viewBox-only SVG and `drawImage(img, 0, 0, w, h)` with explicit dest dims.
// Passing proves the injection is unnecessary on Chromium, so it can be deleted.
test('sizedSvg: Chromium rasterizes a viewBox-only SVG (the WebKit naturalWidth=0 case)', async () => {
  const out = await page.evaluate(async () => {
    // No width/height attrs — only a viewBox + a CSS max-width, exactly what
    // mermaid/vega emit and what WebKit blanked.
    const svg =
      '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 80" style="max-width:100%">' +
      '<rect width="120" height="80" fill="#22aa66"/></svg>';
    const w = 120;
    const h = 80;
    const img = new Image();
    await new Promise<void>((res, rej) => {
      img.onload = () => res();
      img.onerror = () => rej(new Error('svg decode failed'));
      img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg);
    });
    const canvas = document.createElement('canvas');
    canvas.width = w;
    canvas.height = h;
    const ctx = canvas.getContext('2d')!;
    ctx.drawImage(img, 0, 0, w, h); // explicit dest dims — no injected width/height needed
    const px = ctx.getImageData(10, 10, 1, 1).data; // should be the green rect
    return { alpha: px[3], green: px[1] };
  });
  // A blank PNG (the WebKit failure) would be fully transparent — assert real pixels.
  expect(out.alpha).toBeGreaterThan(0);
  expect(out.green).toBeGreaterThan(100);
});

// ── bytes-over-IPC (§7 row 4 — base64→bytes) ────────────────────────────────
// The file-bytes channels (storage/attachment, localfs, sftp) and voice now pass
// raw bytes over IPC instead of base64. This pins the round-trip on the one path
// reachable without a server/dialog — attachment write→read — in BOTH directions
// (renderer→main write, main→renderer read). The high bytes (253–255) would be
// mangled by any stray text/base64 (mis)handling; exact equality proves binary
// transfer. SFTP/localfs/voice use the identical structured-clone mechanism.
test('bytes over IPC: attachment write→read round-trips raw bytes (no base64)', async () => {
  const rt = await page.evaluate(async () => {
    const b = window.__ELECTRON_BRIDGE__!;
    const root = await b.invoke<string>('attachment_default_dir');
    const original = [0, 1, 2, 66, 121, 116, 101, 115, 253, 254, 255]; // incl. high bytes
    const added = await b.invoke<{ key: string; file: string; path: string }>('attachment_write_bytes', {
      root,
      filename: 'e2e-bytes-roundtrip.bin',
      bytes: new Uint8Array(original),
    });
    const f = await b.invoke<{ bytes: ArrayLike<number>; mime: string }>('attachment_read', { path: added.path });
    const readback = Array.from(new Uint8Array(f.bytes));
    await b.invoke('attachment_delete', { path: added.path }); // clean up
    return { original, readback };
  });
  expect(rt.readback).toEqual(rt.original);
});

test('read: synced Zotero files open from the default attachment location', async () => {
  await dismissConnectModal();
  const fixture = await page.evaluate(async () => {
    const b = window.__ELECTRON_BRIDGE__!;
    const root = await b.invoke<string>('attachment_default_dir');
    const added = await b.invoke<{ key: string; file: string; path: string }>('attachment_write_bytes', {
      root,
      filename: 'e2e-default-storage.txt',
      bytes: new TextEncoder().encode('opened from the default storage root'),
    });
    const libraryKey = 'termipod.library.v1';
    const linkKey = 'termipod.zotero.storagePath';
    const originalLibrary = localStorage.getItem(libraryKey);
    const originalLink = localStorage.getItem(linkKey);
    localStorage.removeItem(linkKey);
    localStorage.setItem(libraryKey, JSON.stringify({
      references: [{
        id: 'ref-e2e-default-storage',
        type: 'article',
        title: 'E2E default storage attachment',
        authors: ['TermiPod'],
        tags: [],
        collectionIds: [],
        notes: '',
        source: 'zotero',
        addedAt: Date.now(),
        dirty: false,
        attachments: [{
          id: 'att-e2e-default-storage',
          file: added.file,
          contentType: 'text/plain',
          source: 'zotero',
          key: added.key,
          addedAt: Date.now(),
        }],
      }],
      collections: [],
    }));
    return { path: added.path, originalLibrary, originalLink };
  });

  try {
    await page.reload({ waitUntil: 'domcontentloaded' });
    await dismissConnectModal();
    await page.locator('[data-job="read"]').click();

    const row = page.locator('.read-table tbody tr').filter({ hasText: 'E2E default storage attachment' });
    await expect(row).toBeVisible();
    await expect(page.locator('.read-import-msg.attn')).toHaveCount(0);
    await row.dblclick();
    await expect(page.locator('.att-text')).toContainText('opened from the default storage root');
  } finally {
    await page.evaluate(async ({ path, originalLibrary, originalLink }) => {
      const libraryKey = 'termipod.library.v1';
      const linkKey = 'termipod.zotero.storagePath';
      if (originalLibrary === null) localStorage.removeItem(libraryKey);
      else localStorage.setItem(libraryKey, originalLibrary);
      if (originalLink === null) localStorage.removeItem(linkKey);
      else localStorage.setItem(linkKey, originalLink);
      await window.__ELECTRON_BRIDGE__!.invoke('attachment_delete', { path });
    }, fixture);
    await page.reload({ waitUntil: 'domcontentloaded' });
  }
});

test('read: ratings persist, update in one click, and sort highest first', async () => {
  await dismissConnectModal();
  const originalLibrary = await page.evaluate(() => {
    const libraryKey = 'termipod.library.v1';
    const original = localStorage.getItem(libraryKey);
    const base = {
      type: 'article',
      authors: ['TermiPod'],
      tags: [],
      collectionIds: [],
      notes: '',
      addedAt: Date.now(),
      dirty: false,
      attachments: [],
    };
    localStorage.setItem(libraryKey, JSON.stringify({
      references: [
        { ...base, id: 'ref-rating-two', title: 'Rating Alpha', rating: 2 },
        { ...base, id: 'ref-rating-five', title: 'Rating Beta', rating: 5 },
        { ...base, id: 'ref-rating-none', title: 'Rating Gamma' },
      ],
      collections: [],
    }));
    return original;
  });

  try {
    await page.reload({ waitUntil: 'domcontentloaded' });
    await dismissConnectModal();
    await page.locator('[data-job="read"]').click();

    const table = page.locator('.read-table');
    const scrollMetrics = await page.locator('.read-table-wrap').evaluate((wrapper) => {
      const candidates = [wrapper, ...wrapper.querySelectorAll<HTMLElement>('div')];
      const scroller = candidates.find((candidate) => {
        const overflowX = getComputedStyle(candidate).overflowX;
        return candidate.scrollWidth > candidate.clientWidth + 1 && (overflowX === 'auto' || overflowX === 'scroll');
      });
      if (scroller === undefined) return null;
      scroller.scrollLeft = 120;
      return { clientWidth: scroller.clientWidth, scrollWidth: scroller.scrollWidth, scrollLeft: scroller.scrollLeft };
    });
    expect(scrollMetrics).not.toBeNull();
    expect(scrollMetrics!.scrollWidth).toBeGreaterThan(scrollMetrics!.clientWidth);
    expect(scrollMetrics!.scrollLeft).toBeGreaterThan(0);

    const rows = table.locator('tbody tr');
    await table.getByRole('button', { name: 'Sort by Rating', exact: true }).click();
    await expect(rows.nth(0)).toContainText('Rating Beta');
    await expect(rows.nth(1)).toContainText('Rating Alpha');
    await expect(rows.nth(2)).toContainText('Rating Gamma');

    const unrated = rows.filter({ hasText: 'Rating Gamma' });
    await unrated.getByRole('button', { name: 'Rate 4 out of 5', exact: true }).click();
    await expect(rows.nth(0)).toContainText('Rating Beta');
    await expect(rows.nth(1)).toContainText('Rating Gamma');
    await expect(rows.nth(2)).toContainText('Rating Alpha');
    await expect(rows.nth(1).getByRole('button', { name: 'Clear 4-star rating', exact: true })).toBeVisible();

    const persisted = await page.evaluate(() => {
      const library = JSON.parse(localStorage.getItem('termipod.library.v1') ?? '{}') as {
        references?: { id: string; rating?: number }[];
      };
      return library.references?.find((reference) => reference.id === 'ref-rating-none')?.rating;
    });
    expect(persisted).toBe(4);

    await rows.filter({ hasText: 'Rating Gamma' }).click();
    const metadataRow = page.locator('.ref-rating-type-row');
    await expect(metadataRow).toBeVisible();
    const metadataMetrics = await metadataRow.evaluate((node) => {
      const rating = node.querySelector<HTMLElement>('.ref-rating-field')!.getBoundingClientRect();
      const type = node.querySelector<HTMLElement>('.ref-type-field')!.getBoundingClientRect();
      return {
        ratingTop: Math.round(rating.top),
        ratingRight: Math.round(rating.right),
        typeTop: Math.round(type.top),
        typeLeft: Math.round(type.left),
      };
    });
    expect(metadataMetrics.typeTop).toBe(metadataMetrics.ratingTop);
    expect(metadataMetrics.typeLeft).toBeGreaterThan(metadataMetrics.ratingRight);
  } finally {
    await page.evaluate((original) => {
      if (original === null) localStorage.removeItem('termipod.library.v1');
      else localStorage.setItem('termipod.library.v1', original);
    }, originalLibrary);
    await page.reload({ waitUntil: 'domcontentloaded' });
  }
});

test('read: Cite keeps Scholar and OpenAlex provenance separate', async () => {
  await dismissConnectModal();
  const originalLibrary = await page.evaluate(() => {
    const libraryKey = 'termipod.library.v1';
    const original = localStorage.getItem(libraryKey);
    localStorage.setItem(libraryKey, JSON.stringify({
      references: [{
        id: 'ref-citation-provenance',
        type: 'article',
        title: 'Citation provenance fixture',
        authors: ['TermiPod'],
        year: 2025,
        citationCount: 120,
        citedByCount: 95,
        referenceCount: 14,
        source: 'google-scholar',
        externalId: 'scholar-fixture',
        openAlexId: 'https://openalex.org/W123',
        scholar: {
          resultId: 'scholar-fixture',
          citedByCount: 120,
          citesId: 'fixture-cites',
          citedByUrl: 'https://scholar.google.com/scholar?cites=fixture-cites',
          versionsCount: 3,
          versionsUrl: 'https://scholar.google.com/scholar?cluster=fixture',
          citations: [{ id: 'scholar-citing-1', title: 'Scholar citing work', year: 2026, url: 'https://example.test/scholar' }],
          citationsPerYear: [{ year: 2025, citations: 20 }, { year: 2026, citations: 40 }],
          citationTotalResults: 120,
          citationsLoadedAt: Date.now(),
          citationsHasMore: true,
        },
        citations: [{ id: 'https://openalex.org/W456', title: 'OpenAlex citing work', year: 2026 }],
        tags: [],
        collectionIds: [],
        notes: '',
        addedAt: Date.now(),
        dirty: false,
        attachments: [],
      }],
      collections: [],
    }));
    return original;
  });

  try {
    await page.reload({ waitUntil: 'domcontentloaded' });
    await dismissConnectModal();
    await page.locator('[data-job="read"]').click();
    await page.locator('.read-table tbody tr').filter({ hasText: 'Citation provenance fixture' }).click();
    await page.getByRole('tab', { name: 'Cite', exact: true }).click();

    const cite = page.locator('.ref-cite');
    await expect(cite.locator('.ref-metric').filter({ hasText: 'Google Scholar' })).toContainText('120');
    await expect(cite.locator('.ref-metric').filter({ hasText: 'OpenAlex' })).toContainText('95');
    await expect(cite.locator('.ref-provider-note')).toContainText('different sources');
    await expect(cite.getByText('Scholar citing work', { exact: true })).toBeVisible();
    await expect(cite.getByText('OpenAlex citing work', { exact: true })).toBeVisible();
    await expect(cite.getByRole('button', { name: 'Load more', exact: true })).toBeVisible();
    await expect(cite.locator('.ref-scholar-year-bar')).toHaveCount(2);
  } finally {
    await page.evaluate((original) => {
      if (original === null) localStorage.removeItem('termipod.library.v1');
      else localStorage.setItem('termipod.library.v1', original);
    }, originalLibrary);
    await page.reload({ waitUntil: 'domcontentloaded' });
  }
});

test('read: Discovery uses contextual saved and recent search navigation', async () => {
  await dismissConnectModal();
  const originals = await page.evaluate(() => {
    const libraryKey = 'termipod.library.v1';
    const historyKey = 'termipod.discover.history.v1';
    const originalLibrary = localStorage.getItem(libraryKey);
    const originalHistory = localStorage.getItem(historyKey);
    localStorage.setItem(libraryKey, JSON.stringify({
      references: [{
        id: 'ref-discovery-rail',
        type: 'article',
        title: 'Library rail fixture',
        authors: ['TermiPod'],
        tags: ['library-only-tag'],
        collectionIds: ['collection-only'],
        notes: '',
        addedAt: Date.now(),
        dirty: false,
        attachments: [],
      }],
      collections: [{ id: 'collection-only', name: 'Library-only collection' }],
    }));
    localStorage.setItem(historyKey, JSON.stringify({
      version: 1,
      saved: [{
        id: 'saved-e2e',
        name: 'Saved core query',
        query: 'saved discovery query',
        sourceId: 'core',
        authorFilter: 'Researcher',
        yearFrom: '2024',
        yearTo: '',
        sort: 'newest',
        findPdfs: false,
        savedAt: Date.now() - 2000,
      }],
      recent: [{
        id: 'recent-e2e',
        query: 'recent discovery query',
        sourceId: 'openalex',
        authorFilter: '',
        yearFrom: '',
        yearTo: '',
        sort: 'relevance',
        findPdfs: true,
        ranAt: Date.now() - 1000,
        resultCount: 17,
      }],
    }));
    return { originalLibrary, originalHistory };
  });

  try {
    await page.reload({ waitUntil: 'domcontentloaded' });
    await dismissConnectModal();
    await page.locator('[data-job="read"]').click();
    await page.getByRole('button', { name: 'Discover', exact: true }).click();

    const rail = page.locator('.discover-nav');
    await expect(rail).toBeVisible();
    await expect(rail.getByText('Saved core query', { exact: true })).toBeVisible();
    await expect(rail.getByText('recent discovery query', { exact: true })).toBeVisible();
    await expect(rail.getByText('Recent searches', { exact: true })).toBeVisible();
    await expect(page.getByText('Library-only collection', { exact: true })).toHaveCount(0);
    await expect(page.getByText('library-only-tag', { exact: true })).toHaveCount(0);

    await rail.locator('.discover-nav-row').filter({ hasText: 'Saved core query' }).locator('.discover-nav-main').click();
    await expect(page.locator('.discover-input')).toHaveValue('saved discovery query');
    await expect(page.locator('.discover-src.active')).toHaveText('CORE');
    await expect(page.getByRole('button', { name: 'Remove saved search', exact: true })).toBeVisible();

    await page.locator('.discover-input').fill('a newly saved query');
    await page.locator('.discover-save-search').click();
    await expect(rail.getByText('a newly saved query', { exact: true })).toBeVisible();
    await expect(page.locator('.discover-save-search')).toHaveAttribute(
      'aria-label',
      'Remove saved search',
    );
    await page.locator('.discover-save-search').click();
    await expect(rail.getByText('a newly saved query', { exact: true })).toHaveCount(0);

    await rail.getByRole('button', { name: 'Clear', exact: true }).click();
    await expect(rail.getByText('recent discovery query', { exact: true })).toHaveCount(0);
    const persisted = await page.evaluate(() => JSON.parse(localStorage.getItem('termipod.discover.history.v1') ?? '{}'));
    expect(persisted.recent).toEqual([]);
    expect(persisted.saved).toHaveLength(1);
  } finally {
    await page.evaluate(({ originalLibrary, originalHistory }) => {
      if (originalLibrary === null) localStorage.removeItem('termipod.library.v1');
      else localStorage.setItem('termipod.library.v1', originalLibrary);
      if (originalHistory === null) localStorage.removeItem('termipod.discover.history.v1');
      else localStorage.setItem('termipod.discover.history.v1', originalHistory);
    }, originals);
    await page.reload({ waitUntil: 'domcontentloaded' });
  }
});

test('read: Discovery monitoring exposes updates, subscriptions, schedules, and collection recommendations', async () => {
  await dismissConnectModal();
  const originals = await page.evaluate(() => {
    const keys = ['termipod.library.v1', 'termipod.discover.history.v1', 'termipod.discover.monitor.v1'];
    const values = Object.fromEntries(keys.map((key) => [key, localStorage.getItem(key)]));
    localStorage.setItem('termipod.library.v1', JSON.stringify({
      collections: [{ id: 'collection-monitor', name: 'Graph research' }],
      references: [{
        id: 'reference-monitor', type: 'article', title: 'Graph learning for molecules', authors: ['Ada Researcher'],
        venue: 'Graph Journal', rating: 5, topics: ['Graph learning'], tags: ['molecules'],
        collectionIds: ['collection-monitor'], notes: '', addedAt: Date.now(), dirty: false, attachments: [],
      }],
    }));
    localStorage.setItem('termipod.discover.history.v1', JSON.stringify({
      version: 1,
      recent: [],
      saved: [{
        id: 'saved-monitor', name: 'Saved graph query', query: 'graph learning', sourceId: 'openalex',
        authorFilter: '', yearFrom: '', yearTo: '', sort: 'newest', findPdfs: false, savedAt: Date.now(),
      }],
    }));
    localStorage.setItem('termipod.discover.monitor.v1', JSON.stringify({
      version: 1,
      subscriptions: [{
        id: 'subscription-monitor', kind: 'topic', label: 'Graph learning', value: 'graph learning',
        sourceId: 'openalex', cadence: 'weekly', createdAt: Date.now(),
      }],
      updates: [{
        id: 'update-monitor', originType: 'subscription', originId: 'subscription-monitor',
        originLabel: 'Graph learning', arrivedAt: Date.now(),
        paper: { paperId: 'paper-monitor', title: 'A new graph paper', authors: ['A. Author'], year: 2026, venue: 'Graph Journal' },
      }],
      runs: {
        'subscription:subscription-monitor': { lastRunAt: Date.now(), seen: ['id:paper-monitor'] },
      },
      lastRefreshAt: Date.now(),
    }));
    return values;
  });

  try {
    await page.reload({ waitUntil: 'domcontentloaded' });
    await dismissConnectModal();
    await page.locator('[data-job="read"]').click();
    await page.getByRole('button', { name: 'Discover', exact: true }).click();

    const rail = page.locator('.discover-nav');
    await expect(rail.getByRole('button', { name: /Updates/ })).toContainText('1');
    await rail.getByRole('button', { name: /Updates/ }).click();
    await expect(page.getByRole('heading', { name: 'Updates' })).toBeVisible();
    await expect(page.getByText('A new graph paper', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Mark read', exact: true }).click();
    await expect(rail.locator('.discover-nav-count')).toHaveCount(0);

    await rail.getByRole('button', { name: 'Following', exact: true }).click();
    await expect(page.getByText('Graph learning', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Monitors', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Scheduled saved searches' })).toBeVisible();
    await expect(page.getByText('Saved graph query', { exact: true })).toBeVisible();

    await rail.getByRole('button', { name: 'For you', exact: true }).click();
    await page.getByLabel('Seed collection').selectOption('collection-monitor');
    await expect(page.getByText('Seeded by 1 collection items', { exact: true })).toBeVisible();
    await expect(page.locator('.discovery-recommend-explain')).toContainText('graph');
    await expect(page.locator('.discovery-recommend-explain')).toContainText('molecules');
  } finally {
    await page.evaluate((values) => {
      for (const [key, value] of Object.entries(values)) {
        if (value === null) localStorage.removeItem(key);
        else localStorage.setItem(key, value);
      }
    }, originals);
    await page.reload({ waitUntil: 'domcontentloaded' });
  }
});

test('read: PDF frequent actions stay visible and the outline folds by level', async () => {
  await dismissConnectModal();
  const fixture = await page.evaluate(async ({ bytes }) => {
    const b = window.__ELECTRON_BRIDGE__!;
    const root = await b.invoke<string>('attachment_default_dir');
    const added = await b.invoke<{ key: string; file: string; path: string }>('attachment_write_bytes', {
      root,
      filename: 'e2e-fit-width.pdf',
      bytes: new Uint8Array(bytes),
    });
    const libraryKey = 'termipod.library.v1';
    const linkKey = 'termipod.zotero.storagePath';
    const scaleKey = 'termipod.pdf.scale';
    const annotationsKey = 'termipod.annotations.v1';
    const originalLibrary = localStorage.getItem(libraryKey);
    const originalLink = localStorage.getItem(linkKey);
    const originalScale = localStorage.getItem(scaleKey);
    const originalAnnotations = localStorage.getItem(annotationsKey);
    localStorage.removeItem(linkKey);
    localStorage.setItem(scaleKey, '0.4');
    localStorage.setItem(libraryKey, JSON.stringify({
      references: [{
        id: 'ref-e2e-fit-width',
        type: 'article',
        title: 'E2E PDF fit width',
        authors: ['TermiPod'],
        tags: [],
        collectionIds: [],
        notes: '',
        source: 'zotero',
        addedAt: Date.now(),
        dirty: false,
        attachments: [{
          id: 'att-e2e-fit-width',
          file: added.file,
          contentType: 'application/pdf',
          source: 'zotero',
          key: added.key,
          addedAt: Date.now(),
        }],
      }],
      collections: [],
    }));
    return { path: added.path, originalLibrary, originalLink, originalScale, originalAnnotations };
  }, { bytes: onePagePdfBytes() });

  try {
    await page.reload({ waitUntil: 'domcontentloaded' });
    await dismissConnectModal();
    await page.locator('[data-job="read"]').click();
    const row = page.locator('.read-table tbody tr').filter({ hasText: 'E2E PDF fit width' });
    await expect(row).toBeVisible();
    await row.dblclick();

    const toolbar = page.locator('.pdfjs-toolbar');
    const fitWidth = toolbar.getByRole('button', { name: 'Fit width', exact: true });
    await expect(fitWidth).toBeVisible();
    await fitWidth.click();
    await expect.poll(() => page.evaluate(() => localStorage.getItem('termipod.pdf.scale'))).not.toBe('0.4');

    await toolbar.getByRole('button', { name: 'Contents', exact: true }).click();
    const toc = page.locator('.pdfjs-toc');
    await expect(toc.getByRole('tab', { name: 'Outline', exact: true })).toBeVisible();
    await expect(toc.getByRole('button', { name: 'Chapter 1', exact: true })).toBeVisible();
    await expect(toc.getByRole('button', { name: 'Chapter 2', exact: true })).toBeVisible();
    await expect(toc.getByRole('button', { name: 'Section 1.1', exact: true })).toHaveCount(0);
    await toc.getByRole('button', { name: 'Expand subheadings', exact: true }).first().click();
    await expect(toc.getByRole('button', { name: 'Section 1.1', exact: true })).toBeVisible();
    await expect(toc.getByRole('button', { name: 'Detail 1.1.1', exact: true })).toHaveCount(0);
    await toc.getByRole('button', { name: 'Collapse subheadings', exact: true }).first().click();
    await expect(toc.getByRole('button', { name: 'Section 1.1', exact: true })).toHaveCount(0);
    await toc.getByRole('button', { name: 'Show all heading levels', exact: true }).click();
    await expect(toc.getByRole('button', { name: 'Detail 1.1.1', exact: true })).toBeVisible();
    await toc.getByRole('button', { name: 'Show top-level headings only', exact: true }).click();
    await expect(toc.getByRole('button', { name: 'Section 1.1', exact: true })).toHaveCount(0);

    await toolbar.getByRole('button', { name: 'More PDF controls' }).click();
    await expect(page.getByRole('menuitem', { name: 'Fit width', exact: true })).toHaveCount(0);
    await expect(page.getByRole('menuitem', { name: 'Fit page', exact: true })).toBeVisible();
    await page.keyboard.press('Escape');

    const more = toolbar.locator('.pdfjs-overflow-trigger');
    const details = toolbar.locator('.pdfjs-details-toggle');
    await expect(details).toBeVisible();
    expect(await more.evaluate((node, detailNode) => (
      node.compareDocumentPosition(detailNode as Node) & Node.DOCUMENT_POSITION_FOLLOWING
    ) !== 0, await details.elementHandle())).toBe(true);

    const textSpan = page.locator('.textLayer span').filter({ hasText: 'Selectable PDF text' }).first();
    await expect(textSpan).toBeVisible();
    await textSpan.evaluate((node) => {
      const range = document.createRange();
      range.selectNodeContents(node);
      const selection = window.getSelection();
      selection?.removeAllRanges();
      selection?.addRange(range);
      node.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, view: window }));
    });
    const selectionActions = page.getByRole('toolbar', { name: 'Selection actions' });
    await expect(selectionActions).toBeVisible();
    await expect(selectionActions.getByRole('button', { name: 'Copy', exact: true })).toBeVisible();
    await expect(selectionActions.getByRole('button', { name: 'Highlight', exact: true })).toBeVisible();
    await expect(selectionActions.getByRole('button', { name: 'Add to notes', exact: true })).toBeVisible();

    await textSpan.evaluate((node) => {
      const rect = node.getBoundingClientRect();
      const init = { bubbles: true, button: 2, clientX: rect.left + 4, clientY: rect.top + 4 };
      node.dispatchEvent(new PointerEvent('pointerdown', init));
      node.dispatchEvent(new MouseEvent('contextmenu', { ...init, view: window }));
      node.dispatchEvent(new MouseEvent('mouseup', { ...init, view: window }));
    });
    const contextMenu = page.locator('.pdfjs-ctxmenu');
    await expect(contextMenu).toBeVisible();
    await expect(contextMenu.getByRole('button', { name: 'Copy', exact: true })).toBeVisible();
    await expect(contextMenu.getByRole('button', { name: 'Highlight', exact: true })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(contextMenu).toHaveCount(0);

    await textSpan.evaluate((node) => {
      node.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, button: 0, view: window }));
    });
    await expect(selectionActions).toBeVisible();
    await selectionActions.getByRole('button', { name: 'Highlight', exact: true }).click();
    await expect(selectionActions).toHaveCount(0);
    await expect(page.locator('.pdfjs-anno.highlight')).toBeVisible();

    const areaButton = toolbar.getByRole('button', { name: 'Area (A)', exact: true });
    if (await areaButton.count()) {
      await areaButton.click();
    } else {
      // Narrow toolbars move annotation tools into the responsive overflow menu.
      await toolbar.getByRole('button', { name: 'More PDF controls', exact: true }).click();
      await page.getByRole('menuitem', { name: 'Area', exact: true }).click();
    }
    const areaSurface = page.locator('.pdfjs-draw-surface.image');
    await expect(areaSurface).toBeVisible();
    await areaSurface.evaluate((node) => {
      const rect = node.getBoundingClientRect();
      const start = { clientX: rect.left + 40, clientY: rect.top + 40 };
      const end = { clientX: rect.left + 140, clientY: rect.top + 100 };
      node.dispatchEvent(new PointerEvent('pointerdown', { ...start, bubbles: true, button: 0, buttons: 1 }));
      window.dispatchEvent(new PointerEvent('pointermove', { ...end, bubbles: true, button: 0, buttons: 1 }));
      window.dispatchEvent(new PointerEvent('pointerup', { ...end, bubbles: true, button: 0 }));
    });
    const annoEditor = page.getByRole('dialog', { name: 'Annotation editor' });
    await expect(annoEditor).toBeVisible();
    const annoActions = annoEditor.getByRole('toolbar', { name: 'Annotation actions' });
    await expect(annoActions).toBeVisible();
    for (const name of ['Copy image', 'Save image as…', 'Add to note', 'Delete', 'Done']) {
      const action = annoActions.getByRole('button', { name, exact: true });
      await expect(action).toBeVisible();
      await expect(action).toHaveText('');
      await expect(action).toHaveAttribute('data-tooltip', name);
    }
    const actionCenters = await annoActions.getByRole('button').evaluateAll((buttons) =>
      buttons.map((button) => {
        const rect = button.getBoundingClientRect();
        return Math.round(rect.top + rect.height / 2);
      }),
    );
    expect(new Set(actionCenters).size).toBe(1);

    const saveImage = annoActions.getByRole('button', { name: 'Save image as…', exact: true });
    await saveImage.hover();
    await expect.poll(() =>
      saveImage.evaluate((button) => getComputedStyle(button, '::after').opacity),
    ).toBe('1');

    await annoActions.getByRole('button', { name: 'Delete', exact: true }).click();
    const deleteConfirm = page.getByRole('dialog', { name: 'Delete this annotation? This cannot be undone.' });
    await expect(deleteConfirm).toBeVisible();
    await expect(page.locator('.pdfjs-anno.image')).toBeVisible();
    await deleteConfirm.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(deleteConfirm).toHaveCount(0);
  } finally {
    await page.evaluate(async ({ path, originalLibrary, originalLink, originalScale, originalAnnotations }) => {
      const libraryKey = 'termipod.library.v1';
      const linkKey = 'termipod.zotero.storagePath';
      const scaleKey = 'termipod.pdf.scale';
      const annotationsKey = 'termipod.annotations.v1';
      if (originalLibrary === null) localStorage.removeItem(libraryKey);
      else localStorage.setItem(libraryKey, originalLibrary);
      if (originalLink === null) localStorage.removeItem(linkKey);
      else localStorage.setItem(linkKey, originalLink);
      if (originalScale === null) localStorage.removeItem(scaleKey);
      else localStorage.setItem(scaleKey, originalScale);
      if (originalAnnotations === null) localStorage.removeItem(annotationsKey);
      else localStorage.setItem(annotationsKey, originalAnnotations);
      await window.__ELECTRON_BRIDGE__!.invoke('attachment_delete', { path });
    }, fixture);
    await page.reload({ waitUntil: 'domcontentloaded' });
  }
});

// ── Excalidraw sketch editor (figure-plan Phase C) ───────────────────────────
// The interactive sketch surface is a heavy lazy chunk that mounts its own React
// tree and loads fonts. Pin that it lazy-loads and mounts under the packaged
// `app://` origin (the black-screen render class doesn't reproduce), AND that its
// font loader is pointed at the SELF-HOSTED assets — never the esm.sh CDN
// fallback — which is the offline-first contract (full airplane-mode is
// device-verified: fonts degrade gracefully to system fonts if absent).
test('excalidraw: the sketch editor lazy-mounts and is configured for offline fonts', async () => {
  // The "Add a hub" modal auto-opens once on boot (no hub configured, AppShell.tsx)
  // and its backdrop blocks the activity bar. It can pop up at any point during the
  // earlier tests (`init()` resolves async), so close it deterministically here.
  // `toPass` re-runs until the modal is gone, absorbing the open/animation race;
  // it's also a no-op if the modal was never present (count 0 → the assertion holds).
  await expect(async () => {
    const closeBtn = page.locator('.connect .connect-head button');
    if ((await closeBtn.count()) > 0) await closeBtn.click({ timeout: 2000 });
    await expect(page.locator('.connect')).toHaveCount(0);
  }).toPass({ timeout: 15_000 });

  // Navigate to Author by clicking its activity-bar button — a keyboard shortcut
  // (Ctrl+4) is swallowed when a modal or the terminal xterm holds focus.
  await page.getByRole('button', { name: 'Author', exact: true }).click();
  // Open the categorized "New ▾" menu and pick Sketch → an in-memory sketch doc
  // (no workspace folder in CI). The standalone "New X" buttons collapsed into
  // this menu in the W1 shell cleanup.
  await page.locator('.author-newcaret').click();
  await page.getByRole('menuitem', { name: 'Sketch (Excalidraw)' }).click();

  // The Excalidraw canvas mounted — the lazy chunk resolved and its React tree
  // painted without crashing on the `app://` origin.
  await expect(page.locator('.excalidraw-host .excalidraw').first()).toBeVisible({ timeout: 20_000 });
  // The font loader was pointed at the local copy, so it will not fall back to the
  // esm.sh CDN. (Set at the ExcalidrawEditor module scope, which only evaluates
  // once the lazy chunk above has loaded.)
  const assetPath = await page.evaluate(
    () => (window as unknown as { EXCALIDRAW_ASSET_PATH?: string }).EXCALIDRAW_ASSET_PATH,
  );
  expect(assetPath).toBe('/excalidraw-assets/');
});

// ── Author shell: New ▾ menu + workspace-pane fold (W1 shell cleanup) ─────────
// The six standalone "New X" buttons collapsed into one categorized New ▾ menu,
// and the left pane is workspace-only with one persistent header toggle. Pin
// the create-from-menu path and both states of that stable fold control.
test('author: the New ▾ menu creates a document and the workspace pane folds', async () => {
  await page.getByRole('button', { name: 'Author', exact: true }).click();
  const workspaceToggle = page.locator('.surface-author .surface-leading-actions .header-pane-toggle.left');
  await expect(workspaceToggle).toBeVisible();
  // This state persists across retries and neighboring tests, so explicitly
  // establish the open precondition before exercising the create flow.
  if ((await workspaceToggle.getAttribute('aria-pressed')) !== 'true') await workspaceToggle.click();
  await expect(page.locator('.author-nav')).toBeVisible();
  // Open the New ▾ menu and create a Document from it (menuitem, not the primary
  // button — this exercises the menu path).
  await page.locator('.author-newcaret').click();
  await page.getByRole('menuitem', { name: 'New', exact: true }).click();
  await expect(page.locator('.read-tabstrip .read-tabitem').last()).toBeVisible();
  // The same header control folds and restores the pane; the affordance no
  // longer jumps to a body-edge reveal rail while closed.
  await workspaceToggle.click();
  await expect(page.locator('.author-nav')).toHaveCount(0);
  await expect(workspaceToggle).toHaveAttribute('aria-pressed', 'false');
  await workspaceToggle.click();
  await expect(page.locator('.author-nav')).toBeVisible();
  await expect(workspaceToggle).toHaveAttribute('aria-pressed', 'true');
});

// ── Author outline: right-hand heading nav + jump-to-line (W2) ───────────────
// The markdown editor gains an Obsidian-style outline on the right (the shared
// MarkdownOutline rail, extended to drive the CodeMirror source pane). Pin that
// it lists the document's headings and that a click jumps the source editor to
// the heading's line.
test('author: the markdown outline lists headings and jumps the source editor', async () => {
  await page.getByRole('button', { name: 'Author', exact: true }).click();
  // A fresh Document from the New ▾ menu.
  await page.locator('.author-newcaret').click();
  await page.getByRole('menuitem', { name: 'New', exact: true }).click();
  // Split mode keeps both the editor and preview live.
  await page.getByRole('button', { name: 'Split', exact: true }).click();
  // Type a two-heading document into the editor. `force` skips the actionability
  // retry loop — a plain click on the CodeMirror contenteditable hangs under
  // xvfb (the pointer-stability check never settles).
  const editor = page.locator('.md-editor .cm-content').last();
  await editor.click({ force: true });
  await editor.focus();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.type('# First heading\n\nalpha\n\n## Second heading\n\nbeta\n');
  // The outline rail appears on the right listing both headings (it hides at
  // ≤ 1 heading, so its presence also proves the recompute ran).
  const outline = page.locator('.mdreader-outline.side-right');
  await expect(outline).toBeVisible({ timeout: 15_000 });
  await expect(outline.getByRole('button', { name: 'Second heading' })).toBeVisible();
  // Clicking the second heading jumps the source editor to its line — the active
  // line becomes the `## Second heading` line.
  await outline.getByRole('button', { name: 'Second heading' }).click();
  await expect(page.locator('.md-editor .cm-activeLine').last()).toContainText('Second heading');
});

// ── Author canvas v2: React Flow board + JSON Canvas 1.0 body (W3) ───────────
// The canvas editor is rebuilt on React Flow and its body/on-disk format is
// JSON Canvas 1.0 (Obsidian-interoperable). Pin that a board mounts (the lazy
// React Flow chunk resolves), notes add as nodes, and the persisted body is
// JSON Canvas (`nodes`), NOT the legacy `{cards,edges}` shape — the round-trip
// serialization is what makes "restores on reload" true.
test('author: a canvas board mounts on React Flow and saves as JSON Canvas', async () => {
  await page.getByRole('button', { name: 'Author', exact: true }).click();
  await page.locator('.author-newcaret').click();
  await page.getByRole('menuitem', { name: 'Board (canvas)' }).click();
  // The React Flow surface mounts (its lazy chunk resolved).
  await expect(page.locator('.canvas-flow')).toBeVisible({ timeout: 20_000 });
  // Add two notes from the toolbar → two nodes on the board.
  await page.getByRole('button', { name: 'Note', exact: true }).click();
  await page.getByRole('button', { name: 'Note', exact: true }).click();
  await expect(page.locator('.react-flow__node')).toHaveCount(2);
  // The persisted document body is JSON Canvas 1.0 (a `nodes` array of length 2),
  // never the legacy `{cards,edges}` shape — the round-trip that makes "restores
  // on reload" true. The documents store persists on a 400ms debounce, so poll.
  await expect
    .poll(
      async () =>
        page.evaluate(() => {
          const raw = localStorage.getItem('termipod.documents.v1');
          if (raw === null) return 'no-store';
          const docs = (JSON.parse(raw) as { docs: { kind: string; body: string }[] }).docs;
          const canvas = docs.find((d) => d.kind === 'canvas');
          if (canvas === undefined) return 'no-doc';
          const parsed = JSON.parse(canvas.body) as { nodes?: unknown[]; cards?: unknown[] };
          if (parsed.cards !== undefined) return 'legacy';
          return Array.isArray(parsed.nodes) ? `nodes:${parsed.nodes.length}` : 'no-nodes';
        }),
      { timeout: 10_000 },
    )
    .toBe('nodes:2');
});

// ── Web tab: real <webview> guest (read-web-tabs plan W1) ────────────────────
// The Read surface's in-app browser tab is an Electron <webview> guest in the
// isolated `persist:webtab` partition. This pins the load-bearing invariants that
// the guest hardening in webtab.ts enforces (native menus / real logins are
// device-verified): a guest loads a real page, its title propagates, it does NOT
// carry the preload bridge, and it cannot reach the privileged `app://` scheme
// (registered on defaultSession only). Serving from an in-test http server keeps
// it offline + deterministic.
test('web tab: a <webview> guest loads, isolates the bridge, and cannot reach app://', async () => {
  const server = http.createServer((_req, res) => {
    res.setHeader('content-type', 'text/html; charset=utf-8');
    res.end('<!doctype html><html><head><title>E2E Webview OK</title></head><body>hello guest</body></html>');
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', () => r()));
  const { port } = server.address() as AddressInfo;
  const guestUrl = `http://127.0.0.1:${port}/`;
  try {
    const result = await page.evaluate(async (url) => {
      const wv = document.createElement('webview') as HTMLElement & {
        getTitle(): string;
        executeJavaScript(code: string): Promise<unknown>;
      };
      wv.setAttribute('src', url);
      // The main-process `will-attach-webview` guard REJECTS any partition other
      // than persist:webtab, so setting it correctly is also what lets the guest
      // attach at all (an implicit test of that enforcement).
      wv.setAttribute('partition', 'persist:webtab');
      wv.style.width = '400px';
      wv.style.height = '300px';
      document.body.appendChild(wv);
      await new Promise<void>((resolve, reject) => {
        const to = setTimeout(() => reject(new Error('webview load timeout')), 15_000);
        wv.addEventListener('did-finish-load', () => { clearTimeout(to); resolve(); }, { once: true });
        wv.addEventListener('did-fail-load', (e) => {
          if ((e as unknown as { isMainFrame?: boolean }).isMainFrame === false) return;
          clearTimeout(to);
          reject(new Error('did-fail-load ' + String((e as unknown as { errorCode?: number }).errorCode)));
        });
      });
      const title = wv.getTitle();
      const hasBridge = await wv.executeJavaScript('typeof window.__ELECTRON_BRIDGE__');
      const userAgent = await wv.executeJavaScript('navigator.userAgent');
      const appFetch = await wv.executeJavaScript(
        "fetch('app://termipod/index.html').then(r => 'reached:' + r.status).catch(() => 'blocked')",
      );
      wv.remove();
      return { title, hasBridge, userAgent, appFetch };
    }, guestUrl);
    expect(result.title).toBe('E2E Webview OK');
    // No preload → the bridge (and the whole command allowlist) never exists here.
    expect(result.hasBridge).toBe('undefined');
    // Do not impersonate stock Chrome. A rewritten UA misrepresents the client
    // and is itself a bot-detection signal; use Electron's truthful default.
    expect(result.userAgent).toContain('Electron/');
    // The app:// scheme handler is installed on defaultSession only — the guest
    // partition can't resolve it.
    expect(result.appFetch).toBe('blocked');
  } finally {
    await new Promise<void>((r) => server.close(() => r()));
  }
});

// ── Session web panel: the kimiweb guest partition (agent-transcript-redesign P0) ──
// The embedded `kimi web` panel runs its guest in the NON-persistent `kimiweb`
// partition, whose top-frame navigation is pinned to loopback (webtab_policy.ts)
// — the bearer token rides the URL hash, so the guest must never load an
// external origin. This pins the policy end-to-end without the kimi binary:
// a stand-in loopback server plays the SPA.
test('kimiweb guest: loopback loads, external navigation is blocked, unknown partition is refused', async () => {
  const server = http.createServer((_req, res) => {
    res.setHeader('content-type', 'text/html; charset=utf-8');
    res.end('<!doctype html><html><head><title>E2E Kimiweb OK</title></head><body>kimi stand-in</body></html>');
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', () => r()));
  const { port } = server.address() as AddressInfo;
  const guestUrl = `http://127.0.0.1:${port}/#token=e2e-token`;
  try {
    const result = await page.evaluate(async (url) => {
      const wv = document.createElement('webview') as HTMLElement & {
        getTitle(): string;
        getURL(): string;
        loadURL(u: string): Promise<void>;
        executeJavaScript(code: string): Promise<unknown>;
      };
      wv.setAttribute('src', url);
      wv.setAttribute('partition', 'kimiweb');
      wv.style.width = '400px';
      wv.style.height = '300px';
      document.body.appendChild(wv);
      await new Promise<void>((resolve, reject) => {
        const to = setTimeout(() => reject(new Error('webview load timeout')), 15_000);
        wv.addEventListener('did-finish-load', () => { clearTimeout(to); resolve(); }, { once: true });
        wv.addEventListener('did-fail-load', (e) => {
          if ((e as unknown as { isMainFrame?: boolean }).isMainFrame === false) return;
          clearTimeout(to);
          reject(new Error('did-fail-load ' + String((e as unknown as { errorCode?: number }).errorCode)));
        });
      });
      const title = wv.getTitle();
      const hasBridge = await wv.executeJavaScript('typeof window.__ELECTRON_BRIDGE__');
      // A programmatic top-frame load to an external origin must be cancelled
      // by the partition's onBeforeRequest guard (`.invalid` so nothing
      // resolves even if the policy regressed — the loadURL rejection is what
      // proves the block).
      const external = await wv.loadURL('http://kimiweb-e2e.invalid/').then(
        () => 'loaded',
        () => 'blocked',
      );
      const stayedUrl = wv.getURL();
      wv.remove();
      return { title, hasBridge, external, stayedUrl };
    }, guestUrl);
    expect(result.title).toBe('E2E Kimiweb OK');
    // No preload → the bridge never exists in the guest.
    expect(result.hasBridge).toBe('undefined');
    expect(result.external).toBe('blocked');
    // …and the guest is still on the loopback embed URL (token hash intact).
    expect(result.stayedUrl).toBe(guestUrl);

    // A partition outside the allowlist must not host a guest at all.
    const denied = await page.evaluate(async (url) => {
      const wv = document.createElement('webview') as HTMLElement & { loadURL(u: string): Promise<void> };
      wv.setAttribute('src', url);
      wv.setAttribute('partition', 'persist:not-allowlisted');
      document.body.appendChild(wv);
      let loaded = false;
      wv.addEventListener('did-finish-load', () => {
        loaded = true;
      });
      await new Promise((r) => setTimeout(r, 2000));
      wv.remove();
      return loaded;
    }, `http://127.0.0.1:${port}/`);
    expect(denied).toBe(false);
  } finally {
    // The guest's keep-alive sockets to the stand-in server would keep
    // the close callback (and with it the whole suite) pending forever —
    // destroy them before awaiting close.
    server.closeAllConnections();
    await new Promise<void>((r) => server.close(() => r()));
  }
});

test('inspect: New scratch opens a code tab on CodeMirror and the trace lens jumps', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  // Empty state until a tab is opened.
  await expect(page.locator('.inspect-empty')).toBeVisible();
  // New scratch → a code tab + a CodeMirror editor mount.
  await page.getByRole('button', { name: 'New scratch' }).click();
  await expect(page.locator('.inspect-tab').last()).toBeVisible();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  // Type a Python traceback into the scratch. The editor is contenteditable —
  // force-click + focus, never a plain click (which hangs under xvfb).
  await editor.click({ force: true });
  await editor.focus();
  await page.keyboard.type(
    'Traceback (most recent call last):\n  File "app.py", line 7, in main\n    raise ValueError("boom")\nValueError: boom',
  );
  // The trace lens detects the traceback and lists the frame; the file chip
  // carries the base name.
  await expect(page.locator('.inspect-trace')).toBeVisible();
  await expect(page.locator('.inspect-frame .frame-file').first()).toHaveText('app.py');
  // Closing the tab returns to the empty state.
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
  await expect(page.locator('.inspect-empty')).toBeVisible();
});

test('inspect: the Open menu launches the source picker modal', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  // The Open ▾ menu lists the source affordances.
  await page.getByRole('button', { name: 'Open', exact: true }).click();
  await expect(page.locator('.inspect-menu')).toBeVisible();
  // From workspace… opens the picker modal (its contents depend on whether a
  // workspace folder is set — assert the modal itself, backend-free).
  await page.getByRole('menuitem', { name: 'From workspace…' }).click();
  await expect(page.locator('.inspect-modal')).toBeVisible();
  // The × in the modal header closes it.
  await page.locator('.inspect-modal .inspect-modal-head .icon-btn').click();
  await expect(page.locator('.inspect-modal')).toHaveCount(0);
});

test('workbench: primary surface headers share one grid and action height', async () => {
  await dismissConnectModal();
  const rail = page.locator('.activity-bar');
  const railTabs = rail.locator('.activity-tab');
  await expect.poll(() => rail.evaluate((node) => node.getBoundingClientRect().width)).toBe(48);
  await expect(rail.locator('.activity-label')).toHaveCount(0);
  await expect(railTabs).toHaveCount(10);
  for (const tab of await railTabs.all()) {
    await expect(tab).toHaveAttribute('aria-label', /\S/);
    await expect.poll(() => tab.evaluate((node) => {
      const rect = node.getBoundingClientRect();
      return { width: rect.width, height: rect.height };
    })).toEqual({ width: 40, height: 40 });
  }
  const surfaces = [
    { job: 'fleet', header: '.fleet-toolbar' },
    { job: 'projects', header: '.fleet-toolbar' },
    { job: 'read', header: '.surface-read .surface-head' },
    { job: 'author', header: '.surface-author .surface-head' },
    { job: 'debug', header: '.surface-debug .surface-head' },
    { job: 'compare', header: '.surface-compare .surface-head' },
    { job: 'replay', header: '.surface-replay .surface-head' },
    { job: 'record', header: '.surface-record .surface-head' },
    { job: 'terminal', header: '.term-panel.surface .term-surface-head' },
  ];
  const heights: number[] = [];
  for (const surface of surfaces) {
    await page.locator(`[data-job="${surface.job}"]`).click();
    const header = page.locator(surface.header).first();
    await expect(header).toBeVisible();
    heights.push(await header.evaluate((node) => node.getBoundingClientRect().height));
    await expect(header).not.toContainText(/\bJ\d+\b/);
  }
  expect(heights).toEqual([48, 48, 48, 48, 48, 48, 48, 48, 48]);

  for (const job of ['author', 'debug', 'replay']) {
    await expect(page.locator(`[data-job="${job}"]`)).not.toHaveAttribute('title', /\bJ\d+\b/);
  }

  for (const job of ['fleet', 'projects', 'author', 'debug']) {
    await page.locator(`[data-job="${job}"]`).click();
    const primary = page.locator(job === 'fleet' || job === 'projects' ? '.fleet-toolbar button.primary' : `.surface-${job} .surface-head button.primary`).first();
    await expect(primary).toBeVisible();
    await expect.poll(() => primary.evaluate((node) => node.getBoundingClientRect().height)).toBe(28);
  }
});

test('macOS: empty terminal session-header space remains a window drag region', async () => {
  const os = await page.evaluate(() => window.__ELECTRON_BRIDGE__!.invoke<string>('platform_os'));
  if (os !== 'macos') return;

  await dismissConnectModal();
  await page.locator('[data-job="terminal"]').click();

  const actions = page.locator('.term-panel.surface .term-surface-actions');
  const emptySpace = actions.locator(':scope > .spacer');
  const addButton = actions.locator('.term-add-btn');
  await expect(emptySpace).toBeVisible();
  await expect(addButton).toBeVisible();
  await expect.poll(() => emptySpace.evaluate((node) => getComputedStyle(node).getPropertyValue('-webkit-app-region'))).toBe('drag');
  await expect.poll(() => addButton.evaluate((node) => getComputedStyle(node).getPropertyValue('-webkit-app-region'))).toBe('no-drag');
});

test('workbench: pane toggles stay pinned to the surface header edges', async () => {
  await dismissConnectModal();

  for (const job of ['fleet', 'projects']) {
    await page.locator(`[data-job="${job}"]`).click();
    const header = page.locator('.fleet-toolbar').first();
    const left = header.locator('.header-pane-toggle.left');
    const right = header.locator('.header-pane-toggle.right');
    await expect(left).toBeVisible();
    await expect(right).toBeVisible();
    await expect.poll(() => header.evaluate((node) => {
      const leftToggle = node.querySelector('.header-pane-toggle.left');
      const label = node.querySelector('.fleet-toolbar-label');
      const rightToggle = node.querySelector('.header-pane-toggle.right');
      return leftToggle !== null && label !== null && rightToggle !== null
        ? {
            leftBeforeLabel: (leftToggle.compareDocumentPosition(label) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0,
            rightIsLast: rightToggle === rightToggle.parentElement?.lastElementChild,
          }
        : null;
    })).toEqual({ leftBeforeLabel: true, rightIsLast: true });

    const nav = page.locator('.mission-nav');
    const dock = page.locator('.region.dock');
    const navWasOpen = (await left.getAttribute('aria-pressed')) === 'true';
    const dockWasOpen = (await right.getAttribute('aria-pressed')) === 'true';
    await left.click();
    await expect(nav).toHaveCount(navWasOpen ? 0 : 1);
    await expect(left).toHaveAttribute('aria-pressed', navWasOpen ? 'false' : 'true');
    await right.click();
    await expect(dock).toHaveCount(dockWasOpen ? 0 : 1);
    await expect(right).toHaveAttribute('aria-pressed', dockWasOpen ? 'false' : 'true');
    await left.click();
    await right.click();
  }

  await page.locator('[data-job="author"]').click();
  const authorToggle = page.locator('.surface-author .surface-leading-actions .header-pane-toggle.left');
  await expect(authorToggle).toBeVisible();
  const authorWasOpen = (await authorToggle.getAttribute('aria-pressed')) === 'true';
  await authorToggle.click();
  await expect(page.locator('.surface-author .author-nav-col')).toHaveCount(authorWasOpen ? 0 : 1);
  await expect(authorToggle).toHaveAttribute('aria-pressed', authorWasOpen ? 'false' : 'true');
  await authorToggle.click();

  await page.locator('[data-job="read"]').click();
  await page.locator('.surface-read .surface-head .seg-btn').first().click();
  const readLeft = page.locator('.surface-read .surface-leading-actions .header-pane-toggle.left');
  const readRight = page.locator('.surface-read .surface-actions .header-pane-toggle.right');
  await expect(readLeft).toBeVisible();
  await expect(readRight).toBeVisible();
  const railWasOpen = (await readLeft.getAttribute('aria-pressed')) === 'true';
  const inspectorWasOpen = (await readRight.getAttribute('aria-pressed')) === 'true';
  await readLeft.click();
  await expect(page.locator('.surface-read .read-rail')).toHaveCount(railWasOpen ? 0 : 1);
  await readRight.click();
  await expect(page.locator('.surface-read .read-inspector-pane')).toHaveCount(inspectorWasOpen ? 0 : 1);
  await readLeft.click();
  await readRight.click();

  const remaining = [
    { job: 'compare', pane: '.compare-runs', toggle: '.surface-compare .header-pane-toggle.left' },
    { job: 'replay', pane: '.replay-rail', toggle: '.surface-replay .header-pane-toggle.left' },
    { job: 'record', pane: '.record-form', toggle: '.surface-record .header-pane-toggle.left' },
    { job: 'terminal', pane: '.term-nav', toggle: '.term-panel.surface .header-pane-toggle.left' },
  ];
  for (const surface of remaining) {
    await page.locator(`[data-job="${surface.job}"]`).click();
    const toggle = page.locator(surface.toggle);
    const pane = page.locator(surface.pane);
    await expect(toggle).toBeVisible();
    const wasOpen = (await toggle.getAttribute('aria-pressed')) === 'true';
    await toggle.click();
    if (wasOpen) await expect(pane).toBeHidden();
    else await expect(pane).toBeVisible();
    await expect(toggle).toHaveAttribute('aria-pressed', wasOpen ? 'false' : 'true');
    await toggle.click();
  }
});

test('workbench: left-pane header cells align actions after the body divider', async () => {
  await dismissConnectModal();
  const surfaces = [
    { job: 'fleet', identity: '.fleet-toolbar-identity', pane: '.mission-nav', action: '.fleet-toolbar-actions button.primary' },
    { job: 'projects', identity: '.fleet-toolbar-identity', pane: '.mission-nav', action: '.fleet-toolbar-actions button.primary' },
    { job: 'read', identity: '.surface-identity', pane: '.read-rail', action: '.surface-actions .seg-btn' },
    { job: 'author', identity: '.surface-identity', pane: '.author-nav-col', action: '.surface-actions button.primary' },
    { job: 'compare', identity: '.surface-identity', pane: '.compare-runs', action: '.surface-actions select' },
    { job: 'replay', identity: '.surface-identity', pane: '.replay-rail', action: '.surface-actions select' },
    { job: 'terminal', identity: '.term-surface-identity', pane: '.term-nav', action: '.term-surface-actions .term-tabs' },
  ];

  for (const surface of surfaces) {
    await page.locator(`[data-job="${surface.job}"]`).click();
    if (surface.job === 'read') await page.locator('.surface-read .surface-head .seg-btn').first().click();
    const toggle = page.locator(
      surface.job === 'fleet' || surface.job === 'projects'
        ? '.fleet-toolbar .header-pane-toggle.left'
        : surface.job === 'terminal'
          ? '.term-surface-head .header-pane-toggle.left'
          : `.surface-${surface.job} .header-pane-toggle.left`,
    ).first();
    if ((await toggle.getAttribute('aria-pressed')) !== 'true') await toggle.click();
    const identity = page.locator(surface.identity).first();
    const pane = page.locator(surface.pane).first();
    const action = page.locator(surface.action).first();
    await expect(identity).toBeVisible();
    await expect(pane).toBeVisible();
    await expect(action).toBeVisible();
    const metrics = await Promise.all(
      [identity, pane, action].map((locator) =>
        locator.evaluate((node) => {
          const rect = node.getBoundingClientRect();
          return { left: Math.round(rect.left), right: Math.round(rect.right) };
        }),
      ),
    );
    expect(metrics[0]?.right).toBe(metrics[1]?.right);
    expect(metrics[2]?.left ?? 0).toBeGreaterThan(metrics[0]?.right ?? 0);
  }
});

test('read: the empty inspector starts without a redundant tabs row', async () => {
  await page.locator('[data-job="read"]').click();
  await page.locator('.surface-read .surface-head .seg-btn').first().click();
  const listBar = page.locator('.read-list-bar');
  const emptyInspector = page.locator('.read-inspector-pane .ref-inspector-empty-wrap');
  await expect(listBar).toBeVisible();
  await expect(emptyInspector).toBeVisible();
  await expect(emptyInspector.locator('.ref-tabs')).toHaveCount(0);
  const metrics = await Promise.all(
    [listBar, emptyInspector].map((locator) =>
      locator.evaluate((node) => {
        const rect = node.getBoundingClientRect();
        return { top: rect.top };
      }),
    ),
  );
  expect(metrics[0]?.top).toBe(metrics[1]?.top);
  await expect(listBar.locator('button.primary')).toContainText('Add');

  await page.locator('.surface-read .surface-head .seg-btn').nth(1).click();
  const discoverBar = page.locator('.discover-bar');
  const discoverInput = discoverBar.locator('input');
  const discoverButton = discoverBar.locator('button.primary');
  const sourceChip = page.locator('.discover-src').first();
  await expect(discoverBar).toBeVisible();
  await expect(sourceChip).toBeVisible();
  const discoverMetrics = await Promise.all(
    [discoverBar, discoverInput, discoverButton, sourceChip].map((locator) =>
      locator.evaluate((node) => {
        const rect = node.getBoundingClientRect();
        return { top: rect.top, bottom: rect.bottom, height: rect.height };
      }),
    ),
  );
  expect(discoverMetrics[1]).toEqual(discoverMetrics[2]);
  expect(discoverMetrics[3]?.height).toBe(28);
  expect((discoverMetrics[3]?.top ?? 0) - (discoverMetrics[0]?.bottom ?? 0)).toBeGreaterThanOrEqual(8);
});

test('inspect: tree filter and content search use two rows at most', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'termipod-inspect-search-'));
  fs.writeFileSync(path.join(dir, 'sample.txt'), 'needle in a file\n');
  try {
    await page.evaluate((rootPath) => {
      localStorage.setItem(
        'termipod.inspect.roots',
        JSON.stringify([{ id: 'e2e-search-root', source: 'local', label: 'search-layout', path: rootPath }]),
      );
    }, dir);
    await page.reload({ waitUntil: 'domcontentloaded' });
    await dismissConnectModal();
    await page.getByRole('button', { name: 'Inspect', exact: true }).click();

    const treeToggle = page.locator('.surface-debug .surface-leading-actions .header-pane-toggle.left');
    await expect(treeToggle).toBeVisible();
    await expect(treeToggle).toHaveAttribute('aria-pressed', 'true');
    await treeToggle.click();
    await expect(page.locator('.surface-debug .inspect-tree')).toHaveCount(0);
    await expect(treeToggle).toHaveAttribute('aria-pressed', 'false');
    await treeToggle.click();

    const root = page.locator('.inspect-tree-root', { hasText: 'search-layout' });
    await page.locator('.surface-debug .surface-head button.primary').click();
    const treeHead = page.locator('.inspect-tree-head');
    const tabs = page.locator('.inspect-tabs');
    await expect(treeHead).toBeVisible();
    await expect(tabs).toBeVisible();
    const paneMetrics = await Promise.all(
      [treeHead, tabs].map((locator) =>
        locator.evaluate((node) => {
          const rect = node.getBoundingClientRect();
          return { top: rect.top, bottom: rect.bottom, height: rect.height };
        }),
      ),
    );
    expect(paneMetrics[0]).toEqual(paneMetrics[1]);
    expect(paneMetrics[0]?.height).toBe(40);

    const filterBar = root.locator('.inspect-tree-filterbar');
    await expect(filterBar).toBeVisible();
    await expect(filterBar.getByPlaceholder('Filter this tree…')).toBeVisible();
    const contentToggle = filterBar.getByRole('button', { name: 'Search contents' });
    await expect(contentToggle).toHaveAttribute('aria-expanded', 'false');
    await expect(root.locator('.inspect-tree-searchbar')).toHaveCount(0);
    // The old redundant label occupied its own permanent row; the trigger now
    // lives in the filename-filter bar and reveals only one additional row.
    await expect(root.locator('.inspect-tree-search > .inspect-tree-searchtoggle')).toHaveCount(0);

    await contentToggle.click();
    await expect(contentToggle).toHaveAttribute('aria-expanded', 'true');
    await expect(root.locator('.inspect-tree-searchbar')).toBeVisible();
    await expect(root.getByPlaceholder('Search file contents…')).toBeFocused();
    await page.locator('.inspect-tab .inspect-tab-close').last().click();
  } finally {
    await page.evaluate(() => localStorage.removeItem('termipod.inspect.roots'));
    await page.reload({ waitUntil: 'domcontentloaded' });
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('inspect: the tree-sitter symbol outline lists symbols and jumps the editor', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  // Choose JavaScript (brace-based → immune to auto-indent) so the outline
  // activates and the WASM grammar loads on demand.
  await page.locator('.inspect-runbar .surface-select').selectOption('javascript');
  await editor.click({ force: true });
  await editor.focus();
  await page.keyboard.type('function alpha(){ return 1; }\nfunction beta(){ return 2; }\nclass Gamma { m(){ return 3; } }\n');
  // The outline rail appears with the extracted symbols (grammar wasm fetched
  // from app:// on demand — allow generous time).
  const outline = page.locator('.code-outline');
  await expect(outline).toBeVisible({ timeout: 15000 });
  await expect(outline.locator('.code-outline-name', { hasText: 'alpha' })).toBeVisible();
  await expect(outline.locator('.code-outline-name', { hasText: 'Gamma' })).toBeVisible();
  // Clicking a symbol jumps the editor caret to its line.
  await outline.locator('.code-outline-item', { hasText: 'beta' }).click();
  await expect(page.locator('.inspect-code .cm-activeLine')).toContainText('beta');
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: a pasted patch renders the multi-file diff viewer (W2)', async () => {
  await dismissConnectModal();
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  await editor.click({ force: true });
  await editor.focus();
  // A two-file git patch. CRITICAL: no line may start with whitespace — a plain
  // scratch has no language, so CM's newline command copies the previous line's
  // leading indent; a ` context` line would then indent every following line and
  // break `^@@`/`^diff --git` matching. Pure delete + add hunks have no
  // leading-space lines, so the typed text round-trips verbatim.
  await page.keyboard.type(
    [
      'diff --git a/del.txt b/del.txt',
      'deleted file mode 100644',
      '--- a/del.txt',
      '+++ /dev/null',
      '@@ -1,2 +0,0 @@',
      '-alpha',
      '-beta',
      'diff --git a/add.txt b/add.txt',
      'new file mode 100644',
      '--- /dev/null',
      '+++ b/add.txt',
      '@@ -0,0 +1,2 @@',
      '+gamma',
      '+delta',
    ].join('\n'),
  );
  // The content sniffs as a patch → the "View as diff" affordance appears.
  await page.getByRole('button', { name: 'View as diff' }).click();
  // The patch viewer renders one card per file (git-diff-view lazy chunk).
  await expect(page.locator('.patch-file')).toHaveCount(2, { timeout: 15000 });
  await expect(page.locator('.patch-file-path').first()).toContainText('del.txt');
  // Both a delete (A→D) and an add badge render.
  await expect(page.locator('.patch-status.k-add')).toBeVisible();
  await expect(page.locator('.patch-status.k-delete')).toBeVisible();
  // "View source" flips the tab back to the editor.
  await page.getByRole('button', { name: 'View source' }).click();
  await expect(page.locator('.inspect-code .cm-content')).toBeVisible();
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: comparing two open tabs scrolls and resizes both merge panes (W2)', async () => {
  await dismissConnectModal();
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  // Tab A.
  await page.getByRole('button', { name: 'New scratch' }).click();
  let editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  await editor.click({ force: true });
  await editor.focus();
  await page.keyboard.insertText(
    Array.from({ length: 140 }, (_, index) => `left-${index}: ${'alpha '.repeat(24)}`).join('\n'),
  );
  // Tab B (becomes active).
  await page.getByRole('button', { name: 'New scratch' }).click();
  editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  await editor.click({ force: true });
  await editor.focus();
  await page.keyboard.insertText(
    Array.from({ length: 140 }, (_, index) => `right-${index}: ${'BETA '.repeat(24)}`).join('\n'),
  );
  // Compare ▾ → the first "open tab" entry is the other scratch. Scope to the
  // surface (`main`): "Compare" also names the J5 activity-bar tab in the nav,
  // so an unscoped role query is a strict-mode collision.
  await page.getByRole('main').getByRole('button', { name: 'Compare', exact: true }).click();
  await expect(page.locator('.inspect-menu')).toBeVisible();
  await page.locator('.inspect-menu-item').first().click();
  // The @codemirror/merge view mounts (its own lazy chunk).
  const merge = page.locator('.compare-host .cm-mergeView');
  await expect(merge).toBeVisible({ timeout: 15000 });

  // The merge root owns vertical scrolling; each CodeMirror scroller keeps its
  // own horizontal axis for long source lines.
  await expect.poll(() => merge.evaluate((node) => node.scrollHeight - node.clientHeight)).toBeGreaterThan(20);
  await merge.evaluate((node) => {
    node.scrollTop = Math.min(240, node.scrollHeight - node.clientHeight);
  });
  await expect.poll(() => merge.evaluate((node) => node.scrollTop)).toBeGreaterThan(0);
  const leftScroller = merge.locator('.cm-scroller').first();
  await expect.poll(() => leftScroller.evaluate((node) => node.scrollWidth - node.clientWidth)).toBeGreaterThan(20);
  await leftScroller.evaluate((node) => {
    node.scrollLeft = Math.min(180, node.scrollWidth - node.clientWidth);
  });
  await expect.poll(() => leftScroller.evaluate((node) => node.scrollLeft)).toBeGreaterThan(0);

  // The shared divider resizes the two compare panes and persists their ratio.
  const leftPane = merge.locator('.cm-mergeViewEditor').first();
  const beforeWidth = await leftPane.evaluate((node) => node.getBoundingClientRect().width);
  const divider = page.locator('.compare-split-handle .resize-handle');
  await expect(divider).toBeVisible();
  await divider.evaluate((node) => {
    const x = node.getBoundingClientRect().left;
    node.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, button: 0, clientX: x }));
    window.dispatchEvent(new PointerEvent('pointermove', { bubbles: true, clientX: x + 80 }));
    window.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, clientX: x + 80 }));
  });
  await expect.poll(() => leftPane.evaluate((node) => node.getBoundingClientRect().width)).toBeGreaterThan(beforeWidth + 40);
  await expect.poll(() => page.evaluate(() => Number(localStorage.getItem('termipod.inspect.compareRatio')))).toBeGreaterThan(0.5);
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: a pasted log renders the virtualized log viewer, filters and searches (W3)', async () => {
  await dismissConnectModal();
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  await editor.click({ force: true });
  await editor.focus();
  // A log-shaped paste (level words + step/epoch markers) so `looksLikeLog` fires
  // and "View as log" appears. NO line starts with whitespace — a plain scratch's
  // newline command copies the previous line's indent (the W2 auto-indent trap).
  await page.keyboard.type(
    [
      '2026-07-23 10:00:00 INFO starting run',
      'epoch 1 step 100 loss=2.30',
      'epoch 1 step 200 loss=1.90',
      'WARN gpu memory high',
      'epoch 2 step 300 loss=1.20',
      'ERROR nan encountered',
      'done',
    ].join('\n'),
  );
  // The content sniffs as a log → the "View as log" affordance appears.
  await page.getByRole('button', { name: 'View as log' }).click();
  // The virtualized viewer mounts (its own lazy chunk — react-virtuoso + anser).
  await expect(page.locator('.logview')).toBeVisible({ timeout: 15000 });
  await expect(page.locator('.logview-row').first()).toBeVisible();
  await expect(page.locator('.logview-count')).toContainText('7');

  // Regex search over the whole log: three lines carry "epoch".
  await page.locator('.logview-input').fill('epoch');
  await expect(page.locator('.logview-hitn')).toContainText('1/3');
  await page.locator('.logview-input').fill('');

  // Error/warn quick-filter narrows the view to the WARN + ERROR lines (2).
  await page.locator('.logview-btn', { hasText: 'Warn/Err' }).click();
  await expect(page.locator('.logview-count')).toContainText('matching');
  await expect(page.locator('.logview-count')).toContainText('2');

  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: a pasted DOT graph renders to SVG via the wasm engine (graph)', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  await editor.click({ force: true });
  await editor.focus();
  // DOT graph — line 1 is unindented so `looksLikeDot` fires; inner lines may be
  // auto-indented (harmless — DOT is whitespace-insensitive, semicolons terminate).
  await page.keyboard.type(['digraph G {', 'rankdir=LR;', 'alpha -> beta;', 'beta -> gamma;', '}'].join('\n'));
  await page.getByRole('button', { name: 'View as graph' }).click();
  // The lazy DotGraphView mounts and the wasm Graphviz engine renders an SVG.
  await expect(page.locator('.dotgraph')).toBeVisible({ timeout: 20000 });
  await expect(page.locator('.dotgraph-svg svg')).toBeVisible({ timeout: 20000 });
  // Graphviz emits node labels as SVG <text>; our nodes are alpha/beta/gamma.
  await expect(page.locator('.dotgraph-svg svg')).toContainText('alpha');
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: the Trace model graph form opens and Detect round-trips the interpreter', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  // Make it a Python tab so the "Trace model graph" affordance appears.
  await page.locator('.inspect-runbar .surface-select').selectOption('python');
  await editor.click({ force: true });
  await page.keyboard.type('class Model:\n    pass');
  // Open the trace form.
  await page.getByRole('button', { name: 'Trace model graph' }).click();
  await expect(page.locator('.trace-modal')).toBeVisible();
  // The interpreter defaults to python3; Detect probes it for torch/torchview.
  // The runner has python3 but not torch → the probe round-trips to an error;
  // either outcome (ok/err) proves the trace_run IPC path works end-to-end.
  await page.getByRole('button', { name: 'Detect', exact: true }).click();
  // The result element appearing is what proves the trace_run IPC round-tripped;
  // assert it is ATTACHED, not pixel-visible — in xvfb's small window the tall modal
  // can clip/scroll the result, and its visibility isn't what this smoke verifies.
  await expect(page.locator('.trace-ok, .trace-err').first()).toBeAttached({ timeout: 25000 });
  // Tier 2 (Traced ops / torch.export) shares the venue plumbing but probes torch
  // only. The first select in the modal body is the Graph tier; switching it and
  // re-detecting round-trips detectTorch → trace_run → python3 (no torch → err).
  await page.locator('.trace-modal .surface-select').first().selectOption('traced');
  await page.getByRole('button', { name: 'Detect', exact: true }).click();
  await expect(page.locator('.trace-ok, .trace-err').first()).toBeAttached({ timeout: 25000 });
  // Close the modal (backdrop click) and the tab.
  await page.locator('.inspect-modal-backdrop').click({ position: { x: 5, y: 5 } });
  await expect(page.locator('.trace-modal')).toHaveCount(0);
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: the Call graph form opens and Detect round-trips the interpreter', async () => {
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await page.getByRole('button', { name: 'New scratch' }).click();
  const editor = page.locator('.inspect-code .cm-content');
  await expect(editor).toBeVisible();
  // A Python tab surfaces the "Call graph" affordance (code2flow: py/js/rb/php).
  await page.locator('.inspect-runbar .surface-select').selectOption('python');
  await editor.click({ force: true });
  await page.keyboard.type('def a():\n    b()\n\ndef b():\n    pass');
  await page.getByRole('button', { name: 'Call graph' }).click();
  await expect(page.locator('.trace-modal')).toBeVisible();
  // The runner has python3 but not code2flow → the probe round-trips to an error;
  // either outcome (ok/err) proves the reused trace_run IPC path works end-to-end.
  await page.getByRole('button', { name: 'Detect', exact: true }).click();
  await expect(page.locator('.trace-ok, .trace-err').first()).toBeAttached({ timeout: 25000 });
  await page.locator('.inspect-modal-backdrop').click({ position: { x: 5, y: 5 } });
  await expect(page.locator('.trace-modal')).toHaveCount(0);
  await page.locator('.inspect-tab .inspect-tab-close').last().click();
});

test('inspect: the log index commands slice + search a file without slurping it (W3)', async () => {
  // Exercise the main-process line index directly through the bridge — the
  // no-whole-file-read path LogView's IndexedLogModel drives.
  const p = path.join(os.tmpdir(), `tp-w3-${process.pid}.log`);
  fs.writeFileSync(p, 'boot\nWARN low disk\ninfo tick\nERROR boom\nbye\n');
  try {
    const opened = await page.evaluate(
      (fp) => window.__ELECTRON_BRIDGE__!.invoke<{ id: string; size: number; lines: number }>('log_open', { path: fp }),
      p,
    );
    expect(opened.lines).toBe(5);
    expect(opened.id).toMatch(/^log\d+$/);

    const sl = await page.evaluate(
      (id) => window.__ELECTRON_BRIDGE__!.invoke<{ lines: string[] }>('log_slice', { id, from: 1, count: 1 }),
      opened.id,
    );
    expect(sl.lines).toEqual(['WARN low disk']);

    const se = await page.evaluate(
      (id) => window.__ELECTRON_BRIDGE__!.invoke<{ hits: Array<{ line: number }> }>('log_search', { id, pattern: 'WARN|ERROR', flags: 'i', max: 10 }),
      opened.id,
    );
    expect(se.hits.map((h) => h.line)).toEqual([1, 3]);

    await page.evaluate((id) => window.__ELECTRON_BRIDGE__!.invoke('log_close', { id }), opened.id);
  } finally {
    fs.rmSync(p, { force: true });
  }
});

test('inspect: checkpoint_inspect parses a safetensors header (W4)', async () => {
  // A safetensors file = u64 LE header length + JSON header + tensor bytes. The
  // parser reads only the header, so zero-padded data suffices.
  const header = {
    __metadata__: { format: 'pt' },
    'model.layers.0.attn.weight': { dtype: 'F16', shape: [4, 4], data_offsets: [0, 32] },
    'lm_head.weight': { dtype: 'F32', shape: [8, 4], data_offsets: [32, 160] },
  };
  const json = Buffer.from(JSON.stringify(header), 'utf8');
  const len = Buffer.alloc(8);
  len.writeBigUInt64LE(BigInt(json.length));
  const p = path.join(os.tmpdir(), `tp-w4-${process.pid}.safetensors`);
  fs.writeFileSync(p, Buffer.concat([len, json, Buffer.alloc(160)]));
  try {
    const info = await page.evaluate(
      (fp) =>
        window.__ELECTRON_BRIDGE__!.invoke<{ format: string; totalParams: number; tensorCount: number; tensors: Array<{ name: string }> }>(
          'checkpoint_inspect',
          { path: fp },
        ),
      p,
    );
    expect(info.format).toBe('safetensors');
    expect(info.tensorCount).toBe(2);
    expect(info.totalParams).toBe(16 + 32);
    expect(info.tensors.map((x) => x.name)).toContain('lm_head.weight');
  } finally {
    fs.rmSync(p, { force: true });
  }
});

test('inspect: checkpoint_inspect parses an ONNX graph (W4 remainder)', async () => {
  // Proves the BUNDLED main.cjs decodes ONNX: encode a ModelProto (incl. a
  // raw_data blob the parser must skip), then round-trip it through the real IPC.
  const enc = `
    syntax = "proto3"; package onnx;
    message OperatorSetIdProto { string domain = 1; int64 version = 2; }
    message TensorProto { repeated int64 dims = 1; int32 data_type = 2; string name = 8; bytes raw_data = 9; }
    message ValueInfoProto { string name = 1; }
    message NodeProto { repeated string input = 1; repeated string output = 2; string name = 3; string op_type = 4; }
    message GraphProto {
      repeated NodeProto node = 1; string name = 2; repeated TensorProto initializer = 5;
      repeated ValueInfoProto input = 11; repeated ValueInfoProto output = 12;
    }
    message ModelProto { int64 ir_version = 1; string producer_name = 2; GraphProto graph = 7; repeated OperatorSetIdProto opset_import = 8; }
  `;
  const Model = protobuf.parse(enc).root.lookupType('onnx.ModelProto');
  const bytes = Model.encode(
    Model.create({
      irVersion: 9,
      producerName: 'pytorch',
      opsetImport: [{ version: 18 }],
      graph: {
        name: 'g',
        input: [{ name: 'x' }],
        output: [{ name: 'y' }],
        node: [
          { opType: 'MatMul', name: 'mm', input: ['x', 'model.layers.0.weight'], output: ['h'] },
          { opType: 'Relu', name: 'act', input: ['h'], output: ['y'] },
        ],
        initializer: [
          { name: 'model.layers.0.weight', dataType: 1, dims: [4, 4], rawData: Buffer.alloc(1024, 3) },
          { name: 'model.layers.1.weight', dataType: 10, dims: [4, 8] },
        ],
      },
    }),
  ).finish();
  const p = path.join(os.tmpdir(), `tp-w4onnx-${process.pid}.onnx`);
  fs.writeFileSync(p, Buffer.from(bytes));
  try {
    const info = await page.evaluate(
      (fp) =>
        window.__ELECTRON_BRIDGE__!.invoke<{
          format: string;
          totalParams: number;
          tensorCount: number;
          ops?: Record<string, number>;
          graph?: { nodes: { opType: string; inputs: string[]; outputs: string[] }[]; inputs: string[]; outputs: string[] };
        }>('checkpoint_inspect', { path: fp }),
      p,
    );
    expect(info.format).toBe('onnx');
    expect(info.tensorCount).toBe(2);
    expect(info.totalParams).toBe(16 + 32);
    expect(info.ops).toEqual({ MatMul: 1, Relu: 1 });
    // The bundled main.cjs retains the wired operator graph (for "View as graph").
    expect(info.graph?.nodes.length).toBe(2);
    expect(info.graph?.nodes[0]).toEqual({ name: 'mm', opType: 'MatMul', inputs: ['x', 'model.layers.0.weight'], outputs: ['h'] });
    expect(info.graph?.outputs).toEqual(['y']);
  } finally {
    fs.rmSync(p, { force: true });
  }
});

// A forge (GitHub) root end-to-end against a loopback stand-in (plan §5.9): the
// forge base URL is overridden via localStorage to point at a local server
// serving canned repo / commit / tree / blob JSON, so ref-pinning → tree fold →
// lazy blob read are exercised without the network.
test('inspect: a GitHub repo root resolves, lists its tree, and opens a blob (T3 loopback)', async () => {
  const server = http.createServer((req, res) => {
    const u = (req.url ?? '').split('?')[0];
    const json = (o: unknown): void => {
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify(o));
    };
    if (u === '/repos/acme/widget') return json({ default_branch: 'main' });
    if (u.startsWith('/repos/acme/widget/commits/')) return json({ sha: 'cafe1234' });
    if (u.startsWith('/repos/acme/widget/git/trees/'))
      return json({ truncated: false, tree: [{ path: 'README.md', type: 'blob' }, { path: 'src', type: 'tree' }, { path: 'src/app.py', type: 'blob' }] });
    if (u.startsWith('/repos/acme/widget/contents/README.md')) {
      res.setHeader('content-type', 'text/plain');
      return void res.end('# Widget\nhello from the loopback forge');
    }
    res.statusCode = 404;
    res.setHeader('content-type', 'application/json');
    res.end('{}');
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', () => r()));
  const { port } = server.address() as AddressInfo;
  try {
    await dismissConnectModal();
    await page.getByRole('button', { name: 'Inspect', exact: true }).click();
    // Point the forge API at the loopback server (the app never writes this key).
    await page.evaluate((base) => localStorage.setItem('termipod.forge.githubApi', base), `http://127.0.0.1:${port}`);

    // Open the add-repo dialog and pin acme/widget (shorthand → GitHub default branch).
    await page.getByRole('button', { name: 'Open', exact: true }).click();
    await page.getByRole('menuitem', { name: /GitHub \/ Hugging Face/ }).click();
    await expect(page.locator('.inspect-repoadd')).toBeVisible();
    await page.locator('.inspect-repoadd .inspect-modal-search').first().fill('acme/widget');
    await page.getByRole('button', { name: 'Add repo' }).click();

    // The pinned root appears (label = id@ref); expand it → the tree fetch folds.
    const root = page.locator('.inspect-tree-root', { hasText: 'acme/widget' });
    await expect(root).toBeVisible({ timeout: 15000 });
    await root.locator('.inspect-tree-roottoggle').click();
    const readme = root.locator('.inspect-tree-row.file', { hasText: 'README.md' });
    await expect(readme).toBeVisible({ timeout: 15000 });

    // Opening the blob reads it (2 MB-capped) and dispatches `.md` to the
    // rendered Markdown preview; literal bytes remain available via Source.
    await readme.click();
    await expect(page.locator('.inspect-rich-preview .md')).toContainText('hello from the loopback forge', { timeout: 15000 });
    await expect(page.getByRole('button', { name: 'Source', exact: true })).toBeVisible();
  } finally {
    // Don't leak the override / the pinned root into later runs.
    await page.evaluate(() => {
      localStorage.removeItem('termipod.forge.githubApi');
      localStorage.removeItem('termipod.inspect.roots');
    });
    await new Promise<void>((r) => server.close(() => r()));
  }
});

// ── Shell split pane: one pinned secondary surface (S1) ──────────────────────
// ADR-050 argues the desktop exists for simultaneity, but the shell was a modal
// one-job-at-a-time switch (`plans/desktop-shell-split-pane.md`). Pin the three
// store rules that make the split usable, end-to-end through the real shell:
// a job pins beside the primary from the palette, clicking the already-pinned
// job's rail icon SWAPS the panes instead of duplicating the surface (surfaces
// are singletons over singleton stores), and closing returns to one pane. Each
// pane carries its own ErrorBoundary, so this also covers the seam that makes a
// crash in one pane survivable.
test('shell: a job pins beside the primary, swaps from the rail, and closes (split S1)', async () => {
  await dismissConnectModal();
  const panes = page.locator('.shell-pane');
  const primary = page.locator('.shell-pane[data-pane="primary"]');
  const secondary = page.locator('.shell-pane[data-pane="secondary"]');

  // One pane to start. The split PERSISTS (localStorage), so a mid-test failure
  // in an earlier attempt can leave one open — establish the state, don't assume
  // it, or the retry fails on a stale pane count.
  await page.getByRole('button', { name: 'Fleet', exact: true }).click();
  if ((await panes.count()) > 1) await page.keyboard.press('ControlOrMeta+Backslash');
  await expect(panes).toHaveCount(1);
  await expect(primary.locator('.fleet-toolbar')).toBeVisible();

  // Pin Compare beside it from the palette. The command carries a stable id, so
  // the assertion doesn't depend on the translated label or the list order.
  await page.keyboard.press('ControlOrMeta+K');
  await page.locator('#palette-opt-split-open-compare').click();
  await expect(page.locator('.shell-panes.split')).toHaveCount(1);
  await expect(panes).toHaveCount(2);
  await expect(primary.locator('.fleet-toolbar')).toBeVisible();
  await expect(secondary.locator('.compare-layout')).toBeVisible();
  // "Open beside" focuses what the user asked to look at.
  await expect(secondary).toHaveClass(/\bactive\b/);

  // The rail icon of a job already pinned in the OTHER pane swaps the panes —
  // the same surface must never mount twice.
  await page.getByRole('button', { name: 'Fleet', exact: true }).click();
  await expect(primary.locator('.compare-layout')).toBeVisible();
  await expect(secondary.locator('.fleet-toolbar')).toBeVisible();
  await expect(panes).toHaveCount(2);
  await expect(page.locator('.fleet-toolbar')).toHaveCount(1);

  // Closing collapses to the primary — and leaves no pinned state behind for the
  // next test (the split persists in localStorage).
  await page.keyboard.press('ControlOrMeta+K');
  await page.locator('#palette-opt-split-close').click();
  await expect(page.locator('.shell-panes.split')).toHaveCount(0);
  await expect(panes).toHaveCount(1);
  await expect(primary.locator('.compare-layout')).toBeVisible();
});

// ── Shell split pane S2: rail, shortcuts, divider ────────────────────────────
// S1 shipped the store + render + palette; S2 adds the ergonomics. Drive all four
// entry points through the real shell, because each one is a *writer* of the same
// pane state and the S1 review found a bug in exactly that class (a rule enforced
// at one writer but not another). The `Mod+Shift+\` chord is the interesting one:
// `KeyboardEvent.key` reports the shifted character ('|'), so it only matches
// because the combo resolves punctuation by physical `code`.
test('shell: rail Alt-click pins, Mod+\\ toggles, Mod+Shift+\\ swaps, the divider drags (split S2)', async () => {
  await dismissConnectModal();
  const panes = page.locator('.shell-pane');
  const primary = page.locator('.shell-pane[data-pane="primary"]');
  const secondary = page.locator('.shell-pane[data-pane="secondary"]');
  const compareTab = page.locator('[data-job="compare"]');

  await page.getByRole('button', { name: 'Fleet', exact: true }).click();
  // Same reason as the S1 spec: establish one pane rather than assume it.
  if ((await panes.count()) > 1) await page.keyboard.press('ControlOrMeta+Backslash');
  await expect(panes).toHaveCount(1);

  // Alt-click the rail: pins beside instead of switching — Fleet stays primary.
  await compareTab.click({ modifiers: ['Alt'] });
  await expect(panes).toHaveCount(2);
  await expect(primary.locator('.fleet-toolbar')).toBeVisible();
  await expect(secondary.locator('.compare-layout')).toBeVisible();
  // The pinned job's rail icon carries the corner dot.
  await expect(compareTab).toHaveAttribute('data-beside', '1');
  await expect(compareTab.locator('.activity-tab-dot')).toBeVisible();

  // Mod+\ closes the split; pressing it again reopens the SAME job (the toggle
  // remembers what was pinned — otherwise it is a one-way close).
  await page.keyboard.press('ControlOrMeta+Backslash');
  await expect(panes).toHaveCount(1);
  await expect(compareTab).not.toHaveAttribute('data-beside', '1');
  await page.keyboard.press('ControlOrMeta+Backslash');
  await expect(panes).toHaveCount(2);
  await expect(secondary.locator('.compare-layout')).toBeVisible();

  // Mod+Shift+\ swaps the two panes' contents.
  await page.keyboard.press('ControlOrMeta+Shift+Backslash');
  await expect(primary.locator('.compare-layout')).toBeVisible();
  await expect(secondary.locator('.fleet-toolbar')).toBeVisible();

  // Drag the divider. The ratio persists too, so normalize first by dragging
  // hard LEFT (which lands on the clamp's lower bound whatever it started at),
  // then drag right and assert the primary pane's basis grew.
  // Direct child: the SURFACES inside the panes have their own ResizeHandles
  // (MissionLayout's nav + attention dock), so a descendant selector matches
  // three elements and trips strict mode.
  const handle = page.locator('.shell-panes > .resize-handle');
  const basis = async (): Promise<number> =>
    Number.parseFloat(await primary.evaluate((el) => (el as HTMLElement).style.flexBasis));
  async function dragBy(dx: number): Promise<void> {
    const box = await handle.boundingBox();
    if (box === null) throw new Error('the split divider has no box');
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 + dx, box.y + box.height / 2, { steps: 8 });
    await page.mouse.up();
  }
  await dragBy(-2000); // pin to the lower clamp
  const before = await basis();
  await dragBy(160);
  expect(await basis()).toBeGreaterThan(before);

  // Leave one pane and the default ratio for whatever runs next.
  await page.keyboard.press('ControlOrMeta+Backslash');
  await expect(panes).toHaveCount(1);
});
