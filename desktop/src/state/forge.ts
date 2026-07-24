import { invoke } from '../bridge';
import { isShell } from '../platform';
import { listItems, getItemSecret, saveItem } from './vaultItems';
import { encForgePath, parseForgeUrl, type Forge, type ParsedForge } from './forgeUrl';
import type { ForgeRepo } from './inspect';

// Re-exported so existing consumers keep importing forge parsing from './forge'.
export { parseForgeUrl };
export type { Forge, ParsedForge };

/// Forge sources for the Inspect tree (round-3 T3): point Inspect at a **GitHub**
/// repo or a **Hugging Face** model at a ref and read it. A root is an immutable
/// snapshot — the ref is resolved to a commit SHA at add/refresh and every tree +
/// blob read uses the SHA, so a moving branch can't tear a tree mid-read.
///
/// Venue: in the desktop shell every request goes through the proxy-aware
/// `forge_fetch` main-process IPC (a forge must honour the app proxy like every
/// other outbound transport); the plain-browser build fetches directly (the
/// GitHub / HF APIs are CORS-open). Auth is optional — a token in the **vault**
/// (an `api` item whose endpoint is the forge host), never `localStorage`.
///
/// T3a implements GitHub; the HF arms (`fetchTree`/`readForgeBlob` `hf` branch)
/// land in T3b. `parseForgeUrl` already understands both.

const GH_API = 'https://api.github.com';
const HF_HOST = 'https://huggingface.co';

const BLOB_CAP = 2 * 1024 * 1024; // 2 MB — "no uncapped reads" applied to the network
const META_CAP = 12 * 1024 * 1024; // repo metadata + recursive tree JSON
const DISPLAY_TREE_ENTRIES = 50_000; // client-side display ceiling with a banner
const HF_MAX_PAGES = 30; // tree pagination guard (limit 1000/page → ≤30k entries)

// ── fetch venue (shell IPC / browser-direct) ─────────────────────────────────
interface ForgeResponse {
  status: number;
  headers: Record<string, string>;
  body: string;
  tooLarge: boolean;
  size: number;
}

async function forgeFetch(url: string, headers: Record<string, string>, maxBytes: number): Promise<ForgeResponse> {
  if (isShell()) return invoke<ForgeResponse>('forge_fetch', { url, headers, maxBytes });
  // Plain-browser build: the GitHub / HF endpoints are CORS-open. A UA header is
  // browser-managed and can't be overridden here (GitHub accepts a browser UA).
  const res = await fetch(url, { headers });
  const outHeaders: Record<string, string> = {};
  res.headers.forEach((v, k) => (outHeaders[k.toLowerCase()] = v));
  const size = Number(res.headers.get('content-length') ?? '0');
  if (size > maxBytes) return { status: res.status, headers: outHeaders, body: '', tooLarge: true, size };
  const buf = new Uint8Array(await res.arrayBuffer());
  if (buf.byteLength > maxBytes) return { status: res.status, headers: outHeaders, body: '', tooLarge: true, size: buf.byteLength };
  return { status: res.status, headers: outHeaders, body: new TextDecoder('utf-8', { fatal: false }).decode(buf), tooLarge: false, size: buf.byteLength };
}

// ── auth (vault token, optional) ─────────────────────────────────────────────
function forgeHost(forge: Forge): string {
  return forge === 'github' ? 'github.com' : 'huggingface.co';
}
function endpointMatches(endpoint: string, host: string): boolean {
  const e = endpoint.toLowerCase().replace(/^https?:\/\//, '').replace(/\/.*$/, '');
  return e === host || e === `api.${host}`;
}
async function forgeToken(forge: Forge): Promise<string | undefined> {
  const host = forgeHost(forge);
  const item = listItems().find((i) => i.type === 'api' && i.secretSlots.includes('token') && endpointMatches(i.endpoint, host));
  if (item === undefined) return undefined;
  const tok = (await getItemSecret(item.id, 'token')).trim();
  return tok === '' ? undefined : tok;
}
/// Store (or update) a forge token in the vault as an `api` item keyed to the
/// forge host, so every repo of that forge picks it up. Never `localStorage`.
export async function saveForgeToken(forge: Forge, token: string): Promise<void> {
  const host = forgeHost(forge);
  const existing = listItems().find((i) => i.type === 'api' && endpointMatches(i.endpoint, host));
  await saveItem({ id: existing?.id, type: 'api', title: forge === 'github' ? 'GitHub' : 'Hugging Face', endpoint: host, secrets: { token } });
}

async function authHeaders(forge: Forge, extra: Record<string, string> = {}): Promise<Record<string, string>> {
  const headers: Record<string, string> = { ...extra };
  const tok = await forgeToken(forge);
  if (tok !== undefined) headers.Authorization = `Bearer ${tok}`;
  return headers;
}

// ── status → typed error (rate-limit / auth aware) ───────────────────────────
function ensureOk(r: ForgeResponse, forge: Forge): void {
  if (r.status >= 200 && r.status < 300) return;
  if ((r.status === 403 || r.status === 429) && r.headers['x-ratelimit-remaining'] === '0') {
    const reset = Number(r.headers['x-ratelimit-reset'] ?? '0');
    const when = reset > 0 ? new Date(reset * 1000).toLocaleTimeString() : 'soon';
    throw new Error(`${forgeHost(forge)} rate limit reached — resets ${when}. Add a token in the vault (an api item for ${forgeHost(forge)}) to raise the limit.`);
  }
  if (r.status === 404) throw new Error(`not found — a private ${forgeHost(forge)} repo needs a token in the vault`);
  if (r.status === 401) throw new Error(`${forgeHost(forge)} rejected the token in the vault (401)`);
  throw new Error(`${forgeHost(forge)} request failed (HTTP ${r.status})`);
}

// ── ref pinning ──────────────────────────────────────────────────────────────
/// Resolve a (possibly absent) ref to an immutable `ForgeRepo` snapshot.
export async function resolveForgeRepo(forge: Forge, id: string, ref?: string): Promise<ForgeRepo> {
  return forge === 'github' ? githubResolveRef(id, ref) : hfResolveRef(id, ref);
}

async function githubResolveRef(id: string, ref?: string): Promise<ForgeRepo> {
  const headers = await authHeaders('github');
  let branch = ref;
  if (branch === undefined || branch === '') {
    const r = await forgeFetch(`${GH_API}/repos/${id}`, headers, META_CAP);
    ensureOk(r, 'github');
    branch = (JSON.parse(r.body) as { default_branch?: string }).default_branch ?? 'main';
  }
  const c = await forgeFetch(`${GH_API}/repos/${id}/commits/${encodeURIComponent(branch)}`, headers, META_CAP);
  ensureOk(c, 'github');
  const sha = (JSON.parse(c.body) as { sha?: string }).sha ?? '';
  if (sha === '') throw new Error('could not resolve the ref to a commit');
  return { id, ref: branch, sha };
}

async function hfResolveRef(id: string, ref?: string): Promise<ForgeRepo> {
  const headers = await authHeaders('hf');
  const rev = ref !== undefined && ref !== '' ? ref : 'main';
  const r = await forgeFetch(`${HF_HOST}/api/models/${id}/revision/${encodeURIComponent(rev)}`, headers, META_CAP);
  ensureOk(r, 'hf');
  const sha = (JSON.parse(r.body) as { sha?: string }).sha ?? '';
  if (sha === '') throw new Error('could not resolve the revision to a commit');
  return { id, ref: rev, sha };
}

// ── tree ─────────────────────────────────────────────────────────────────────
/// One recursive fetch of a repo's tree at its pinned SHA → the flat entry list
/// the tree pane folds (via `foldHubDocs`). `truncated` when the forge truncated
/// the tree, the display ceiling was hit, pagination was cut, or the JSON body
/// exceeded the byte cap.
export async function fetchForgeTree(forge: Forge, repo: ForgeRepo): Promise<{ entries: Array<{ path: string; is_dir: boolean }>; truncated: boolean }> {
  return forge === 'github' ? githubTree(repo) : hfTree(repo);
}

// Parse a `Link:` header's `rel="next"` target (Hugging Face tree pagination).
function nextLink(link: string | undefined): string | null {
  if (link === undefined) return null;
  for (const part of link.split(',')) {
    const m = part.match(/<([^>]+)>\s*;\s*rel="?next"?/i);
    if (m !== null) return m[1];
  }
  return null;
}

async function hfTree(repo: ForgeRepo): Promise<{ entries: Array<{ path: string; is_dir: boolean }>; truncated: boolean }> {
  const headers = await authHeaders('hf');
  let url: string | null = `${HF_HOST}/api/models/${repo.id}/tree/${repo.sha}?recursive=true`;
  const entries: Array<{ path: string; is_dir: boolean }> = [];
  let truncated = false;
  for (let page = 0; url !== null; page += 1) {
    const r = await forgeFetch(url, headers, META_CAP);
    if (r.tooLarge) {
      truncated = true;
      break;
    }
    ensureOk(r, 'hf');
    const arr = JSON.parse(r.body) as Array<{ path: string; type: string }>;
    for (const e of arr) entries.push({ path: e.path, is_dir: e.type === 'directory' });
    const next = nextLink(r.headers['link']);
    if (next === null) break;
    if (page + 1 >= HF_MAX_PAGES) {
      truncated = true;
      break;
    }
    url = next;
  }
  if (entries.length > DISPLAY_TREE_ENTRIES) return { entries: entries.slice(0, DISPLAY_TREE_ENTRIES), truncated: true };
  return { entries, truncated };
}

async function githubTree(repo: ForgeRepo): Promise<{ entries: Array<{ path: string; is_dir: boolean }>; truncated: boolean }> {
  const headers = await authHeaders('github');
  const r = await forgeFetch(`${GH_API}/repos/${repo.id}/git/trees/${repo.sha}?recursive=1`, headers, META_CAP);
  if (r.tooLarge) return { entries: [], truncated: true };
  ensureOk(r, 'github');
  const j = JSON.parse(r.body) as { tree?: Array<{ path: string; type: string }>; truncated?: boolean };
  // Keep blobs + trees; drop submodule ('commit') entries (no venue to follow).
  const entries = (j.tree ?? []).filter((e) => e.type === 'blob' || e.type === 'tree').map((e) => ({ path: e.path, is_dir: e.type === 'tree' }));
  let truncated = j.truncated === true;
  const capped = entries.length > DISPLAY_TREE_ENTRIES ? entries.slice(0, DISPLAY_TREE_ENTRIES) : entries;
  if (capped.length < entries.length) truncated = true;
  return { entries: capped, truncated };
}

// ── blob ─────────────────────────────────────────────────────────────────────
/// Read one file's text at the pinned SHA. Over the 2 MB cap → a typed "too
/// large" error (the surface renders it as a placard instead of a viewer).
export async function readForgeBlob(repo: ForgeRepo, forge: Forge, path: string): Promise<string> {
  if (forge === 'github') {
    const headers = await authHeaders('github', { Accept: 'application/vnd.github.raw+json' });
    const r = await forgeFetch(`${GH_API}/repos/${repo.id}/contents/${encForgePath(path)}?ref=${repo.sha}`, headers, BLOB_CAP);
    if (r.tooLarge) throw new Error(tooLargeMsg(r.size));
    ensureOk(r, 'github');
    return r.body;
  }
  const headers = await authHeaders('hf');
  const r = await forgeFetch(`${HF_HOST}/${repo.id}/resolve/${repo.sha}/${encForgePath(path)}`, headers, BLOB_CAP);
  if (r.tooLarge) throw new Error(tooLargeMsg(r.size));
  ensureOk(r, 'hf');
  return r.body;
}

function tooLargeMsg(size: number): string {
  return `file too large to inspect (${(size / (1024 * 1024)).toFixed(1)} MB — cap is 2 MB)`;
}
