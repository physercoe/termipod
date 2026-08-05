/// The renderer's Content-Security-Policy — the pure half of `appscheme.ts`.
///
/// Split out for the same reason `media_policy.ts` is split from
/// `mediascheme.ts`: `appscheme.ts` imports `electron`, and `schemes.ts` calls
/// `registerSchemesAsPrivileged` at module load, so anything touching either is
/// unreachable from `node --test`. The CSP is a security control that silently
/// breaks features when it is wrong, which makes it exactly the part worth
/// asserting.
///
/// **Registering a privileged scheme does not grant the document access to it.**
/// `registerSchemesAsPrivileged` plus a `protocol.handle` says the main process
/// will serve those bytes; the CSP separately says what this document may load,
/// and the renderer is judged against the CSP first. A scheme that is served
/// but not listed here fails at the element, before the handler is ever called,
/// which looks like a broken feature rather than a policy refusal.
///
/// That is not hypothetical: `drawio:` was added to `frame-src` when the draw.io
/// scheme landed, the media scheme shipped afterwards without adding itself, and
/// every `termipod-media://` URL in the app was refused until it was.

/// Scheme names, as plain strings. `schemes.ts` holds the same names next to the
/// privilege registration; these are duplicated rather than imported because
/// that file cannot be loaded without electron. `cspAllows` below is what keeps
/// the two honest.
export const CSP_APP_SCHEME = 'app';
export const CSP_DRAWIO_SCHEME = 'drawio';
export const CSP_MEDIA_SCHEME = 'termipod-media';

/// Directives that must admit the media scheme: it serves video, audio, images
/// and PDFs, and nothing else. `script-src` and `connect-src` are deliberately
/// absent — nothing should execute from it or fetch() it.
export const MEDIA_DIRECTIVES = ['img-src', 'media-src', 'frame-src'] as const;

/// Build the policy. Carried over from tauri.conf.json's CSP, with the
/// Electron-shell changes documented in `appscheme.ts`.
export function buildCsp(): string {
  const media = `${CSP_MEDIA_SCHEME}:`;
  return [
    "default-src 'self'",
    // The figure renderers are local, trusted, bundled app code that need JS
    // features `'self'` alone forbids: graphviz (@hpcc-js/wasm) instantiates
    // WebAssembly → `'wasm-unsafe-eval'`; vega compiles expressions via the
    // Function constructor → `'unsafe-eval'`. Without these the webview blocks
    // them and the figure shows the CSP-violation text (the `sha256-…` lines).
    "script-src 'self' 'wasm-unsafe-eval' 'unsafe-eval'",
    "style-src 'self' 'unsafe-inline' blob:",
    `img-src 'self' data: blob: https: ${media}`,
    "font-src 'self' data: blob:",
    `media-src 'self' blob: https: ${media}`,
    "worker-src 'self' blob:",
    "connect-src 'self' https: http: ws: wss: data: blob:",
    `frame-src 'self' https: http: data: blob: ${CSP_DRAWIO_SCHEME}: ${media}`,
    "object-src 'none'",
    "base-uri 'self'",
  ].join('; ');
}

/// Split a policy into `directive → sources`. Exported for the tests: asserting
/// on substrings of the joined string passes for the wrong reasons (`media-src`
/// is a substring of nothing, but `https:` appears in six directives).
export function cspDirectives(csp: string): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const part of csp.split(';')) {
    const [name, ...sources] = part.trim().split(/\s+/).filter((s) => s !== '');
    if (name !== undefined) out[name] = sources;
  }
  return out;
}

/// Whether `directive` admits `source` — falling back to `default-src` the way
/// a browser does for a fetch directive that is absent entirely.
export function cspAllows(csp: string, directive: string, source: string): boolean {
  const d = cspDirectives(csp);
  const sources = d[directive] ?? d['default-src'] ?? [];
  return sources.includes(source);
}
