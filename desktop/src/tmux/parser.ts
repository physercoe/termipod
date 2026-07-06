import { SEP } from './commands';

/// tmux output parser (parity — mobile tmux_parser.dart, focused subset). Splits
/// the `|||`-delimited `-F` output of the list-* commands into typed rows.

export interface TmuxSession {
  name: string;
  windows: number;
  attached: boolean;
  created: string;
}
export interface TmuxWindow {
  index: number;
  name: string;
  panes: number;
  active: boolean;
}
export interface TmuxPane {
  index: number;
  command: string;
  title: string;
  active: boolean;
  width: number;
  height: number;
}

function lines(out: string): string[] {
  return out
    .split('\n')
    .map((l) => l.replace(/\r$/, ''))
    .filter((l) => l.trim() !== '');
}

export function parseSessions(out: string): TmuxSession[] {
  return lines(out).map((l) => {
    const f = l.split(SEP);
    return { name: f[0] ?? '', windows: Number(f[1] ?? 0), attached: f[2] === '1', created: f[3] ?? '' };
  });
}

export function parseWindows(out: string): TmuxWindow[] {
  return lines(out).map((l) => {
    const f = l.split(SEP);
    return { index: Number(f[0] ?? 0), name: f[1] ?? '', panes: Number(f[2] ?? 0), active: f[3] === '1' };
  });
}

export function parsePanes(out: string): TmuxPane[] {
  return lines(out).map((l) => {
    const f = l.split(SEP);
    return {
      index: Number(f[0] ?? 0),
      command: f[1] ?? '',
      title: f[2] ?? '',
      active: f[3] === '1',
      width: Number(f[4] ?? 0),
      height: Number(f[5] ?? 0),
    };
  });
}
