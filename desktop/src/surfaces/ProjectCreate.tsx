import { useState } from 'react';
import { useHubAction } from '../hub/action';
import { str } from '../hub/types';
import { useT } from '../i18n';
import { useFocus } from '../state/focus';
import { useSession } from '../state/session';

/// Create a project (parity Phase 4 / F3). Direct POST for the principal
/// (`handleCreateProject`); agents would instead `propose(kind="project.create")`.
/// On success, selects the new project so the board opens.
export function ProjectCreate({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const selectProject = useFocus((s) => s.selectProject);
  const { run, busy, error } = useHubAction();
  const [name, setName] = useState('');
  const [kind, setKind] = useState<'goal' | 'standing'>('goal');
  const [goal, setGoal] = useState('');
  const [configYaml, setConfigYaml] = useState('');

  async function submit(): Promise<void> {
    if (client === null || name.trim() === '') return;
    const created = await run(
      () =>
        client.createProject({
          name: name.trim(),
          kind,
          goal: goal.trim() !== '' ? goal.trim() : undefined,
          config_yaml: configYaml.trim() !== '' ? configYaml : undefined,
        }),
      { invalidate: [['projects']] },
    );
    if (created !== undefined) {
      const id = str(created, 'id');
      if (id !== undefined) selectProject(id);
      onClose();
    }
  }

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="task-detail" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
          <strong>{t('project.new')}</strong>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="task-form">
          <label className="wide">
            {t('project.name')}
            <input value={name} onChange={(e) => setName(e.target.value)} autoFocus />
          </label>
          <label className="wide">
            {t('project.kind')}
            <div className="seg">
              <button className={kind === 'goal' ? 'seg-btn active' : 'seg-btn'} onClick={() => setKind('goal')}>
                {t('project.goalKind')}
              </button>
              <button className={kind === 'standing' ? 'seg-btn active' : 'seg-btn'} onClick={() => setKind('standing')}>
                {t('project.standingKind')}
              </button>
            </div>
          </label>
          <label className="wide">
            {t('project.goal')}
            <textarea value={goal} onChange={(e) => setGoal(e.target.value)} placeholder={t('project.goalPlaceholder')} />
          </label>
          <label className="wide">
            {t('project.configYaml')}
            <textarea
              className="mono"
              value={configYaml}
              spellCheck={false}
              onChange={(e) => setConfigYaml(e.target.value)}
              placeholder={t('project.configPlaceholder')}
            />
          </label>
          {error !== null && <div className="error wide">{error}</div>}
          <div className="wide task-form-actions">
            <button className="primary" disabled={busy || name.trim() === ''} onClick={() => void submit()}>
              {t('project.create')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
