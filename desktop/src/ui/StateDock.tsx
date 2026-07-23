import { useState } from 'react';
import { Icon, type IconName } from './Icon';
import { useT, type TLookup } from '../i18n';
import { statusGlyph } from './ToolGroupCard';
import { visibleDockChips, type DockCall, type DockKind, type DockTodo, type StateDockModel } from './stateDock';

/// P2 state dock (agent-transcript-redesign §6 P2, kimi-web ChatDock.vue
/// parity): ambient session-state chips above the composer — `Tasks (n)` ·
/// `Sub-agents (n)` · `Todos (done/total)` — that toggle ONE detail panel.
/// State visibility, NOT feed filtering: the model is derived from the full
/// session event list (AgentTranscript), so a lens change never moves the
/// counts, and clicking a chip only opens/closes the panel.
///
/// Chips appear/disappear per the visibility rules in stateDock.ts (Tasks /
/// Sub-agents only while something runs; Todos once any plan exists). A
/// panel whose chip lost visibility closes with its chip. The dock sits
/// outside the virtual list, directly above the composer, so open/close is
/// just a composer-adjacent layout change to the scroller (same as the
/// composer's own attachment chips) — no re-pinning beyond what
/// followOutput/atBottomStateChange already handle.

const KIND_ICON: Record<DockKind, IconName> = {
  tasks: 'terminal',
  subagents: 'git-branch',
  todos: 'list',
};

function chipLabel(kind: DockKind, model: StateDockModel, t: TLookup): string {
  if (kind === 'tasks') return t('tx.dock.tasks').replace('{n}', String(model.shellRunning));
  if (kind === 'subagents') return t('tx.dock.subagents').replace('{n}', String(model.subagentRunning));
  const todos = model.todos ?? { done: 0, total: 0 };
  return t('tx.dock.todos').replace('{done}', String(todos.done)).replace('{total}', String(todos.total));
}

/// Same whitespace-collapsing clamp as EventCard's tool summaries (its
/// `truncate` is module-private; 96 chars matches the group rows).
function truncate(s: string, max = 96): string {
  const one = s.replace(/\s+/g, ' ').trim();
  return one.length > max ? `${one.slice(0, max)}…` : one;
}

/// A todo entry's glyph — the shared status glyphs for done/in-progress
/// (statusGlyph, ToolGroupCard), the plan card's muted square for pending.
function todoGlyph(status: string): { icon: IconName; cls: string } {
  if (status === 'completed' || status === 'done') return statusGlyph('done');
  if (status === 'in_progress') return statusGlyph('running');
  return { icon: 'square', cls: 'tg-status st-pending' };
}

/// Task/sub-agent rows: kind icon + tool name + key argument + status glyph
/// (the ToolGroupCard row idiom, minus the lazy detail).
function CallRows({ kind, calls }: { kind: DockKind; calls: DockCall[] }): JSX.Element {
  return (
    <ul className="sd-rows">
      {calls.map((c) => {
        const glyph = statusGlyph(c.status);
        return (
          <li key={c.id} className="sd-row">
            <Icon name={KIND_ICON[kind]} size={14} className="ev-tool-ico" />
            <span className="sd-row-name">{c.name}</span>
            {c.arg !== undefined && <code className="ev-tool-arg">{truncate(c.arg)}</code>}
            <span className={glyph.cls} aria-hidden="true">
              <Icon name={glyph.icon} size={13} />
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/// Todo rows (kimi-web TodoCard): shared glyph + content; completed reads
/// strikethrough + faint, in-progress medium weight.
function TodoRows({ items }: { items: DockTodo[] }): JSX.Element {
  return (
    <ul className="sd-rows">
      {items.map((todo, i) => {
        const glyph = todoGlyph(todo.status);
        const cls =
          todo.status === 'completed' || todo.status === 'done'
            ? 'sd-row sd-todo done'
            : todo.status === 'in_progress'
              ? 'sd-row sd-todo doing'
              : 'sd-row sd-todo';
        return (
          <li key={i} className={cls}>
            <span className={glyph.cls} aria-hidden="true">
              <Icon name={glyph.icon} size={13} />
            </span>
            <span className="sd-todo-text">{todo.content}</span>
          </li>
        );
      })}
    </ul>
  );
}

export function StateDock({ model }: { model: StateDockModel }): JSX.Element | null {
  const t = useT();
  // One panel at a time; re-tapping the open chip (or the close control)
  // dismisses it.
  const [open, setOpen] = useState<DockKind | null>(null);
  const kinds = visibleDockChips(model);
  // No visible chips → no dock at all. A stale `open` is harmless: if the
  // chip reappears, its panel simply reopens (the chip reads as a toggle).
  if (kinds.length === 0) return null;
  // A panel whose chip lost visibility (e.g. the last running task finished)
  // closes with its chip.
  const openKind = open !== null && kinds.includes(open) ? open : null;
  return (
    <div className="sd">
      {openKind !== null && (
        <div className="sd-panel" role="region" aria-label={chipLabel(openKind, model, t)}>
          <div className="sd-panel-head">
            <Icon name={KIND_ICON[openKind]} size={14} />
            <span className="sd-panel-title">{chipLabel(openKind, model, t)}</span>
            <span className="spacer" />
            <button
              type="button"
              className="sd-close"
              title={t('tx.dock.close')}
              aria-label={t('tx.dock.close')}
              onClick={() => setOpen(null)}
            >
              <Icon name="close" size={13} />
            </button>
          </div>
          {openKind === 'todos' ? (
            model.todos === undefined || model.todos.items.length === 0 ? (
              <div className="sd-empty muted">{t('tx.dock.noTodos')}</div>
            ) : (
              <TodoRows items={model.todos.items} />
            )
          ) : (
            <CallRows kind={openKind} calls={openKind === 'tasks' ? model.shellCalls : model.subagentCalls} />
          )}
        </div>
      )}
      <div className="sd-chips" role="toolbar" aria-label={t('tx.dock.label')}>
        {kinds.map((kind) => (
          <button
            key={kind}
            type="button"
            className={openKind === kind ? 'sd-chip active' : 'sd-chip'}
            aria-expanded={openKind === kind}
            onClick={() => setOpen((prev) => (prev === kind ? null : kind))}
          >
            <Icon name={KIND_ICON[kind]} size={13} />
            {chipLabel(kind, model, t)}
          </button>
        ))}
      </div>
    </div>
  );
}
