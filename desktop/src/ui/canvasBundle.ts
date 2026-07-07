/// AFM-V1 (Artifact File Manifest) parser + canvas inliner — a faithful port of
/// mobile's `lib/services/artifact_manifest/artifact_manifest.dart` and
/// `lib/widgets/artifact_viewers/canvas_viewer.dart` (`inlineCanvasBundle`).
///
/// A `canvas-app` / `code-bundle` artifact is NOT raw HTML: its blob body is a
/// JSON manifest `{version, entry?, files:[{path, content, mime}]}` carried under
/// the vendor mime `application/vnd.termipod.canvas+json` (or `…code+json`). To
/// render a canvas we resolve the HTML entry file and inline its relative
/// `<script src>`, `<link rel=stylesheet href>`, and `<img src>` references from
/// the manifest's other files, producing one self-contained HTML document for a
/// sandboxed iframe. Keep this in lockstep with the Dart original.

export const CANVAS_MIME = 'application/vnd.termipod.canvas+json';
export const CODE_MIME = 'application/vnd.termipod.code+json';

export interface ArtifactFile {
  path: string;
  content: string;
  mime: string;
}
export interface ArtifactFileManifest {
  version: number;
  entry?: string;
  files: ArtifactFile[];
}

const EXT_MIME: Record<string, string> = {
  html: 'text/html',
  htm: 'text/html',
  css: 'text/css',
  svg: 'image/svg+xml',
  js: 'text/javascript',
  mjs: 'text/javascript',
  cjs: 'text/javascript',
  json: 'application/json',
  md: 'text/markdown',
  txt: 'text/plain',
  py: 'text/x-python',
  ts: 'text/typescript',
  tsx: 'text/typescript',
  jsx: 'text/javascript',
  go: 'text/x-go',
  rs: 'text/rust',
  java: 'text/x-java',
  kt: 'text/x-kotlin',
  swift: 'text/x-swift',
  rb: 'text/x-ruby',
  php: 'text/x-php',
  c: 'text/x-c',
  h: 'text/x-c',
  cc: 'text/x-c++',
  cpp: 'text/x-c++',
  hpp: 'text/x-c++',
  sh: 'application/x-sh',
  bash: 'application/x-sh',
  yaml: 'text/yaml',
  yml: 'text/yaml',
  toml: 'text/toml',
  xml: 'text/xml',
  scss: 'text/css',
  tex: 'text/x-tex',
  dart: 'text/x-dart',
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
};

/** IANA mime from a POSIX path's extension, `text/plain` fallback. */
export function mimeForPath(path: string): string {
  const dot = path.lastIndexOf('.');
  if (dot < 0 || dot === path.length - 1) return 'text/plain';
  const ext = path.slice(dot + 1).toLowerCase();
  return EXT_MIME[ext] ?? 'text/plain';
}

/** Parse a decoded JSON value into an AFM-V1 manifest, or null if unrecognised. */
export function parseArtifactFileManifest(decoded: unknown): ArtifactFileManifest | null {
  let version = 1;
  let entry: string | undefined;
  let rawFiles: unknown[] | null = null;

  if (Array.isArray(decoded)) {
    rawFiles = decoded;
  } else if (decoded !== null && typeof decoded === 'object') {
    const o = decoded as Record<string, unknown>;
    if ('version' in o) {
      if (typeof o.version === 'number' && Number.isInteger(o.version)) version = o.version;
      else return null;
    }
    if (version !== 1) return null;
    if (typeof o.entry === 'string') entry = o.entry;
    if (Array.isArray(o.files)) rawFiles = o.files;
    else if (typeof o.path === 'string' && typeof o.content === 'string') rawFiles = [decoded];
    else return null;
  } else {
    return null;
  }

  const files: ArtifactFile[] = [];
  for (const item of rawFiles) {
    if (item === null || typeof item !== 'object') continue;
    const f = item as Record<string, unknown>;
    if (typeof f.path !== 'string' || typeof f.content !== 'string') continue;
    const declared = f.mime;
    const mime = typeof declared === 'string' && declared !== '' ? declared : mimeForPath(f.path);
    files.push({ path: f.path, content: f.content, mime });
  }

  if (files.length === 0) return null;
  return { version, entry, files };
}

/** Resolve the canvas HTML entry: declared `entry` → `index.html` → first .html. */
export function resolveCanvasEntry(manifest: ArtifactFileManifest): ArtifactFile | null {
  if (manifest.entry !== undefined) {
    const declared = manifest.files.find((f) => f.path === manifest.entry);
    if (declared !== undefined) return declared;
  }
  const index = manifest.files.find((f) => f.path === 'index.html');
  if (index !== undefined) return index;
  const html = manifest.files.find((f) => {
    const lower = f.path.toLowerCase();
    return lower.endsWith('.html') || lower.endsWith('.htm');
  });
  return html ?? null;
}

/** Resolve a relative in-bundle URL; reject absolute/scheme/`..` (Q13 rules). */
function resolveManifestPath(url: string, byPath: Map<string, ArtifactFile>): ArtifactFile | null {
  if (url.startsWith('http:') || url.startsWith('https:') || url.startsWith('data:') || url.startsWith('//')) {
    return null;
  }
  if (url.startsWith('/')) return null;
  let path = url;
  if (path.startsWith('./')) path = path.slice(2);
  if (path.includes('..')) return null;
  return byPath.get(path) ?? null;
}

const SCRIPT_RE = /<script\b([^>]*)>\s*<\/script>/gi;
const LINK_RE = /<link\b([^>]*?)\/?>/gi;
const IMG_RE = /<img\b([^>]*?)\/?>/gi;

function extractAttr(attrs: string, name: string): string | null {
  const re = new RegExp(`\\b${name}\\s*=\\s*("([^"]*)"|'([^']*)')`, 'i');
  const m = re.exec(attrs);
  if (m === null) return null;
  return m[2] ?? m[3] ?? null;
}

function stripAttr(attrs: string, name: string): string {
  const re = new RegExp(`\\s*\\b${name}\\s*=\\s*("[^"]*"|'[^']*')`, 'gi');
  return attrs.replace(re, '');
}

function utf8ToB64(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}

/** Build one self-contained HTML document from a canvas manifest. Throws when
 * there is no resolvable HTML entry. */
export function inlineCanvasBundle(manifest: ArtifactFileManifest): string {
  const entry = resolveCanvasEntry(manifest);
  if (entry === null) throw new Error('canvas-app manifest has no HTML entry');
  const byPath = new Map<string, ArtifactFile>(manifest.files.map((f) => [f.path, f]));

  let html = entry.content;
  html = html.replace(SCRIPT_RE, (whole, attrs: string) => {
    const src = extractAttr(attrs, 'src');
    if (src === null) return whole;
    const resolved = resolveManifestPath(src, byPath);
    if (resolved === null) return whole;
    return `<script${stripAttr(attrs, 'src')}>${resolved.content}</script>`;
  });
  html = html.replace(LINK_RE, (whole, attrs: string) => {
    const rel = extractAttr(attrs, 'rel');
    if (rel === null || rel.toLowerCase() !== 'stylesheet') return whole;
    const href = extractAttr(attrs, 'href');
    if (href === null) return whole;
    const resolved = resolveManifestPath(href, byPath);
    if (resolved === null) return whole;
    return `<style>${resolved.content}</style>`;
  });
  html = html.replace(IMG_RE, (whole, attrs: string) => {
    const src = extractAttr(attrs, 'src');
    if (src === null) return whole;
    const resolved = resolveManifestPath(src, byPath);
    if (resolved === null) return whole;
    return `<img src="data:${resolved.mime};base64,${utf8ToB64(resolved.content)}"${stripAttr(attrs, 'src')}>`;
  });
  return html;
}
