import { useState } from 'react';
import { Icon } from './Icon';
import { useT } from '../i18n';
import type { Entity } from '../hub/types';
import { callToolId, toolMeta, ToolCallBody, type FeedEvent } from './EventCard';
import { aggregateToolStatus, toolDiffStats, toolStatusOf, type ToolStatus } from './toolGroups';

/// P1 tool-call group card (agent-transcript-redesign §6 P1, kimi-web
/// ToolGroup.vue parity): a run of ≥2 consecutive tool_call events renders as
/// ONE card. Header: `● N tool calls · <running|error|done>` with the
/// aggregate state (running > error > done) and an error count when any call
/// failed. Rows: tool icon + verb + key argument + diffstat (when the
/// call/update carries ACP diff content) + status glyph, with per-row lazy
/// detail (the existing tool_call card body). Groups are EXPANDED by default
/// and never auto-collapse — the header click is the only toggle, and the
/// collapsed state is owned by the parent (per group instance, keyed by the
/// group's stable row key) so it survives virtual-list recycling. Error rows
/// auto-expand their detail.

function statusGlyph(s: ToolStatus): { icon: 'circle-half' | 'close' | 'check'; cls: string } {
  if (s === 'error') return { icon: 'close', cls: 'tg-status st-error' };
  if (s === 'done') return { icon: 'check', cls: 'tg-status st-done' };
  return { icon: 'circle-half', cls: 'tg-status st-running' };
}

function GroupRow({
  ev,
  result,
  update,
  status,
  detailOpen,
  onToggleDetail,
}: {
  ev: FeedEvent;
  result?: Entity;
  update?: Entity;
  status: ToolStatus;
  detailOpen: boolean;
  onToggleDetail: () => void;
}): JSX.Element {
  const t = useT();
  const name = typeof ev.payload['name'] === 'string' ? (ev.payload['name'] as string) : 'tool';
  const meta = toolMeta(name, ev.payload['input']);
  const diff = toolDiffStats(ev.payload, update);
  const glyph = statusGlyph(status);
  return (
    <div className="tg-row">
      <button
        type="button"
        className="tg-row-head"
        aria-expanded={detailOpen}
        onClick={onToggleDetail}
      >
        <Icon name={meta.icon} size={14} className="ev-tool-ico" />
        <span className="ev-tool-verb">{meta.verbKey !== undefined ? t(meta.verbKey) : meta.verb}</span>
        {meta.arg !== undefined && <code className="ev-tool-arg">{meta.arg}</code>}
        {diff !== undefined && (
          <span className="tg-diff" aria-hidden="true">
            <span className="d-add">+{diff.added}</span> <span className="d-del">−{diff.removed}</span>
          </span>
        )}
        <span className={glyph.cls} aria-hidden="true">
          <Icon name={glyph.icon} size={13} />
        </span>
      </button>
      {detailOpen && (
        <div className="tg-detail">
          <ToolCallBody p={ev.payload} result={result} />
        </div>
      )}
    </div>
  );
}

export function ToolGroupCard({
  events,
  resultById,
  updateById,
  collapsed,
  onToggle,
}: {
  events: FeedEvent[];
  resultById: Map<string, Entity>;
  updateById: Map<string, Entity>;
  collapsed: boolean;
  onToggle: () => void;
}): JSX.Element {
  const t = useT();
  const statuses = events.map((ev) => toolStatusOf(ev, resultById, updateById));
  const aggregate = aggregateToolStatus(statuses);
  const errorCount = statuses.filter((s) => s === 'error').length;
  // Per-row lazy detail, keyed by the row's event id. Error rows start open
  // (kimi-web: error rows auto-expand); everything else opens on row click.
  // Local state is fine here — a recycled row re-derives the error auto-open
  // on remount, and the GROUP's collapsed state (the one that must persist)
  // is owned by the parent.
  const [openRows, setOpenRows] = useState<ReadonlySet<string>>(
    () => new Set(events.filter((_, i) => statuses[i] === 'error').map((ev) => ev.id)),
  );
  function toggleRow(id: string): void {
    setOpenRows((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }
  const dotCls = aggregate === 'running' ? 'dot running' : aggregate === 'error' ? 'dot stopped' : 'dot muted';
  const stateKey =
    aggregate === 'running' ? 'tx.toolState.running' : aggregate === 'error' ? 'tx.toolState.error' : 'tx.toolState.done';
  return (
    <div className={aggregate === 'error' ? 'tg tg--error' : 'tg'}>
      <button
        type="button"
        className="tg-head"
        aria-expanded={!collapsed}
        title={t('tx.toolGroupToggle')}
        onClick={onToggle}
      >
        <span className={dotCls} aria-hidden="true" />
        <span className="tg-count">{t.plural('tx.toolCalls', events.length)}</span>
        <span className={`tg-state ${aggregate}`}>{t(stateKey)}</span>
        {errorCount > 0 && <span className="tg-errors">{t.plural('tx.toolErrors', errorCount)}</span>}
        <span className="spacer" />
        <Icon name={collapsed ? 'chevron-down' : 'chevron-up'} size={14} className="tg-chevron" />
      </button>
      {!collapsed && (
        <div className="tg-rows">
          {events.map((ev, i) => {
            const id = callToolId(ev.payload);
            const update = id !== undefined ? updateById.get(id) : undefined;
            return (
              <GroupRow
                key={ev.id}
                ev={ev}
                result={id !== undefined ? resultById.get(id) : undefined}
                update={update}
                status={statuses[i]}
                detailOpen={openRows.has(ev.id)}
                onToggleDetail={() => toggleRow(ev.id)}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}
