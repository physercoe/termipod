export type FileSortKey = 'name' | 'size' | 'modified' | 'permissions';
export type SortDirection = 'asc' | 'desc';

export interface FileSort {
  key: FileSortKey;
  direction: SortDirection;
}

export interface FileListMetadata {
  name: string;
  is_dir: boolean;
  size: number;
  modified_ms: number | null;
  mode: number | null;
}

const NAME_COLLATOR = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

export function nextFileSort(current: FileSort, key: FileSortKey): FileSort {
  if (current.key !== key) return { key, direction: key === 'modified' || key === 'size' ? 'desc' : 'asc' };
  return { key, direction: current.direction === 'asc' ? 'desc' : 'asc' };
}

function compareNullableNumber(a: number | null, b: number | null, direction: SortDirection): number {
  // Missing metadata stays at the bottom in either direction.
  if (a === null || b === null) {
    if (a === b) return 0;
    return a === null ? 1 : -1;
  }
  const delta = a - b;
  return direction === 'asc' ? delta : -delta;
}

/** Keep folders grouped first, then apply the pane's selected ordering. */
export function sortFileEntries<T extends FileListMetadata>(entries: readonly T[], sort: FileSort): T[] {
  return entries.slice().sort((a, b) => {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
    if (sort.key === 'size') {
      const bySize = compareNullableNumber(a.size, b.size, sort.direction);
      if (bySize !== 0) return bySize;
      return NAME_COLLATOR.compare(a.name, b.name);
    }
    if (sort.key === 'modified') {
      const byModified = compareNullableNumber(a.modified_ms, b.modified_ms, sort.direction);
      if (byModified !== 0) return byModified;
      return NAME_COLLATOR.compare(a.name, b.name);
    }
    if (sort.key === 'permissions') {
      const byMode = compareNullableNumber(a.mode, b.mode, sort.direction);
      if (byMode !== 0) return byMode;
      return NAME_COLLATOR.compare(a.name, b.name);
    }
    const byName = NAME_COLLATOR.compare(a.name, b.name);
    if (byName !== 0) return sort.direction === 'asc' ? byName : -byName;
    return a.name.localeCompare(b.name);
  });
}

/** Compact local-time value suitable for a file-browser column. */
export function formatModifiedTime(ms: number | null): string {
  if (ms === null || !Number.isFinite(ms)) return '—';
  const date = new Date(ms);
  if (Number.isNaN(date.getTime())) return '—';
  const pad = (value: number): string => String(value).padStart(2, '0');
  return `${String(date.getFullYear())}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** Standard Unix file-mode notation, including setuid/setgid/sticky bits. */
export function formatPermissions(mode: number | null, isDir: boolean): string {
  if (mode === null || !Number.isInteger(mode)) return '—';
  const type = (() => {
    switch (mode & 0o170000) {
      case 0o040000: return 'd';
      case 0o120000: return 'l';
      case 0o060000: return 'b';
      case 0o020000: return 'c';
      case 0o010000: return 'p';
      case 0o140000: return 's';
      case 0o100000: return '-';
      default: return isDir ? 'd' : '-';
    }
  })();
  const bit = (mask: number, char: string): string => ((mode & mask) !== 0 ? char : '-');
  const specialExec = (execMask: number, specialMask: number, on: string, off: string): string => {
    const exec = (mode & execMask) !== 0;
    return (mode & specialMask) !== 0 ? (exec ? on : off) : exec ? 'x' : '-';
  };
  return `${type}${bit(0o400, 'r')}${bit(0o200, 'w')}${specialExec(0o100, 0o4000, 's', 'S')}`
    + `${bit(0o040, 'r')}${bit(0o020, 'w')}${specialExec(0o010, 0o2000, 's', 'S')}`
    + `${bit(0o004, 'r')}${bit(0o002, 'w')}${specialExec(0o001, 0o1000, 't', 'T')}`;
}
