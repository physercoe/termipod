/// Log model abstraction for the Inspect (J3) log viewer (plan §4 W3).
///
/// The `LogView` UI reads lines through this interface without caring whether
/// they come from a **main-process line index** (a big local file — the headline
/// case; never slurped over IPC) or an **in-memory string** (a paste scratch, or
/// a bounded remote/hub slice already delivered as text). Two implementations,
/// one UI.
import { invoke } from '../bridge';

export interface LogHit {
  line: number;
  col: number;
}

export interface LogModel {
  /// Current line count (grows under `refresh` when following a live file).
  count(): number;
  /// Lines `[from, from+count)`.
  slice(from: number, count: number): Promise<string[]>;
  /// Match positions across the whole log, capped at `max`.
  search(pattern: string, flags: string, max: number): Promise<{ hits: LogHit[]; truncated: boolean }>;
  /// Re-index appended bytes (follow mode); returns whether the log grew.
  refresh(): Promise<boolean>;
  /// Release any held resource (the fd, for the indexed model).
  close(): void;
}

/// An in-memory log — the whole text is already in the renderer (paste scratch,
/// remote/hub slice). Fine for the bounded sizes those sources produce.
export class MemoryLogModel implements LogModel {
  private readonly lines: string[];

  constructor(text: string) {
    const parts = text.split('\n');
    if (parts.length > 0 && parts[parts.length - 1] === '') parts.pop();
    this.lines = parts.map((l) => (l.endsWith('\r') ? l.slice(0, -1) : l));
  }

  count(): number {
    return this.lines.length;
  }

  async slice(from: number, count: number): Promise<string[]> {
    return this.lines.slice(Math.max(0, from), Math.max(0, from) + Math.max(0, count));
  }

  async search(pattern: string, flags: string, max: number): Promise<{ hits: LogHit[]; truncated: boolean }> {
    let re: RegExp;
    try {
      re = new RegExp(pattern, flags.replace('g', ''));
    } catch {
      throw new Error('invalid search pattern');
    }
    const hits: LogHit[] = [];
    for (let i = 0; i < this.lines.length; i += 1) {
      const m = re.exec(this.lines[i]);
      re.lastIndex = 0;
      if (m !== null) {
        hits.push({ line: i, col: m.index });
        if (hits.length >= max) return { hits, truncated: true };
      }
    }
    return { hits, truncated: false };
  }

  async refresh(): Promise<boolean> {
    return false;
  }

  close(): void {
    /* nothing to release */
  }
}

/// A main-process-indexed log — `LogView` pulls only the visible window and match
/// positions; the bytes stay on disk (the `localfs_read`-avoidance the plan's IPC
/// discipline requires).
export class IndexedLogModel implements LogModel {
  private lineCount: number;

  private constructor(
    private readonly id: string,
    lines: number,
  ) {
    this.lineCount = lines;
  }

  static async open(path: string): Promise<IndexedLogModel> {
    const r = await invoke<{ id: string; size: number; lines: number }>('log_open', { path });
    return new IndexedLogModel(r.id, r.lines);
  }

  count(): number {
    return this.lineCount;
  }

  async slice(from: number, count: number): Promise<string[]> {
    const r = await invoke<{ from: number; lines: string[] }>('log_slice', { id: this.id, from, count });
    return r.lines;
  }

  async search(pattern: string, flags: string, max: number): Promise<{ hits: LogHit[]; truncated: boolean }> {
    return invoke<{ hits: LogHit[]; truncated: boolean }>('log_search', { id: this.id, pattern, flags, max });
  }

  async refresh(): Promise<boolean> {
    const r = await invoke<{ size: number; lines: number; grew: boolean }>('log_stat', { id: this.id });
    this.lineCount = r.lines;
    return r.grew;
  }

  close(): void {
    void invoke('log_close', { id: this.id }).catch(() => undefined);
  }
}
