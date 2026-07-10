import { useT } from '../i18n';
import { useDraft } from '../state/draft';
import { Markdown } from '../ui/Markdown';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';

/// J2 — Author reports / slides / figures. Round-1 surface: a split
/// GFM+math+code Markdown editor (source ↔ live preview) over the shared
/// `Markdown` renderer (KaTeX + highlight.js, offline). The landscape doc's
/// posture is EMBED BlockNote (block editor + collab) + Quarto/Typst export for
/// the reproducible-report path; this pane is the honest interim that already
/// covers write → preview with math and code.
export function AuthorSurface(): JSX.Element {
  const t = useT();
  const [text, setText] = useDraft('author', '# \n');

  const words = text.trim() ? text.trim().split(/\s+/).length : 0;

  return (
    <WorkbenchSurface
      job="author"
      actions={
        <span className="surface-meta muted small">{t('author.words').replace('{n}', String(words))}</span>
      }
    >
      <div className="split-2">
        <textarea
          className="editor-pane mono"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={t('author.placeholder')}
          spellCheck={false}
        />
        <div className="preview-pane">
          {text.trim() ? <Markdown text={text} /> : <div className="muted region-pad">{t('author.empty')}</div>}
        </div>
      </div>
    </WorkbenchSurface>
  );
}
