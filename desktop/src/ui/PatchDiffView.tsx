import { useMemo, useState } from 'react';
import { DiffView, DiffModeEnum } from '@git-diff-view/react';
import '@git-diff-view/react/styles/diff-view.css';
import { useT } from '../i18n';
import { useTheme } from '../state/theme';
import { Icon } from './Icon';
import { splitPatch, type PatchFile, type PatchStatus } from '../state/patch';

/// The Inspect (J3) **patch viewer** (W2, tier 1) — GitHub-style rendering of a
/// `.patch`/`.diff` file or a pasted patch, via `@git-diff-view/react`. Lazy
/// chunk (its highlighter + CSS never touch the boot bundle — plan §7). A
/// multi-file patch is split here (`state/patch.ts`) into per-file `<DiffView>`s
/// since the component renders one file at a time. Add/delete/rename/binary are
/// distinguished; a binary file shows a placard rather than an empty diff.

// Resolve dark/light the same way the rest of the app does (data-theme wins,
// else OS); subscribe to the pref store so a theme flip re-renders.
function useResolvedDark(): boolean {
  const pref = useTheme((s) => s.pref);
  const attr = typeof document !== 'undefined' ? document.documentElement.getAttribute('data-theme') : null;
  if (attr === 'dark') return true;
  if (attr === 'light') return false;
  if (pref === 'dark') return true;
  if (pref === 'light') return false;
  return typeof window !== 'undefined' && window.matchMedia('(prefers-color-scheme: dark)').matches;
}

const STATUS_LABEL: Record<PatchStatus, string> = {
  modify: 'M',
  add: 'A',
  delete: 'D',
  rename: 'R',
  binary: 'B',
};

function FileCard({ file, mode, wrap, dark }: { file: PatchFile; mode: DiffModeEnum; wrap: boolean; dark: boolean }): JSX.Element {
  const t = useT();
  const [open, setOpen] = useState(true);
  const title = file.status === 'rename' && file.oldPath !== file.newPath ? `${file.oldPath} → ${file.newPath}` : file.path;
  return (
    <div className={`patch-file status-${file.status}`}>
      <button className="patch-file-head" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
        <Icon name={open ? 'chevron-down' : 'chevron-right'} size={13} />
        <span className={`patch-status k-${file.status}`} title={file.status}>
          {STATUS_LABEL[file.status]}
        </span>
        <span className="patch-file-path mono">{title}</span>
        <span className="spacer" />
        {file.additions > 0 && <span className="patch-add">+{file.additions}</span>}
        {file.deletions > 0 && <span className="patch-del">−{file.deletions}</span>}
      </button>
      {open &&
        (file.status === 'binary' ? (
          <div className="patch-binary small muted">{t('inspect.binaryFile')}</div>
        ) : (
          <DiffView
            className="patch-diffview"
            data={{
              oldFile: { fileName: file.oldPath, fileLang: file.lang },
              newFile: { fileName: file.newPath, fileLang: file.lang },
              hunks: [file.diff],
            }}
            diffViewMode={mode}
            diffViewWrap={wrap}
            diffViewTheme={dark ? 'dark' : 'light'}
            diffViewHighlight
          />
        ))}
    </div>
  );
}

export function PatchDiffView({ patch, onViewSource }: { patch: string; onViewSource?: () => void }): JSX.Element {
  const t = useT();
  const dark = useResolvedDark();
  const [split, setSplit] = useState(true);
  const [wrap, setWrap] = useState(false);
  const files = useMemo(() => splitPatch(patch).filter((f) => f.diff.trim() !== ''), [patch]);
  const mode = split ? DiffModeEnum.Split : DiffModeEnum.Unified;

  if (files.length === 0)
    return (
      <div className="patch-root">
        {onViewSource !== undefined && (
          <div className="patch-bar">
            <span className="spacer" />
            <button className="import-btn" onClick={onViewSource}>
              <Icon name="code" size={14} /> {t('inspect.viewSource')}
            </button>
          </div>
        )}
        <div className="inspect-empty region-pad muted">{t('inspect.noDiff')}</div>
      </div>
    );

  const totalAdd = files.reduce((n, f) => n + f.additions, 0);
  const totalDel = files.reduce((n, f) => n + f.deletions, 0);

  return (
    <div className="patch-root">
      <div className="patch-bar">
        <span className="small muted">
          {files.length === 1 ? t('inspect.oneFile') : t('inspect.nFiles').replace('{n}', String(files.length))}
          {' · '}
          <span className="patch-add">+{totalAdd}</span> <span className="patch-del">−{totalDel}</span>
        </span>
        <span className="spacer" />
        {onViewSource !== undefined && (
          <button className="import-btn" onClick={onViewSource}>
            <Icon name="code" size={14} /> {t('inspect.viewSource')}
          </button>
        )}
        <button className={`icon-btn${wrap ? ' active' : ''}`} title={t('inspect.wrap')} onClick={() => setWrap((w) => !w)}>
          <Icon name="wrap" size={15} />
        </button>
        <div className="patch-modes">
          <button className={`seg-btn${split ? ' active' : ''}`} onClick={() => setSplit(true)}>
            {t('inspect.split')}
          </button>
          <button className={`seg-btn${!split ? ' active' : ''}`} onClick={() => setSplit(false)}>
            {t('inspect.unified')}
          </button>
        </div>
      </div>
      <div className="patch-files">
        {files.map((f, i) => (
          <FileCard key={`${f.path}:${i}`} file={f} mode={mode} wrap={wrap} dark={dark} />
        ))}
      </div>
    </div>
  );
}
