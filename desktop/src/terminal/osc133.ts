import type { IDisposable, IMarker, Terminal } from '@xterm/xterm';

/// OSC 133 semantic-prompt integration — the open protocol behind Warp/iTerm2/
/// VS Code "Blocks" (professional-terminal discussion §3.2, → ADR-053). A shell
/// integration snippet (see `shellIntegrationScript`) emits, around each command:
///
///   OSC 133 ; A ST   prompt start
///   OSC 133 ; B ST   command input start (end of prompt)
///   OSC 133 ; C ST   command output start (command is now running)
///   OSC 133 ; D ; <exit> ST   command end, with exit code
///
/// From these markers we reconstruct, per command, its text, output span, exit
/// code and duration — a Block. We stay deliberately at v1: a command *navigator*
/// (jump between prompts, see exit/duration, copy a command's output), not a full
/// re-rendered Warp card stack. xterm markers track their buffer line as it
/// scrolls, and dispose themselves once the line falls out of scrollback.

export interface CommandBlock {
  id: number;
  /** Marker at the prompt line — the jump target. Null once scrolled away. */
  promptMarker: IMarker | null;
  /** Marker at the first output line — start of the copyable output span. */
  outputMarker: IMarker | null;
  /** Best-effort captured command text (between the B and C markers). */
  command: string;
  /** Exit code from `D;<code>`, or null while running / when unreported. */
  exitCode: number | null;
  /** `performance.now()` at output start (C). */
  startedAt: number;
  /** `performance.now()` at command end (D); null while running. */
  endedAt: number | null;
  running: boolean;
}

/// The bash/zsh shell-integration snippet, as a SINGLE line so it can be written
/// straight into an interactive shell (over SSH or a local PTY) with only one
/// line of echo. It wraps the prompt with OSC 133 A/B and emits C before each
/// command and D;$? after. `__ti_active` gates C to fire once per command (bash's
/// DEBUG trap otherwise fires per simple-command). A sentinel makes re-sourcing a
/// no-op. Kept POSIX-ish for bash + zsh; fish/powershell get their own later.
export const shellIntegrationScript =
  `if [ -z "$TERMIPOD_SI" ]; then export TERMIPOD_SI=1; ` +
  `__ti_osc(){ printf '\\033]133;%s\\007' "$1"; }; ` +
  // C once per command (bash's DEBUG trap fires per simple-command).
  `__ti_pre(){ [ -n "$__ti_active" ] && return; __ti_active=1; __ti_osc C; }; ` +
  // D reports the just-finished command's exit; A is emitted by PS1, NOT here
  // (emitting A in both places double-counts prompts). $? must be read first.
  `__ti_post(){ __ti_osc "D;$?"; __ti_active=; }; ` +
  `if [ -n "$ZSH_VERSION" ]; then autoload -Uz add-zsh-hook 2>/dev/null; ` +
  `add-zsh-hook precmd __ti_post 2>/dev/null; add-zsh-hook preexec __ti_pre 2>/dev/null; ` +
  `PS1="%{$(__ti_osc A)%}$PS1%{$(__ti_osc B)%}"; ` +
  `elif [ -n "$BASH_VERSION" ]; then ` +
  `[[ "$PROMPT_COMMAND" != *__ti_post* ]] && PROMPT_COMMAND="__ti_post;$PROMPT_COMMAND"; ` +
  `trap '__ti_pre' DEBUG; PS1="\\[$(__ti_osc A)\\]$PS1\\[$(__ti_osc B)\\]"; fi; fi`;

/// Whether `shellIntegrationScript` can be injected into the given shell. It is a
/// bash/zsh script, so feeding it to cmd.exe / PowerShell just prints parse
/// errors ("`-z` was unexpected at this time.") — a real bug seen on Windows
/// local shells. Match the shell's basename exactly (an `endsWith('sh')` test
/// would wrongly accept powershell/pwsh). When the shell is unknown (e.g. a
/// remote SSH shell, whose kind we can't see), assume POSIX — remote hosts are
/// overwhelmingly Linux and the user drives injection manually there.
export function isPosixShell(shell: string | undefined): boolean {
  if (shell === undefined || shell.trim() === '') return true;
  const base = (shell.replace(/\\/g, '/').split('/').pop() ?? '').toLowerCase().replace(/\.exe$/, '');
  return ['bash', 'zsh', 'sh', 'dash', 'ash', 'ksh'].includes(base);
}

/// Tracks OSC 133 markers on one terminal and surfaces the resulting blocks.
export class ShellIntegration {
  private readonly term: Terminal;
  private readonly onChange: (blocks: CommandBlock[]) => void;
  private readonly blocks: CommandBlock[] = [];
  private readonly disposables: IDisposable[] = [];
  private nextId = 1;
  private current: CommandBlock | null = null;
  private bPos: { x: number; y: number } | null = null;

  constructor(term: Terminal, onChange: (blocks: CommandBlock[]) => void) {
    this.term = term;
    this.onChange = onChange;
    this.disposables.push(term.parser.registerOscHandler(133, (data) => this.handle(data)));
  }

  dispose(): void {
    for (const d of this.disposables) d.dispose();
    for (const b of this.blocks) {
      b.promptMarker?.dispose();
      b.outputMarker?.dispose();
    }
  }

  /** Read a command block's output as text, from its output marker to the next
   * block's prompt (or the buffer end). Used by the navigator's copy action. */
  getOutput(block: CommandBlock): string {
    const buf = this.term.buffer.active;
    const start = block.outputMarker?.line;
    if (start === undefined) return '';
    const idx = this.blocks.indexOf(block);
    const next = idx >= 0 ? this.blocks[idx + 1] : undefined;
    const end = next?.promptMarker?.line ?? buf.length;
    const lines: string[] = [];
    for (let y = start; y < end && y < buf.length; y++) {
      lines.push(buf.getLine(y)?.translateToString(true) ?? '');
    }
    return lines.join('\n').replace(/\n+$/, '');
  }

  private handle(data: string): boolean {
    switch (data[0]) {
      case 'A':
        this.onPromptStart();
        return true;
      case 'B':
        this.bPos = this.cursor();
        return true;
      case 'C':
        this.onOutputStart();
        return true;
      case 'D':
        this.onCommandEnd(data);
        return true;
      default:
        return false;
    }
  }

  private cursor(): { x: number; y: number } {
    const buf = this.term.buffer.active;
    return { x: buf.cursorX, y: buf.baseY + buf.cursorY };
  }

  private onPromptStart(): void {
    // A new prompt implicitly ends any still-"running" command that never sent D
    // (e.g. Ctrl-C at the prompt) — leave its exit code null but stop the clock.
    if (this.current?.running) {
      this.current.running = false;
      this.current.endedAt = performance.now();
    }
    const block: CommandBlock = {
      id: this.nextId++,
      promptMarker: this.term.registerMarker(0) ?? null,
      outputMarker: null,
      command: '',
      exitCode: null,
      startedAt: 0,
      endedAt: null,
      running: false,
    };
    block.promptMarker?.onDispose(() => {
      block.promptMarker = null;
    });
    this.blocks.push(block);
    this.current = block;
    this.bPos = null;
    this.trim();
    this.emit();
  }

  private onOutputStart(): void {
    const block = this.current;
    // First C for this block only: ignore duplicates within a cycle, and a
    // spurious C from a bash PROMPT_COMMAND sub-command (the block already ran).
    if (block === null || block.running || block.outputMarker !== null) return;
    block.command = this.captureCommand();
    block.outputMarker = this.term.registerMarker(0) ?? null;
    block.startedAt = performance.now();
    block.running = true;
    this.emit();
  }

  private onCommandEnd(data: string): void {
    const block = this.current;
    if (block === null || !block.running) return;
    const parts = data.split(';');
    const code = parts.length > 1 ? Number.parseInt(parts[1], 10) : NaN;
    block.exitCode = Number.isNaN(code) ? null : code;
    block.endedAt = performance.now();
    block.running = false;
    this.emit();
  }

  /** Best-effort: the text the user typed, from the B marker to the cursor at C. */
  private captureCommand(): string {
    const b = this.bPos;
    if (b === null) return '';
    try {
      const buf = this.term.buffer.active;
      const end = buf.baseY + buf.cursorY;
      const parts: string[] = [];
      for (let y = b.y; y <= end && y < buf.length; y++) {
        const line = buf.getLine(y);
        if (line === undefined) continue;
        const from = y === b.y ? b.x : 0;
        parts.push(line.translateToString(true, from));
      }
      return parts.join('').trim();
    } catch {
      return '';
    }
  }

  /** Bound the retained block list so long sessions don't grow it unboundedly. */
  private trim(): void {
    const MAX = 200;
    while (this.blocks.length > MAX) {
      const dropped = this.blocks.shift();
      dropped?.promptMarker?.dispose();
      dropped?.outputMarker?.dispose();
    }
  }

  private emit(): void {
    this.onChange(this.blocks.slice());
  }
}
