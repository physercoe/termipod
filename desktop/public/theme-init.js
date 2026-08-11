// Set the theme before first paint. CSS defaults to dark, so a
// light-preference user would otherwise get a dark flash (FOUC) until the
// React effect runs. Mirrors state/theme.ts (key + resolution).
//
// This lives in an external file, not an inline <script> in index.html: both
// shells' CSP (`script-src 'self' …`, no hash/'unsafe-inline') forbids inline
// scripts, so the inline version was blocked and never ran — every launch
// flashed dark for light-theme users (#352). Served from the app origin it is
// covered by `script-src 'self'` on both shells with no CSP relaxation.
(function () {
  try {
    var p = localStorage.getItem('termipod.theme') || 'dark';
    var t =
      p === 'system'
        ? window.matchMedia('(prefers-color-scheme: dark)').matches
          ? 'dark'
          : 'light'
        : p;
    document.documentElement.dataset.theme = t;

    // Apply typography before first paint too. state/appearance.ts owns the
    // same keys, narrowing and live updates once React mounts.
    var font = localStorage.getItem('termipod.uiFont') || 'inter';
    var stacks = {
      inter: '"Inter Variable", "Inter", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
      system: 'system-ui, -apple-system, "Segoe UI", Roboto, "Noto Sans", sans-serif',
      mono: '"JetBrains Mono Variable", ui-monospace, "SF Mono", "Cascadia Code", Menlo, monospace',
    };
    if (!Object.prototype.hasOwnProperty.call(stacks, font)) font = 'inter';
    var scale = Number.parseFloat(localStorage.getItem('termipod.uiFontScale') || '1');
    if (!Number.isFinite(scale)) scale = 1;
    scale = Math.min(1.3, Math.max(0.8, scale));
    scale = Math.round((scale - 0.8) / 0.05) * 0.05 + 0.8;
    var sizes = {
      '--font-size-label': 11,
      '--font-size-caption': 12,
      '--font-size-body-small': 13,
      '--font-size-body': 14,
      '--font-size-subtitle': 16,
      '--font-size-title': 18,
      '--font-size-title-large': 20,
    };
    var root = document.documentElement;
    root.dataset.uiFont = font;
    root.dataset.uiFontScale = String(Math.round(scale * 100));
    root.style.setProperty('--sans', stacks[font]);
    Object.keys(sizes).forEach(function (token) {
      root.style.setProperty(token, String(sizes[token] * scale) + 'px');
    });
  } catch (e) {
    /* ignore — falls back to the CSS default (dark) */
  }
})();
