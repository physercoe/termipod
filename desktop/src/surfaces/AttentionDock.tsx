import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useAttention } from '../hub/queries';
import { obj, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
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
  const t = useT();
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
      {kind === 'browser_action' && pending !== undefined && (
        <div className="card-detail">
          <code>{str(pending, 'tool') ?? 'browser tool'}</code>
          {str(pending, 'host_name') !== undefined && <span> · {str(pending, 'host_name')}</span>}
          {preview(pending['args']) !== '' && <div className="mono">{preview(pending['args'])}</div>}
        </div>
      )}
      {/* D3 (docs/plans/desktop-ui-context-and-pointing.md §3.3): the gated
          desktop screenshot, and — since coworking A3 — the author_apply edit
          card, which shares the kind. The card describes WHAT was asked for:
          for a capture, which surfaces are on screen; for an edit, which
          document and why. The per-call-only sentence is a claim about the
          card's OFFER, so it renders from the payload (`session_grant`) rather
          than from the kind — an author card that offers a session lease must
          not say no standing grant exists. */}
      {kind === 'desktop_action' && pending !== undefined && (
        <div className="card-detail">
          <code>{str(pending, 'tool') ?? 'desktop tool'}</code>
          {str(pending, 'scope') !== undefined && <span> · {str(pending, 'scope')}</span>}
          {str(pending, 'title') !== undefined && (
            <span>
              {' '}
              · <b>{str(pending, 'title')}</b>
            </span>
          )}
          {preview(pending['surfaces']) !== '' && <div className="mono">{preview(pending['surfaces'])}</div>}
          {str(pending, 'url') !== undefined && <div className="mono">{str(pending, 'url')}</div>}
          {str(pending, 'reason') !== undefined && str(pending, 'reason') !== '' && (
            <div className="muted">{str(pending, 'reason')}</div>
          )}
          {pending['session_grant'] !== true && <div className="muted">{t('att.perCallOnly')}</div>}
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
            placeholder={t('att.replyPlaceholder')}
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
            {t('att.answer')}
          </button>
          <button disabled={busy} onClick={() => void decide('reject')}>
            {t('att.dismiss')}
          </button>
        </div>
      ) : kind === 'browser_action' ? (
        // W3 browser bridge: approve routes this one call; "session" also
        // records a hub-side grant so the agent's later action calls on this
        // desktop skip the card (revocable under Settings → Remote driving).
        <div className="card-actions">
          <button className="primary" disabled={busy} onClick={() => void decide('approve')}>
            {t('att.allowOnce')}
          </button>
          <button disabled={busy} onClick={() => void decide('approve', { option_id: 'session' })}>
            {t('att.allowSession')}
          </button>
          <button disabled={busy} onClick={() => void decide('reject')}>
            {t('att.reject')}
          </button>
        </div>
      ) : kind === 'desktop_action' ? (
        // A screenshot card offers NO "Allow session" — a screenshot never
        // gets a standing grant (plan §3.3, ADR-062 D-4). An author_apply card
        // DOES: "allow this document for this session" (coworking A3), and the
        // payload's `session_grant` flag is what says which card this is — the
        // desktop holds the lease per (agent, document), so the option rides
        // `option_id: 'session'` back through the decision row it polls
        // (readCardDecision). The tool parks on this decision either way and
        // fails closed if it does not arrive.
        <div className="card-actions">
          <button className="primary" disabled={busy} onClick={() => void decide('approve')}>
            {t('att.allowOnce')}
          </button>
          {pending !== undefined && pending['session_grant'] === true && (
            <button disabled={busy} onClick={() => void decide('approve', { option_id: 'session' })}>
              {str(pending, 'session_grant_scope') === 'document' ? t('att.allowDocSession') : t('att.allowSession')}
            </button>
          )}
          <button disabled={busy} onClick={() => void decide('reject')}>
            {t('att.reject')}
          </button>
        </div>
      ) : (
        <div className="card-actions">
          <button className="primary" disabled={busy} onClick={() => void decide('approve')}>
            {t('att.approve')}
          </button>
          <button disabled={busy} onClick={() => void decide('reject')}>
            {t('att.reject')}
          </button>
          {changeKind !== undefined && (
            <button
              disabled={busy}
              title={t('attention.principalOverride')}
              onClick={() => void decide('override', { override: true })}
            >
              {t('att.override')}
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
  const t = useT();
  const query = useAttention();
  const items = (query.data ?? []).filter((a) => (str(a, 'status') ?? 'open') === 'open');

  if (query.isLoading) return <div className="region-pad muted">{t('att.loading')}</div>;
  if (query.isError) return <div className="region-pad error">{msg(query.error)}</div>;
  if (items.length === 0) return <div className="region-pad muted">{t('att.empty')}</div>;

  return (
    <div className="dock-list">
      {items.map((item) => (
        <AttentionCard key={str(item, 'id')} item={item} />
      ))}
    </div>
  );
}
