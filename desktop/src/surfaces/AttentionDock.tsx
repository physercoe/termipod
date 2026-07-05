import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useAttention } from '../hub/queries';
import { obj, str, type Entity } from '../hub/types';
import { useSession } from '../state/session';

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function preview(value: unknown, max = 160): string {
  if (value === undefined || value === null) return '';
  const s = typeof value === 'string' ? value : JSON.stringify(value);
  return s.length > max ? `${s.slice(0, max)}…` : s;
}

/// One approval card. Renders by kind (ProposeCardRouter parity — permission
/// prompts, propose approvals, help requests, generic) and drives
/// `POST /attention/{id}/decide`. Decisions are approve | reject | override
/// (override = principal path, ADR-030 W9); help_request approvals carry `body`.
function AttentionCard({ item }: { item: Entity }): JSX.Element {
  const client = useSession((s) => s.client);
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [reply, setReply] = useState('');
  const [error, setError] = useState<string | null>(null);

  const id = str(item, 'id') ?? '';
  const kind = str(item, 'kind') ?? 'attention';
  const changeKind = str(item, 'change_kind');
  const severity = str(item, 'severity');
  const actor = str(item, 'actor_handle');
  const project = str(item, 'project_id');
  const summary = str(item, 'summary') ?? '';
  const pending = obj(item, 'pending_payload');

  async function decide(decision: string, extra: Record<string, unknown> = {}): Promise<void> {
    if (client === null) return;
    setBusy(true);
    setError(null);
    try {
      await client.decideAttention(id, decision, extra);
      await qc.invalidateQueries({ queryKey: ['attention'] });
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  }

  const headingKind = changeKind ?? kind;

  return (
    <div className="card">
      <div className="card-head">
        <span className="card-kind">{headingKind}</span>
        {severity !== undefined && severity !== '' && (
          <span className={`sev sev-${severity}`}>{severity}</span>
        )}
      </div>
      <div className="card-summary">{summary}</div>

      {kind === 'permission_prompt' && pending !== undefined && (
        <div className="card-detail">
          <code>{str(pending, 'tool_name') ?? 'tool'}</code>
          {preview(pending['input']) !== '' && <div className="mono">{preview(pending['input'])}</div>}
        </div>
      )}
      {changeKind !== undefined && (
        <div className="card-detail mono">{preview(item['change_spec'] ?? item['target_ref'])}</div>
      )}

      <div className="card-meta">
        {actor !== undefined && <span>{actor}</span>}
        {project !== undefined && <span>· {project}</span>}
      </div>

      {error !== null && <div className="error">{error}</div>}

      {kind === 'help_request' ? (
        <div className="card-actions">
          <input
            value={reply}
            placeholder="Reply…"
            onChange={(e) => setReply(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && reply.trim() !== '') void decide('approve', { body: reply });
            }}
          />
          <button
            className="primary"
            disabled={busy || reply.trim() === ''}
            onClick={() => void decide('approve', { body: reply })}
          >
            Answer
          </button>
          <button disabled={busy} onClick={() => void decide('reject')}>
            Dismiss
          </button>
        </div>
      ) : (
        <div className="card-actions">
          <button className="primary" disabled={busy} onClick={() => void decide('approve')}>
            Approve
          </button>
          <button disabled={busy} onClick={() => void decide('reject')}>
            Reject
          </button>
          {changeKind !== undefined && (
            <button
              disabled={busy}
              title="Principal override (ADR-030 W9)"
              onClick={() => void decide('override', { override: true })}
            >
              Override
            </button>
          )}
        </div>
      )}
    </div>
  );
}

/// The always-visible approvals dock (plan §4) — governance is the moat, so it
/// never leaves the screen. Shows open attention items as per-kind cards.
export function AttentionDock(): JSX.Element {
  const query = useAttention();
  const items = (query.data ?? []).filter((a) => (str(a, 'status') ?? 'open') === 'open');

  if (query.isLoading) return <div className="region-pad muted">Loading approvals…</div>;
  if (query.isError) return <div className="region-pad error">{msg(query.error)}</div>;
  if (items.length === 0) return <div className="region-pad muted">Nothing needs you.</div>;

  return (
    <div className="dock-list">
      {items.map((item) => (
        <AttentionCard key={str(item, 'id')} item={item} />
      ))}
    </div>
  );
}
