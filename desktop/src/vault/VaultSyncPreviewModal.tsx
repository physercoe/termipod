import { useT } from '../i18n';
import { Modal } from '../ui/Modal';
import type { VaultChange, VaultChangeSection, VaultResolution, VaultResolutions } from './merge';
import type { VaultSyncPreview } from './service';

const SECTION_ORDER: VaultChangeSection[] = ['connections', 'sshKeys', 'items', 'app', 'hostPins'];

function displayTime(value: string | null, unknown: string): string {
  if (value === null) return unknown;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? unknown : new Date(parsed).toLocaleString();
}

export function VaultSyncPreviewModal({
  preview,
  resolutions,
  busy,
  onClose,
  onConfirm,
  onResolutionChange,
}: {
  preview: VaultSyncPreview;
  resolutions: VaultResolutions;
  busy: boolean;
  onClose: () => void;
  onConfirm: () => void;
  onResolutionChange: (key: string, value: VaultResolution) => void;
}): JSX.Element {
  const t = useT();
  const changesBySection = new Map<VaultChangeSection, VaultChange[]>();
  for (const section of SECTION_ORDER) changesBySection.set(section, []);
  for (const change of preview.changes) changesBySection.get(change.section)?.push(change);

  const relation = (change: VaultChange): string => t(`vault.preview.relation.${change.relation}`);
  const selected = (change: VaultChange): VaultResolution =>
    resolutions[change.key] ?? (change.action === 'useRemote' ? 'remote' : 'local');
  const action = (change: VaultChange): string => {
    if (preview.direction === 'up') {
      if (change.relation === 'localOnly') return t('vault.preview.action.addLocalToHub');
      if (change.relation === 'remoteOnly') return t('vault.preview.action.keepRemote');
      return change.action === 'useRemote' ? t('vault.preview.action.useRemote') : t('vault.preview.action.useLocal');
    }
    if (change.relation === 'remoteOnly') return t('vault.preview.action.addRemote');
    return selected(change) === 'remote' ? t('vault.preview.action.useRemote') : t('vault.preview.action.keepLocal');
  };
  const label = (change: VaultChange): string =>
    change.section === 'app' && change.label.startsWith('secret:')
      ? `${t('vault.preview.secret')}: ${change.label.slice('secret:'.length)}`
      : change.label;

  const title = t(preview.direction === 'up' ? 'vault.previewTitleUp' : 'vault.previewTitle');
  const intro = t(preview.direction === 'up' ? 'vault.previewIntroUp' : 'vault.previewIntro');
  const snapshotTime = displayTime(preview.updatedAt, t('vault.previewUnknown'));
  const snapshotDevice = preview.lastDevice !== null && preview.lastDevice !== '' ? ` · ${preview.lastDevice}` : '';
  return (
    <Modal
      onClose={() => { if (!busy) onClose(); }}
      closeOnBackdrop={!busy}
      className="vault-sync-preview"
      ariaLabel={title}
    >
      <div className="vault-sync-preview-head">
        <div>
          <h3>{title}</h3>
          <p className="muted small">{intro}</p>
        </div>
        <button type="button" aria-label={t('vault.previewCancel')} disabled={busy} onClick={onClose}>
          ×
        </button>
      </div>

      <div className="vault-sync-snapshot muted small">
        {t('vault.previewSnapshot')
          .replace('{v}', String(preview.version))
          .replace('{time}', snapshotTime)}
        {snapshotDevice}
      </div>

      <div className="vault-sync-preview-body">
        {preview.changes.length === 0 ? (
          <div className="vault-sync-empty">{t('vault.previewNoChanges')}</div>
        ) : (
          <>
            <div className="vault-sync-count">
              {t('vault.previewChanges').replace('{n}', String(preview.changes.length))}
            </div>
            {SECTION_ORDER.map((section) => {
              const changes = changesBySection.get(section) ?? [];
              if (changes.length === 0) return null;
              return (
                <section className="vault-sync-section" key={section}>
                  <h4>{t(`vault.preview.section.${section}`)} <span>{changes.length}</span></h4>
                  <div className="vault-sync-list">
                    {changes.map((change) => {
                      const needsChoice = change.relation === 'ageUnknown' || change.relation === 'sameTime';
                      return (
                        <div className="vault-sync-row" key={change.key}>
                          <div className="vault-sync-row-main">
                            <span className="vault-sync-label">{label(change)}</span>
                            <span className={`vault-sync-relation ${change.relation}`}>{relation(change)}</span>
                            {needsChoice ? (
                              <select
                                className="vault-sync-choice"
                                aria-label={t('vault.preview.choose').replace('{item}', label(change))}
                                value={selected(change)}
                                disabled={busy}
                                onChange={(event) =>
                                  onResolutionChange(change.key, event.target.value as VaultResolution)
                                }
                              >
                                <option value="local">
                                  {t(preview.direction === 'up'
                                    ? 'vault.preview.action.useLocal'
                                    : 'vault.preview.action.keepLocal')}
                                </option>
                                <option value="remote">{t('vault.preview.action.useRemote')}</option>
                              </select>
                            ) : (
                              <span className="vault-sync-action">{action(change)}</span>
                            )}
                          </div>
                          <div className="vault-sync-times muted">
                            <span>{t('vault.previewLocal')}: {displayTime(change.localUpdatedAt, '—')}</span>
                            <span>{t('vault.previewHub')}: {displayTime(change.remoteUpdatedAt, '—')}</span>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </section>
              );
            })}
          </>
        )}
      </div>

      <div className="vault-sync-preview-actions">
        <button type="button" disabled={busy} onClick={onClose}>{t('vault.previewCancel')}</button>
        <button type="button" className="primary" disabled={busy} onClick={onConfirm}>
          {busy ? t('vault.previewApplying') : t('vault.previewApply')}
        </button>
      </div>
    </Modal>
  );
}
