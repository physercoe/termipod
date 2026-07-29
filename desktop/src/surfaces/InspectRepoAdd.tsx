import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import type { PinRoot } from './InspectOpen';
import { RepoResolveForm } from './InspectForgePick';

/// Add a **forge** root to the Inspect tree (round-3 T3): paste a GitHub or
/// Hugging Face repo URL / shorthand, resolve its ref to an immutable commit
/// SHA, and pin it. The form itself is the shared [[RepoResolveForm]] (#460 —
/// the compare menu's repo picker resolves the same way, then browses instead
/// of pinning).
export function InspectRepoAddDialog({ onClose, onAdd }: { onClose: () => void; onAdd: (root: PinRoot) => void }): JSX.Element {
  const t = useT();
  return (
    <div className="inspect-modal-backdrop" onClick={onClose}>
      <div className="inspect-modal" role="dialog" aria-label={t('inspect.fromRepo')} onClick={(e) => e.stopPropagation()}>
        <div className="inspect-modal-head">
          <span className="inspect-modal-title">{t('inspect.fromRepo')}</span>
          <span className="spacer" />
          <button className="icon-btn" title={t('inspect.close')} onClick={onClose}>
            <Icon name="close" size={15} />
          </button>
        </div>
        <RepoResolveForm
          submitLabel={t('inspect.repoAdd')}
          busyLabel={t('inspect.repoResolving')}
          onResolved={(forge, repo, id) => onAdd({ source: forge, repo, label: `${id}@${repo.ref}` })}
        />
      </div>
    </div>
  );
}
