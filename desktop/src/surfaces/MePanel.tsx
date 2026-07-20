import { useQuery } from '@tanstack/react-query';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Modal } from '../ui/Modal';

/// "Me" surface (parity Phase 4). The hub has no `/me` route and no `/decisions`
/// or `/notes` endpoints (grounded), so this composes what does exist: the
/// team's principals (`GET …/principals`, coalesced by handle) and a decision
/// history derived from resolved attention items (`GET …/attention?status=resolved`,
/// whose `decisions[]` + `resolved_by`/`resolved_at` record who decided what).
/// Read-only.
export function MePanel({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);

  const principalsQ = useQuery({
    queryKey: ['principals', client?.transport.teamId],
    enabled: client !== null,
    queryFn: () => client!.listPrincipals(),
  });
  const decisionsQ = useQuery({
    queryKey: ['attention', client?.transport.teamId, 'resolved'],
    enabled: client !== null,
    refetchInterval: 20000,
    queryFn: () => client!.listAttention('resolved'),
  });

  const principals = principalsQ.data ?? [];
  const decisions = decisionsQ.data ?? [];

  function decisionOf(item: Entity): string {
    const arr = Array.isArray(item['decisions']) ? (item['decisions'] as Entity[]) : [];
    const last = arr[arr.length - 1];
    return (last !== undefined ? str(last, 'decision') : undefined) ?? str(item, 'resolution') ?? '—';
  }

  return (
    <Modal onClose={onClose} className="sessions-panel" ariaLabel={t('me.title')}>
        <div className="admin-tabs">
          <strong>{t('me.title')}</strong>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="region-pad scroll">
          <section className="setting-group">
            <h3>{t('me.principals')}</h3>
            {principalsQ.isLoading && <div className="muted">{t('common.loading')}</div>}
            {principalsQ.isError && <div className="error">{(principalsQ.error as Error).message}</div>}
            {principals.map((p, i) => (
              <div key={str(p, 'handle') ?? String(i)} className="admin-row">
                <span className="mono">
                  {str(p, 'handle') ?? '—'}
                  {p['unnamed'] === true ? ` (${t('me.unnamed')})` : ''}
                </span>
                <span className="spacer" />
                <span className="muted small">
                  {num(p, 'token_count') ?? 0} {t('me.tokens')}
                </span>
              </div>
            ))}
            {!principalsQ.isLoading && principals.length === 0 && <div className="muted">{t('me.noPrincipals')}</div>}
          </section>

          <section className="setting-group">
            <h3>{t('me.decisionHistory')}</h3>
            {decisionsQ.isLoading && <div className="muted">{t('common.loading')}</div>}
            {decisionsQ.isError && <div className="error">{(decisionsQ.error as Error).message}</div>}
            {decisions.map((item, i) => {
              const decision = decisionOf(item);
              return (
                <div key={str(item, 'id') ?? String(i)} className="admin-row">
                  <span className={`sev${decision === 'approve' ? ' sev-medium' : decision === 'reject' ? ' sev-high' : ''}`}>
                    {decision}
                  </span>
                  <span className="me-decision-kind">{str(item, 'change_kind') ?? str(item, 'kind') ?? ''}</span>
                  <span className="spacer" />
                  <span className="muted small">
                    {str(item, 'resolved_by') ?? ''}
                    {str(item, 'resolved_at') !== undefined ? ` · ${str(item, 'resolved_at')}` : ''}
                  </span>
                </div>
              );
            })}
            {!decisionsQ.isLoading && decisions.length === 0 && <div className="muted">{t('me.noDecisions')}</div>}
          </section>
        </div>
    </Modal>
  );
}
