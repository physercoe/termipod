/// tmux command builder (parity — mobile tmux_commands.dart). Each function
/// returns a shell string run over the SSH exec channel (`sshExec`). Output is
/// formatted with a `|||` field separator so `parser.ts` can split it reliably.
/// This is a focused subset — the management ops a control panel needs; the live
/// pane render still flows through the interactive PTY.

const SEP = '|||';

/// Single-quote-escape an argument for /bin/sh (mirrors tmux_commands _escapeArg):
/// close the quote, emit an escaped quote, reopen — so embedded quotes survive.
export function shQuote(arg: string): string {
  return `'${arg.replace(/'/g, "'\\''")}'`;
}

export const TmuxCmd = {
  listSessions(): string {
    const fmt = ['#{session_name}', '#{session_windows}', '#{?session_attached,1,0}', '#{session_created}'].join(SEP);
    return `tmux list-sessions -F ${shQuote(fmt)}`;
  },
  listWindows(session: string): string {
    const fmt = ['#{window_index}', '#{window_name}', '#{window_panes}', '#{?window_active,1,0}'].join(SEP);
    return `tmux list-windows -t ${shQuote(session)} -F ${shQuote(fmt)}`;
  },
  listPanes(session: string, window: number): string {
    const fmt = ['#{pane_index}', '#{pane_current_command}', '#{pane_title}', '#{?pane_active,1,0}', '#{pane_width}', '#{pane_height}'].join(SEP);
    return `tmux list-panes -t ${shQuote(`${session}:${window}`)} -F ${shQuote(fmt)}`;
  },
  newSession(name: string): string {
    return `tmux new-session -d -s ${shQuote(name)}`;
  },
  killSession(name: string): string {
    return `tmux kill-session -t ${shQuote(name)}`;
  },
  renameSession(from: string, to: string): string {
    return `tmux rename-session -t ${shQuote(from)} ${shQuote(to)}`;
  },
  newWindow(session: string): string {
    return `tmux new-window -t ${shQuote(session)}`;
  },
  killWindow(session: string, window: number): string {
    return `tmux kill-window -t ${shQuote(`${session}:${window}`)}`;
  },
  renameWindow(session: string, window: number, name: string): string {
    return `tmux rename-window -t ${shQuote(`${session}:${window}`)} ${shQuote(name)}`;
  },
  splitWindow(session: string, window: number, pane: number, horizontal: boolean): string {
    return `tmux split-window ${horizontal ? '-h' : '-v'} -t ${shQuote(`${session}:${window}.${pane}`)}`;
  },
  killPane(session: string, window: number, pane: number): string {
    return `tmux kill-pane -t ${shQuote(`${session}:${window}.${pane}`)}`;
  },
  sendKeys(session: string, window: number, pane: number, keys: string): string {
    return `tmux send-keys -t ${shQuote(`${session}:${window}.${pane}`)} ${shQuote(keys)} Enter`;
  },
  capturePane(session: string, window: number, pane: number): string {
    return `tmux capture-pane -p -t ${shQuote(`${session}:${window}.${pane}`)}`;
  },
};

export { SEP };
