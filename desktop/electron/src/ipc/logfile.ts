/// Log-file command family for the Inspect (J3) log viewer (plan §4 W3).
///
/// Owns the registry of open line indexes and exposes them to the renderer as
/// `log_open` / `log_slice` / `log_search` / `log_stat` / `log_close`. Every read
/// goes through the fd-backed index in `logindex.ts` — the renderer never
/// receives the whole file, only the window it is showing (the IPC discipline the
/// plan's §7 review anchor calls out). An index is closed (and its fd released)
/// when the renderer closes its tab.
import type { Handler } from './dispatch';
import { openIndex, extend, slice, search, lineCount, type LogIndex } from './logindex';

const openLogs = new Map<string, LogIndex>();
let seq = 0;

function get(id: unknown): LogIndex {
  const idx = openLogs.get(String(id ?? ''));
  if (idx === undefined) throw new Error(`log: no such open log '${String(id ?? '')}'`);
  return idx;
}

export const logfileHandlers: Record<string, Handler> = {
  log_open: async (args): Promise<{ id: string; size: number; lines: number }> => {
    const p = String(args.path ?? '');
    if (p === '') throw new Error('log_open: empty path');
    const idx = await openIndex(p);
    seq += 1;
    const id = `log${seq}`;
    openLogs.set(id, idx);
    return { id, size: idx.size, lines: lineCount(idx) };
  },

  log_slice: async (args): Promise<{ from: number; lines: string[] }> => {
    const idx = get(args.id);
    const from = Math.max(0, Math.floor(Number(args.from ?? 0)));
    const count = Math.max(0, Math.floor(Number(args.count ?? 0)));
    return { from, lines: await slice(idx, from, count) };
  },

  log_search: async (args): Promise<{ hits: Array<{ line: number; col: number }>; truncated: boolean }> => {
    const idx = get(args.id);
    const pattern = String(args.pattern ?? '');
    const flags = String(args.flags ?? 'i');
    const max = Math.min(Math.max(1, Math.floor(Number(args.max ?? 5000))), 20000);
    if (pattern === '') return { hits: [], truncated: false };
    return search(idx, pattern, flags, max);
  },

  // Follow-mode tick: re-index bytes appended since the last read.
  log_stat: async (args): Promise<{ size: number; lines: number; grew: boolean }> => {
    const idx = get(args.id);
    const grew = await extend(idx);
    return { size: idx.size, lines: lineCount(idx), grew };
  },

  log_close: async (args): Promise<Record<string, never>> => {
    const id = String(args.id ?? '');
    const idx = openLogs.get(id);
    if (idx !== undefined) {
      openLogs.delete(id);
      await idx.fh.close().catch(() => undefined);
    }
    return {};
  },
};
