import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { EditorState, type Extension } from '@codemirror/state';
import { EditorView, lineNumbers, highlightActiveLineGutter } from '@codemirror/view';
import { foldGutter, codeFolding } from '@codemirror/language';
import { MergeView } from '@codemirror/merge';
import { codeTheme, highlightExtension, resolveLang } from './codeTheme';
import { Icon } from './Icon';
import { ResizeHandle } from './ResizeHandle';
import { useT } from '../i18n';

/// The Inspect (J3) **two-blob compare** viewer (W2, tier 2) — editor-grade
/// side-by-side diffing of two texts via `@codemirror/merge`, on top of the same
/// CM6 theme/highlight the code viewer uses. Lazy chunk (the `merge` package
/// never touches the boot bundle — plan §7). Both sides are read-only; a
/// bounded `scanLimit`/`timeout` keeps a pathological pair from hanging (the huge
/// unrelated-file case degrades to "no alignment found" rather than freezing).

export function TwoBlobCompare({
  a,
  b,
  aTitle,
  bTitle,
  filename,
  lang,
}: {
  a: string;
  b: string;
  aTitle?: string;
  bTitle?: string;
  filename?: string;
  lang?: string;
}): JSX.Element {
  const t = useT();
  const hostRef = useRef<HTMLDivElement | null>(null);
  const [wrap, setWrap] = useState(false);
  const viewRef = useRef<MergeView | null>(null);
  const [splitRatio, setSplitRatio] = useState(() => {
    const stored = Number(localStorage.getItem('termipod.inspect.compareRatio'));
    return Number.isFinite(stored) && stored >= 0.2 && stored <= 0.8 ? stored : 0.5;
  });
  const persistRatio = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  function resizePanes(dx: number): void {
    const width = hostRef.current?.clientWidth ?? 0;
    if (width <= 0) return;
    setSplitRatio((current) => {
      const next = Math.max(0.2, Math.min(0.8, current + dx / width));
      if (persistRatio.current !== undefined) clearTimeout(persistRatio.current);
      persistRatio.current = setTimeout(() => {
        try {
          localStorage.setItem('termipod.inspect.compareRatio', String(next));
        } catch {
          /* ignore */
        }
      }, 250);
      return next;
    });
  }

  useEffect(
    () => () => {
      if (persistRatio.current !== undefined) clearTimeout(persistRatio.current);
    },
    [],
  );

  useEffect(() => {
    const host = hostRef.current;
    if (host === null) return;
    let disposed = false;
    let view: MergeView | null = null;

    void (async () => {
      const resolved = await resolveLang(filename, lang);
      if (disposed || host === null) return;
      const langExt: Extension = resolved?.ext ?? [];
      const wrapExt: Extension = wrap ? EditorView.lineWrapping : [];
      const common: Extension[] = [
        lineNumbers(),
        highlightActiveLineGutter(),
        foldGutter(),
        codeFolding(),
        highlightExtension,
        langExt,
        wrapExt,
        codeTheme,
        EditorState.readOnly.of(true),
        EditorView.editable.of(false),
      ];
      view = new MergeView({
        parent: host,
        a: { doc: a, extensions: common },
        b: { doc: b, extensions: common },
        collapseUnchanged: { margin: 3, minSize: 4 },
        gutter: true,
        highlightChanges: true,
        diffConfig: { scanLimit: 5000, timeout: 3000 },
      });
      viewRef.current = view;
    })();

    return () => {
      disposed = true;
      view?.destroy();
      viewRef.current = null;
    };
    // Rebuild on any input/lang/wrap change (cheap; the parent keys by tab).
  }, [a, b, filename, lang, wrap]);

  return (
    <div className="compare-root">
      <div className="compare-bar">
        <span className="compare-side small muted" title={aTitle}>
          {aTitle ?? t('inspect.compareA')}
        </span>
        <Icon name="git-compare" size={13} />
        <span className="compare-side small muted" title={bTitle}>
          {bTitle ?? t('inspect.compareB')}
        </span>
        <span className="spacer" />
        <button className={`icon-btn${wrap ? ' active' : ''}`} title={t('inspect.wrap')} onClick={() => setWrap((w) => !w)}>
          <Icon name="wrap" size={15} />
        </button>
      </div>
      <div
        className="compare-stage"
        style={{ '--compare-split': `${splitRatio * 100}%` } as CSSProperties}
      >
        <div className="compare-host" ref={hostRef} />
        <div className="compare-split-handle">
          <ResizeHandle onResize={resizePanes} />
        </div>
      </div>
    </div>
  );
}
