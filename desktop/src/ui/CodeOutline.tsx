import { useState } from 'react';
import { useT } from '../i18n';
import { Icon } from './Icon';
import { ResizeHandle, usePanelWidth } from './ResizeHandle';
import type { CodeSymbol, SymbolKind } from '../state/treeSitter';

/// The Inspect (J3) code outline — a right-hand foldable, resizable rail of the
/// file's functions / classes / methods / types (tree-sitter symbols, extracted
/// in `state/treeSitter.ts`). Clicking a symbol jumps the CodeView caret to its
/// line. Mirrors the Author markdown outline's UX + keys (`side='right'`, fold +
/// width persistence) and reuses its rail chrome CSS; a file with no extractable
/// symbols (or an unsupported language) hides the rail entirely — the outline's
/// analog of the markdown rail's ≤1-heading rule.

const KIND_BADGE: Record<SymbolKind, string> = { function: 'ƒ', class: 'C', method: 'm', type: 'T' };

export function CodeOutline({
  symbols,
  onJump,
  widthKey = 'termipod.debug.outlineW',
  foldKey = 'termipod.debug.outlineOpen',
}: {
  symbols: CodeSymbol[];
  /// Jump the editor to a 1-based line (CodeView.revealLine).
  onJump: (line: number) => void;
  widthKey?: string;
  foldKey?: string;
}): JSX.Element | null {
  const t = useT();
  const [open, setOpen] = useState(() => localStorage.getItem(foldKey) !== '0');
  const [outlineW, resizeOutline] = usePanelWidth(widthKey, 220, 160, 420, -1);

  function fold(next: boolean): void {
    setOpen(next);
    try {
      localStorage.setItem(foldKey, next ? '1' : '0');
    } catch {
      /* ignore */
    }
  }

  if (symbols.length === 0) return null;

  const foldBtn = (
    <button className="read-fold" title={t('read.collapse')} onClick={() => fold(false)}>
      <Icon name="chevron-right" size={14} />
    </button>
  );
  const rail = (
    <div className="mdreader-outline side-right code-outline" style={{ width: outlineW }}>
      <div className="mdreader-outline-head">
        {foldBtn}
        <span className="muted small">{t('inspect.outline')}</span>
        <span className="spacer" />
      </div>
      <div className="mdreader-outline-list">
        {symbols.map((s, i) => (
          <button
            key={`${s.line}-${s.name}-${i}`}
            className={`code-outline-item${s.kind === 'method' ? ' method' : ''}`}
            title={`${s.name} · line ${s.line}`}
            onClick={() => onJump(s.line)}
          >
            <span className={`code-outline-kind k-${s.kind}`}>{KIND_BADGE[s.kind]}</span>
            <span className="code-outline-name">{s.name}</span>
          </button>
        ))}
      </div>
    </div>
  );
  const handle = <ResizeHandle onResize={resizeOutline} />;
  return open ? (
    <>
      {handle}
      {rail}
    </>
  ) : (
    <button className="mdreader-outline-show side-right" title={t('inspect.outline')} onClick={() => fold(true)}>
      <Icon name="list" />
    </button>
  );
}
