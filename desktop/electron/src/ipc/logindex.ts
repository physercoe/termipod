/// Main-process line index for the Inspect (J3) log viewer (plan §4 W3).
///
/// Big training/CI logs (100 MB+) must never be slurped into the renderer over
/// IPC — that is exactly what `localfs_read` does and why it is banned for this
/// path (plan §7 IPC discipline). Instead the main process builds a **byte-offset
/// line index** with fd reads (never whole-file), and the renderer pulls only the
/// visible window (`slice`) or async match positions (`search`). This module is
/// the pure core — no `electron` import — so it runs under `node --test` (the
/// `download.ts` precedent). The IPC registry that owns open indexes lives in
/// `logfile.ts`.
import { open, type FileHandle } from 'node:fs/promises';

// 1 MiB fd read window — the index is built by scanning the file in blocks,
// holding at most one block in memory at a time.
const CHUNK = 1 << 20;
const NL = 0x0a; // '\n'

export interface LogIndex {
  path: string;
  fh: FileHandle;
  /// Bytes scanned into the index so far (== file size at the last `extend`).
  size: number;
  /// Byte offset of the start of each line. `starts[0]` is always 0; a file that
  /// ends in a newline leaves a trailing phantom entry `=== size` (dropped by
  /// `lineCount`).
  starts: number[];
}

/// Real line count. A trailing newline yields a phantom final start (`=== size`)
/// that is not a line; an empty file has zero lines.
export function lineCount(idx: LogIndex): number {
  if (idx.size === 0) return 0;
  const n = idx.starts.length;
  return idx.starts[n - 1] === idx.size ? n - 1 : n;
}

/// Scan the bytes appended since the last index build and extend the offset list.
/// Returns whether the file grew (follow-mode's tick). A file that SHRANK (log
/// rotation / truncation) is re-indexed from scratch.
///
/// Re-scanning starts at the last known line start, not at `idx.size`: a partial
/// final line (no trailing newline yet) may now be complete, and the bytes
/// between that line start and the old EOF held no newline by construction, so no
/// offset is ever duplicated.
export async function extend(idx: LogIndex): Promise<boolean> {
  const st = await idx.fh.stat();
  if (st.size === idx.size) return false;
  if (st.size < idx.size) {
    idx.starts = [0];
    idx.size = 0;
  }
  let pos = idx.starts[idx.starts.length - 1];
  const buf = Buffer.allocUnsafe(CHUNK);
  while (pos < st.size) {
    const { bytesRead } = await idx.fh.read(buf, 0, CHUNK, pos);
    if (bytesRead === 0) break;
    for (let i = 0; i < bytesRead; i += 1) {
      if (buf[i] === NL) idx.starts.push(pos + i + 1);
    }
    pos += bytesRead;
  }
  idx.size = st.size;
  return true;
}

/// Open a file and build its initial line index.
export async function openIndex(path: string): Promise<LogIndex> {
  const fh = await open(path, 'r');
  const idx: LogIndex = { path, fh, size: 0, starts: [0] };
  await extend(idx);
  return idx;
}

/// Read lines `[from, from+count)` — one contiguous fd read of exactly the bytes
/// those lines span, split renderer-side. Out-of-range requests clamp to the
/// available line range (an empty array past EOF). CRLF is normalised to LF.
export async function slice(idx: LogIndex, from: number, count: number): Promise<string[]> {
  const total = lineCount(idx);
  const start = Math.max(0, Math.min(from, total));
  const end = Math.max(start, Math.min(from + count, total));
  if (end <= start) return [];
  const byteStart = idx.starts[start];
  const byteEnd = end < idx.starts.length ? idx.starts[end] : idx.size;
  const len = byteEnd - byteStart;
  const buf = Buffer.allocUnsafe(len);
  await idx.fh.read(buf, 0, len, byteStart);
  const lines = buf.toString('utf8').split('\n');
  // The block's last byte is a newline exactly when `end` lands on a line
  // boundary (`byteEnd === starts[end]`, whose predecessor byte is that '\n') —
  // i.e. whenever `end < starts.length`. That newline makes `split` emit a
  // trailing '' to drop. The only case it does NOT hold is `end === total` on a
  // file with no final newline (the last real line ends at EOF, no '\n').
  if (end < idx.starts.length && lines.length > 0 && lines[lines.length - 1] === '') lines.pop();
  return lines.map((l) => (l.endsWith('\r') ? l.slice(0, -1) : l));
}

export interface LogHit {
  line: number;
  col: number;
}

/// Find lines matching `pattern` (one hit per line, at the first match column),
/// scanning the whole file in blocks so memory stays bounded. Stops at `max`
/// hits (`truncated: true`). An invalid regex throws a typed error.
export async function search(idx: LogIndex, pattern: string, flags: string, max: number): Promise<{ hits: LogHit[]; truncated: boolean }> {
  let re: RegExp;
  try {
    re = new RegExp(pattern, flags.replace('g', ''));
  } catch {
    throw new Error('invalid search pattern');
  }
  const total = lineCount(idx);
  const hits: LogHit[] = [];
  const BLOCK = 4000;
  for (let from = 0; from < total; from += BLOCK) {
    const lines = await slice(idx, from, BLOCK);
    for (let i = 0; i < lines.length; i += 1) {
      const m = re.exec(lines[i]);
      re.lastIndex = 0;
      if (m !== null) {
        hits.push({ line: from + i, col: m.index });
        if (hits.length >= max) return { hits, truncated: true };
      }
    }
  }
  return { hits, truncated: false };
}
