import { useMemo, useState } from 'react';
import { useT } from '../i18n';
import { useDraft } from '../state/draft';
import { Markdown } from '../ui/Markdown';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';

const LANGS = ['auto', 'python', 'go', 'rust', 'typescript', 'javascript', 'bash', 'json', 'diff', 'log'];

/// J3 — Debug code and runs. The director reads diffs, stack traces, and huge
/// logs to understand and decide (the agent hand-fixes). Round-1 surface: paste
/// code/logs on the left, syntax-highlighted (highlight.js via the shared
/// Markdown fenced-block path, offline) on the right, with a line count and
/// language hint. The landscape doc's posture is EMBED Monaco (+ MonacoDiffEditor
/// for real side-by-side diffs and file:line jumps) — the next dep-adding round;
/// this covers paste → highlight today.
export function DebugSurface(): JSX.Element {
  const t = useT();
  const [code, setCode] = useDraft('debug');
  const [lang, setLang] = useState('auto');

  const lines = code === '' ? 0 : code.split('\n').length;
  const fenced = useMemo(() => {
    const tag = lang === 'auto' ? '' : lang;
    return `\`\`\`${tag}\n${code}\n\`\`\``;
  }, [code, lang]);

  return (
    <WorkbenchSurface
      job="debug"
      actions={
        <>
          <select className="surface-select" value={lang} onChange={(e) => setLang(e.target.value)}>
            {LANGS.map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </select>
          <span className="surface-meta muted small">{t('debug.lines').replace('{n}', String(lines))}</span>
        </>
      }
    >
      <div className="split-2">
        <textarea
          className="editor-pane mono"
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder={t('debug.placeholder')}
          spellCheck={false}
        />
        <div className="preview-pane code-view">
          {code.trim() ? <Markdown text={fenced} /> : <div className="muted region-pad">{t('debug.empty')}</div>}
        </div>
      </div>
    </WorkbenchSurface>
  );
}
