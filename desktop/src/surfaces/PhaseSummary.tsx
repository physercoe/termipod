import { useQuery } from '@tanstack/react-query';
import { str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Modal } from '../ui/Modal';

/// Phase summary overlay (parity — mobile PhaseSummaryScreen). Opened by tapping
/// any phase pip on the project Overview — current OR past/future. The project
/// overview only carries the *active* phase's deliverables, so this fetches the
/// selected phase's deliverables directly (`GET /projects/{id}/deliverables?
/// phase=`). Selecting one hands off to the existing DeliverableDetail.
export function PhaseSummary({
  projectId,
  phase,
  isCurrent,
  onOpenDeliverable,
  onClose,
}: {
  projectId: string;
  phase: string;
  isCurrent: boolean;
  onOpenDeliverable: (id: string) => void;
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);

  const q = useQuery({
    queryKey: ['deliverables', projectId, phase],
    enabled: client !== null && phase !== '',
    queryFn: () => client!.listDeliverables(projectId, { phase }),
  });
  const items: Entity[] = q.data ?? [];

  return (
    <Modal onClose={onClose} className="task-detail" ariaLabel={phase}>
        <div className="admin-tabs">
          <strong>{phase}</strong>
          <span className={isCurrent ? 'sev sev-medium' : 'muted small'}>
            {isCurrent ? t('phase.current') : t('phase.summary')}
          </span>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="region-pad scroll">
          {q.isLoading && <div className="muted">{t('common.loading')}</div>}
          {q.isError && <div className="error">{(q.error as Error).message}</div>}
          {!q.isLoading && items.length === 0 && <div className="muted">{t('phase.noDeliverables')}</div>}
          {items.map((d) => {
            const did = str(d, 'id') ?? '';
            const state = str(d, 'ratification_state') ?? '—';
            const ratified = state === 'ratified';
            return (
              <div key={did} className="admin-row">
                <button className="deliv-open" onClick={() => onOpenDeliverable(did)} title={t('deliv.openHint')}>
                  {str(d, 'kind') ?? did}
                </button>
                <span className="spacer" />
                <span className={`sev${ratified ? ' sev-medium' : ''}`}>{state}</span>
              </div>
            );
          })}
        </div>
    </Modal>
  );
}
