import { useState } from 'react';
import { useT } from '../i18n';
import { useJsonDraft } from '../state/draft';
import { ConfirmButton } from '../ui/ConfirmButton';
import { Markdown } from '../ui/Markdown';
import { HeaderPaneToggle, WorkbenchSurface } from '../ui/WorkbenchSurface';

interface Record {
  id: string;
  title: string;
  context: string;
  decision: string;
  consequences: string;
  ts: number;
}

/// J6 — Capture decisions and findings. Research is a narrative of hypotheses and
/// results; this makes decision/finding capture first-class (TermiPod's ADR
/// discipline, in the workbench). Round-1 surface: an ADR-shaped form (title ·
/// context · decision · consequences) that renders to Markdown and appends to a
/// device-local log. The landscape doc's posture is BUILD — records eventually
/// link to the runs that justify them (provenance on the hub `Deliverable`) and
/// share the J2 authoring editor; this is the honest interim capture surface.
function toMarkdown(r: Record): string {
  const parts = [`## ${r.title || '(untitled)'}`];
  if (r.context.trim()) parts.push(`**Context** — ${r.context.trim()}`);
  if (r.decision.trim()) parts.push(`**Decision** — ${r.decision.trim()}`);
  if (r.consequences.trim()) parts.push(`**Consequences** — ${r.consequences.trim()}`);
  return parts.join('\n\n');
}

export function RecordSurface(): JSX.Element {
  const t = useT();
  const [records, setRecords] = useJsonDraft<Record[]>('records', []);
  const [title, setTitle] = useState('');
  const [context, setContext] = useState('');
  const [decision, setDecision] = useState('');
  const [consequences, setConsequences] = useState('');
  const [formOpen, setFormOpen] = useState(() => localStorage.getItem('termipod.record.formOpen') !== '0');

  function save(): void {
    if (title.trim() === '' && decision.trim() === '') return;
    const rec: Record = {
      id: `rec${Date.now()}`,
      title: title.trim(),
      context,
      decision,
      consequences,
      ts: Date.now(),
    };
    setRecords([rec, ...records]);
    setTitle('');
    setContext('');
    setDecision('');
    setConsequences('');
  }

  function remove(id: string): void {
    setRecords(records.filter((r) => r.id !== id));
  }

  function toggleForm(): void {
    setFormOpen((open) => {
      const next = !open;
      try {
        localStorage.setItem('termipod.record.formOpen', next ? '1' : '0');
      } catch {
        /* ignore */
      }
      return next;
    });
  }

  return (
    <WorkbenchSurface
      job="record"
      leadingPaneWidth={formOpen ? 360 : 0}
      leadingActions={
        <HeaderPaneToggle
          side="left"
          open={formOpen}
          showLabel={t('nav.expand')}
          hideLabel={t('nav.collapse')}
          onToggle={toggleForm}
        />
      }
    >
      <div className={`record-layout${formOpen ? '' : ' form-folded'}`}>
        {formOpen && (
          <div className="record-form">
          <label className="wide">
            {t('record.recTitle')}
            <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder={t('record.titlePlaceholder')} />
          </label>
          <label className="wide">
            {t('record.context')}
            <textarea value={context} onChange={(e) => setContext(e.target.value)} rows={2} />
          </label>
          <label className="wide">
            {t('record.decision')}
            <textarea value={decision} onChange={(e) => setDecision(e.target.value)} rows={3} />
          </label>
          <label className="wide">
            {t('record.consequences')}
            <textarea value={consequences} onChange={(e) => setConsequences(e.target.value)} rows={2} />
          </label>
          <div className="wide task-form-actions">
            <button className="primary" disabled={title.trim() === '' && decision.trim() === ''} onClick={save}>
              {t('record.save')}
            </button>
          </div>
          </div>
        )}
        <div className="record-log">
          <div className="notes-head muted small">
            {t('record.log').replace('{n}', String(records.length))}
          </div>
          {records.length === 0 && <div className="muted region-pad">{t('record.empty')}</div>}
          {records.map((r) => (
            <div key={r.id} className="record-card">
              <div className="record-card-body">
                <Markdown text={toMarkdown(r)} />
              </div>
              <div className="record-card-foot">
                <span className="muted small">{new Date(r.ts).toLocaleString()}</span>
                <span className="spacer" />
                <ConfirmButton label={t('record.delete')} danger onConfirm={() => remove(r.id)} />
              </div>
            </div>
          ))}
        </div>
      </div>
    </WorkbenchSurface>
  );
}
