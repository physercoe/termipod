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
  } catch (e) {
    /* ignore — falls back to the CSS default (dark) */
  }
})();
