import { useQuery } from '@tanstack/react-query';
import { useHubAction } from '../hub/action';
import { num, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Modal } from '../ui/Modal';

/// Plan detail + editor (parity — mobile plan_viewer_screen). Edits the plan's
/// status (draft|ready|running|completed|failed|cancelled) and each step's status
/// (pending|running|completed|failed|blocked|skipped) via PATCH. The mobile
/// viewer only mutates status transitions, so we mirror that (spec_json is shown
/// read-only). Backed by GET/PATCH …/plans/{id} and …/plans/{id}/steps/{step}.
const PLAN_STATUSES = ['draft', 'ready', 'running', 'completed', 'failed', 'cancelled'];
const STEP_STATUSES = ['pending', 'running', 'completed', 'failed', 'blocked', 'skipped'];

function fmtSpec(v: unknown): string {
  if (v === null || v === undefined || v === '') return '';
  if (typeof v === 'string') return v;
  try {
    return JSON.stringify(v, null, 2) ?? '';
  } catch {
    return String(v);
  }
}

export function PlanDetail({ planId, onClose }: { planId: string; onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const { run, busy, error } = useHubAction();

  const planQ = useQuery({
    queryKey: ['plan', planId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.getPlan(planId),
  });
  const stepsQ = useQuery({
    queryKey: ['plan-steps', planId],
    enabled: client !== null,
    refetchInterval: 15000,
    queryFn: () => client!.listPlanSteps(planId),
  });

  const plan = planQ.data ?? {};
  const status = str(plan, 'status') ?? '—';
  const steps = [...(stepsQ.data ?? [])].sort(
    (a, b) => (num(a, 'phase_idx') ?? 0) - (num(b, 'phase_idx') ?? 0) || (num(a, 'step_idx') ?? 0) - (num(b, 'step_idx') ?? 0),
  );

  const invalidate = [['plan', planId], ['plan-steps', planId], ['plans']];

  function setPlanStatus(next: string): void {
    void run(() => client!.updatePlan(planId, { status: next }), { invalidate });
  }
  function setStepStatus(stepId: string, next: string): void {
    void run(() => client!.updatePlanStep(planId, stepId, { status: next }), { invalidate: [['plan-steps', planId]] });
  }

  return (
    <Modal onClose={onClose} className="sessions-panel" ariaLabel={t('plan.title')}>
        <div className="admin-tabs">
          <strong>{t('plan.title')}</strong>
          <span className={`sev${status === 'completed' ? ' sev-medium' : status === 'failed' ? ' sev-high' : ''}`}>{status}</span>
          <span className="spacer" />
          <label className="inline-select">
            {t('plan.status')}
            <select value={status} disabled={busy} onChange={(e) => setPlanStatus(e.target.value)}>
              {PLAN_STATUSES.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="region-pad scroll">
          {planQ.isLoading && <div className="muted">{t('common.loading')}</div>}
          {error !== null && <div className="error">{error}</div>}
          <div className="muted small mono">{planId}</div>

          <section className="setting-group">
            <h3>{t('plan.steps')}</h3>
            {stepsQ.isLoading && <div className="muted">{t('common.loading')}</div>}
            {!stepsQ.isLoading && steps.length === 0 && <div className="muted">{t('plan.noSteps')}</div>}
            {steps.map((s, i) => {
              const sid = str(s, 'id') ?? String(i);
              const sstatus = str(s, 'status') ?? 'pending';
              return (
                <div key={sid} className="plan-step-row">
                  <span className="pill">
                    {num(s, 'phase_idx') ?? 0}.{num(s, 'step_idx') ?? 0}
                  </span>
                  <div className="plan-step-main">
                    <div>{str(s, 'kind') ?? '—'}</div>
                    {str(s, 'agent_id') !== undefined && <div className="muted small mono">{str(s, 'agent_id')}</div>}
                  </div>
                  <span className="spacer" />
                  <select value={sstatus} disabled={busy} onChange={(e) => setStepStatus(sid, e.target.value)}>
                    {STEP_STATUSES.map((st) => (
                      <option key={st} value={st}>
                        {st}
                      </option>
                    ))}
                  </select>
                </div>
              );
            })}
          </section>

          {fmtSpec((plan as Entity)['spec_json']) !== '' && (
            <section className="setting-group">
              <h3>{t('plan.spec')}</h3>
              <pre className="ev-mono">{fmtSpec((plan as Entity)['spec_json'])}</pre>
            </section>
          )}
        </div>
    </Modal>
  );
}
