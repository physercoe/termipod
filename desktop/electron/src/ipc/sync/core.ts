/// Shared sync decision core (ADR-055 M2.5) — the pure, backend-agnostic logic
/// the three sync backends (folder-WebDAV, Zotero-WebDAV, S3) share. Ported from
/// `foldersync.rs` (`decide_both` / `will_transfer` / `enumerate_local` /
/// `element_blocks` / `days_from_civil`) + `webdav.rs` (`extract_all` / `is_key`)
/// + `s3.rs` (`iso8601_to_ms` / `xml_unescape`).
///
/// This is where the RISK of the sync port lives — the never-delete direction
/// rule, the dependency-free XML scanning, and the date parsing that drives
/// mtime comparison — so it is isolated here with NO HTTP and covered by a
/// fixture test suite (`core.test.ts`). The transports are thin consumers.
///
/// THE NEVER-DELETE INVARIANT (director's choice, foldersync.rs): sync is two-way
/// and additive — a file removed on one side is left intact on the other, so a
/// bad run can never destroy data. `SyncAction` has no delete variant by
/// construction, and a path present on only one side always copies, never
/// removes.
import { readdirSync, statSync } from 'node:fs';
import path from 'node:path';

export const MAX_ENTRIES = 5000; // per side — mirrors workspace_list's cap
export const MAX_DEPTH = 8;
export const MAX_FILE_BYTES = 100 * 1024 * 1024; // don't buffer giant blobs

// Build/VCS/cache dirs — noise in a vault, never meant to sync. Dotfiles (incl.
// `.obsidian/`) are already skipped by the `.`-prefix rule.
export const SKIP_DIRS: readonly string[] = [
  'node_modules', '.git', 'target', 'dist', 'build', '.next', '.venv', 'venv',
  '__pycache__', '.cache', '.idea', '.vscode', '.svn', '.hg',
];

/// The direction picked for a path present on BOTH sides.
export type SyncAction = 'upload' | 'download' | 'skip' | 'conflict';

/// The additive, never-delete rule shared by every backend: equal byte-length ⇒
/// identical (skip, no whole-tree hashing / ping-pong); else newest mtime wins;
/// equal or unknown mtime ⇒ a genuine conflict we never guess. Mirrors
/// foldersync.rs `decide_both`.
export function decideBoth(
  lSize: number,
  lMtime: number | null,
  rSize: number,
  rMtime: number | null,
): SyncAction {
  if (lSize === rSize) return 'skip';
  if (lMtime !== null && rMtime !== null) {
    if (lMtime > rMtime) return 'upload';
    if (rMtime > lMtime) return 'download';
  }
  return 'conflict';
}

/// Whether a (local, remote) pair will actually move bytes — used to pre-count
/// the transfer total for the N/M chip. Each side is `{size, mtime}` or null when
/// absent. Mirrors foldersync.rs `will_transfer`.
export function willTransfer(
  local: { size: number; mtime: number | null } | null,
  remote: { size: number; mtime: number | null } | null,
): boolean {
  if (local !== null && remote === null) return true;
  if (local === null && remote !== null) return remote.size <= MAX_FILE_BYTES;
  if (local !== null && remote !== null) {
    const a = decideBoth(local.size, local.mtime, remote.size, remote.mtime);
    return a === 'upload' || a === 'download';
  }
  return false;
}

// ── local tree enumeration (shared by folder-WebDAV + S3) ────────────────────
export interface LocalFile {
  abs: string;
  size: number;
  mtimeMs: number | null;
}

function fileMtimeMs(abs: string): number | null {
  try {
    return Math.trunc(statSync(abs).mtimeMs);
  } catch {
    return null;
  }
}

/// Walk the workspace tree (depth/entry-capped), keyed by relative POSIX path,
/// skipping hidden entries and SKIP_DIRS. Mirrors foldersync.rs `enumerate_local`
/// / `walk_local`.
export function enumerateLocalTree(root: string): Map<string, LocalFile> {
  const out = new Map<string, LocalFile>();
  walk(root, '', 0, out);
  return out;
}

function walk(dir: string, rel: string, depth: number, out: Map<string, LocalFile>): void {
  if (depth >= MAX_DEPTH || out.size >= MAX_ENTRIES) return;
  let entries: string[];
  try {
    entries = readdirSync(dir);
  } catch {
    return;
  }
  for (const name of entries) {
    if (out.size >= MAX_ENTRIES) break;
    if (name.startsWith('.')) continue;
    const abs = path.join(dir, name);
    const childRel = rel === '' ? name : `${rel}/${name}`;
    let isDir: boolean;
    let size = 0;
    try {
      const st = statSync(abs);
      isDir = st.isDirectory();
      size = st.size;
    } catch {
      continue;
    }
    if (isDir) {
      if (SKIP_DIRS.includes(name)) continue;
      walk(abs, childRel, depth + 1, out);
    } else {
      out.set(childRel, { abs, size, mtimeMs: fileMtimeMs(abs) });
    }
  }
}

// ── dependency-free XML scanning (shared by WebDAV + S3) ─────────────────────
/// Split an XML body into the inner content of every top-level `<name>…</name>`
/// element (namespace prefix stripped), delimiter-aware on the tag localname so
/// `<responsedescription>` never matches `response` and `<ContentsFoo>` never
/// matches `Contents`. Mirrors foldersync.rs `element_blocks`.
export function elementBlocks(xml: string, name: string): string[] {
  const out: string[] = [];
  let openStart: number | null = null;
  let i = 0;
  for (;;) {
    const pos = xml.indexOf('<', i);
    if (pos < 0) break;
    const lt = pos;
    const gtRel = xml.indexOf('>', lt + 1);
    if (gtRel < 0) break;
    const tag = xml.slice(lt + 1, gtRel);
    const contentStart = gtRel + 1;
    const closing = tag.startsWith('/');
    const head = closing ? tag.slice(1) : tag;
    if (head.startsWith('?') || head.startsWith('!')) {
      i = contentStart;
      continue;
    }
    const nm = head.split(/[ \t\n\r/]/)[0] ?? '';
    const localname = nm.includes(':') ? nm.slice(nm.lastIndexOf(':') + 1) : nm;
    if (localname.toLowerCase() === name.toLowerCase()) {
      if (closing) {
        if (openStart !== null) {
          out.push(xml.slice(openStart, lt));
          openStart = null;
        }
      } else if (!tag.endsWith('/')) {
        openStart = contentStart;
      }
    }
    i = contentStart;
  }
  return out;
}

/// Inner text of every element whose local name equals `local` (case-insensitive).
/// A tiny XML scan for WebDAV `<D:href>` listings + flat `.prop` files. Mirrors
/// webdav.rs `extract_all`.
export function extractAll(xml: string, local: string): string[] {
  const out: string[] = [];
  let i = 0;
  for (;;) {
    const pos = xml.indexOf('<', i);
    if (pos < 0) break;
    const start = pos;
    const after = xml[start + 1] ?? '';
    if (after === '/' || after === '?' || after === '!') {
      i = start + 1;
      continue;
    }
    const gtRel = xml.indexOf('>', start + 1);
    if (gtRel < 0) break;
    const tag = xml.slice(start + 1, gtRel);
    const nm = tag.split(/[ \t\n\r]/)[0] ?? '';
    const localname = nm.includes(':') ? nm.slice(nm.lastIndexOf(':') + 1) : nm;
    const contentStart = gtRel + 1;
    if (!tag.endsWith('/') && localname.toLowerCase() === local.toLowerCase()) {
      const ltRel = xml.indexOf('<', contentStart);
      if (ltRel >= 0) out.push(xml.slice(contentStart, ltRel).trim());
    }
    i = contentStart;
  }
  return out;
}

/// Undo the five predefined XML entities (S3 keys arrive XML-escaped). Mirrors
/// s3.rs `xml_unescape`. Order matters: `&amp;` last would double-unescape, so
/// `&amp;` is done first.
export function xmlUnescape(s: string): string {
  return s
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'");
}

/// Minimal `%XX` percent-decoder — enough to turn an encoded href segment back
/// into a filesystem name. Mirrors foldersync.rs `pct_decode`.
export function pctDecode(s: string): string {
  const b = Buffer.from(s, 'latin1');
  const out: number[] = [];
  let i = 0;
  while (i < b.length) {
    if (b[i] === 0x25 /* % */ && i + 2 < b.length) {
      const hi = hexVal(b[i + 1]);
      const lo = hexVal(b[i + 2]);
      if (hi >= 0 && lo >= 0) {
        out.push(hi * 16 + lo);
        i += 3;
        continue;
      }
    }
    out.push(b[i]);
    i += 1;
  }
  return Buffer.from(out).toString('utf8');
}

function hexVal(byte: number): number {
  const c = String.fromCharCode(byte).toLowerCase();
  if (c >= '0' && c <= '9') return c.charCodeAt(0) - 48;
  if (c >= 'a' && c <= 'f') return c.charCodeAt(0) - 87;
  return -1;
}

// ── date parsing (drives mtime comparison) ───────────────────────────────────
const MONTHS = ['jan', 'feb', 'mar', 'apr', 'may', 'jun', 'jul', 'aug', 'sep', 'oct', 'nov', 'dec'];

/// Days since the Unix epoch for a civil date (Howard Hinnant). Mirrors
/// foldersync.rs `days_from_civil` — no date library.
export function daysFromCivil(y0: number, m: number, d: number): number {
  const y = m <= 2 ? y0 - 1 : y0;
  const era = Math.floor((y >= 0 ? y : y - 399) / 400);
  const yoe = y - era * 400;
  const doy = Math.floor((153 * (m > 2 ? m - 3 : m + 9) + 2) / 5) + d - 1;
  const doe = yoe * 365 + Math.floor(yoe / 4) - Math.floor(yoe / 100) + doy;
  return era * 146097 + doe - 719468;
}

/// Parse an RFC-1123 date (`Wed, 15 Jul 2026 10:20:30 GMT`, WebDAV
/// `getlastmodified`) to epoch ms; null if it doesn't parse (caller treats an
/// unknown mtime conservatively). Mirrors foldersync.rs `parse_http_date_ms`.
export function parseHttpDateMs(s: string): number | null {
  const toks = s.trim().split(/\s+/);
  if (toks.length < 5) return null;
  const day = toInt(toks[1]);
  const mon = MONTHS.findIndex((m) => toks[2].toLowerCase().startsWith(m)) + 1;
  const year = toInt(toks[3]);
  const hms = toks[4].split(':');
  if (day === null || mon === 0 || year === null || hms.length !== 3) return null;
  const hh = toInt(hms[0]);
  const mm = toInt(hms[1]);
  const ss = toInt(hms[2]);
  if (hh === null || mm === null || ss === null) return null;
  return (daysFromCivil(year, mon, day) * 86400 + hh * 3600 + mm * 60 + ss) * 1000;
}

/// Parse an S3 `LastModified` (`2026-07-15T09:43:10.000Z`) to epoch ms; null on a
/// malformed value. Mirrors s3.rs `iso8601_to_ms`.
export function iso8601ToMs(s: string): number | null {
  const tIdx = s.indexOf('T');
  if (tIdx < 0) return null;
  const date = s.slice(0, tIdx);
  const time = s.slice(tIdx + 1);
  const d = date.split('-');
  if (d.length !== 3) return null;
  const y = toInt(d[0]);
  const mon = toInt(d[1]);
  const day = toInt(d[2]);
  if (y === null || mon === null || day === null) return null;
  if (time.length < 8) return null;
  const hms = time.slice(0, 8).split(':');
  if (hms.length !== 3) return null;
  const hh = toInt(hms[0]);
  const mm = toInt(hms[1]);
  const ss = toInt(hms[2]);
  if (hh === null || mm === null || ss === null) return null;
  return (daysFromCivil(y, mon, day) * 86400 + hh * 3600 + mm * 60 + ss) * 1000;
}

/// Strict non-negative integer parse (rejects the `NaN`/trailing-garbage that
/// `Number.parseInt` would tolerate), matching Rust's `str::parse::<i64>()`.
function toInt(s: string): number | null {
  if (!/^\d+$/.test(s)) return null;
  const n = Number.parseInt(s, 10);
  return Number.isFinite(n) ? n : null;
}

/// A Zotero attachment key is exactly 8 alphanumeric chars. Mirrors webdav.rs
/// `is_key`.
export function isKey(k: string): boolean {
  return k.length === 8 && /^[A-Za-z0-9]{8}$/.test(k);
}
