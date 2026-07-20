import { useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import {
  COL_TYPES,
  newId,
  parseTable,
  serializeTable,
  tableToCsv,
  type ColType,
  type TableColumn,
  type TableData,
} from '../state/table';
import { Icon } from './Icon';
import { clipboardItems, useContextMenu } from './ContextMenu';

/// Cap on the structural-undo history so a long editing session stays bounded.
const UNDO_CAP = 50;

/// The **table / database** document editor — a lightweight Notion/Obsidian-style
/// grid: typed columns (text · number · checkbox · select · date) over rows of
/// cells. It is one kind of Author document; on disk a table is a real `.csv`
/// file (see `state/table.ts`). Deliberately dependency-free (no grid library):
/// per-type inline cell editors, add/remove row & column, rename/retype a column,
/// CSV export. Keyed per doc by the caller, so switching tabs remounts clean.

export function TableEditor({ value, onChange }: { value: string; onChange: (next: string) => void }): JSX.Element {
  const t = useT();
  const menu = useContextMenu();
  const [data, setData] = useState<TableData>(() => parseTable(value, t('table.colName')));
  const dataRef = useRef(data);
  dataRef.current = data;
  // Structural undo (#322): every mutation snapshots the prior table so add/remove
  // row+column, rename, retype, and cell edits can be rolled back. Bounded so a
  // long editing session doesn't grow the stack without limit. Cell text still has
  // the input's own native undo while typing; this covers the structural ops the
  // browser can't undo.
  const [past, setPast] = useState<TableData[]>([]);
  function mutate(fn: (d: TableData) => TableData): void {
    const prev = dataRef.current;
    const next = fn(prev);
    setPast((p) => [...p, prev].slice(-UNDO_CAP));
    dataRef.current = next;
    setData(next);
    onChange(serializeTable(next));
  }
  function undo(): void {
    setPast((p) => {
      if (p.length === 0) return p;
      const prev = p[p.length - 1];
      dataRef.current = prev;
      setData(prev);
      onChange(serializeTable(prev));
      return p.slice(0, -1);
    });
  }

  const { columns, rows } = data;
  const [menuCol, setMenuCol] = useState<string | null>(null);
  const rootRef = useRef<HTMLDivElement | null>(null);

  // Move focus to another cell's editor (grid keyboard navigation, #322).
  function focusCell(r: number, c: number): void {
    rootRef.current?.querySelector<HTMLElement>(`[data-r="${r}"][data-c="${c}"]`)?.focus();
  }

  // Keydown handler for the whole grid. Two behaviours, both guarded so in-cell
  // editing keeps working: Cmd/Ctrl+Z structural undo (not while typing — the
  // input keeps its native undo), and Notion-style navigation (#322) — Enter
  // moves down (cells commit on every change, so there is nothing to "commit"
  // first), Tab/Shift-Tab walk the cells row-major, arrows move between cells
  // but only where they don't steal a native behaviour (text caret movement,
  // number/date stepping, select option-cycling).
  function onGridKeyDown(e: React.KeyboardEvent): void {
    const tag = (e.target as HTMLElement).tagName;
    const editable = tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'z' && !e.shiftKey && !editable) {
      e.preventDefault();
      undo();
      return;
    }
    const el = (e.target as HTMLElement).closest('[data-r]');
    if (el === null || !(el instanceof HTMLElement)) return;
    const r = Number(el.dataset.r);
    const c = Number(el.dataset.c);
    const lastRow = rows.length - 1;
    const lastCol = columns.length - 1;
    if (e.key === 'Enter' && tag !== 'SELECT') {
      if (r < lastRow) {
        e.preventDefault();
        focusCell(r + 1, c);
      }
      return;
    }
    if (e.key === 'Tab') {
      const next = c + (e.shiftKey ? -1 : 1);
      // Row-major walk; at the grid edges let focus leave the table natively.
      const [nr, nc] = next > lastCol ? [r + 1, 0] : next < 0 ? [r - 1, lastCol] : [r, next];
      if (nr >= 0 && nr <= lastRow) {
        e.preventDefault();
        focusCell(nr, nc);
      }
      return;
    }
    if (e.key.startsWith('Arrow')) {
      // Selects cycle options and number/date inputs step values with arrows —
      // navigation must not steal those.
      if (el instanceof HTMLSelectElement) return;
      if (el instanceof HTMLInputElement && (el.type === 'number' || el.type === 'date')) return;
      if (el instanceof HTMLInputElement && el.type === 'text') {
        const s = el.selectionStart ?? 0;
        const en = el.selectionEnd ?? 0;
        // Leave the cell only when the caret is already at the edge in that
        // direction; otherwise the arrow keeps moving the caret inside the text.
        if (e.key === 'ArrowLeft' && (s > 0 || en > 0)) return;
        if (e.key === 'ArrowRight' && (s < el.value.length || en < el.value.length)) return;
      }
      const [dr, dc] =
        e.key === 'ArrowUp' ? [-1, 0] : e.key === 'ArrowDown' ? [1, 0] : e.key === 'ArrowLeft' ? [0, -1] : [0, 1];
      const nr = r + dr;
      const nc = c + dc;
      if (nr >= 0 && nr <= lastRow && nc >= 0 && nc <= lastCol) {
        e.preventDefault();
        focusCell(nr, nc);
      }
    }
  }

  // ---- mutations ----
  const addRow = (): void => mutate((d) => ({ ...d, rows: [...d.rows, { id: newId('row'), cells: {} }] }));
  const removeRow = (id: string): void => mutate((d) => ({ ...d, rows: d.rows.filter((r) => r.id !== id) }));
  const addColumn = (): void =>
    mutate((d) => ({
      ...d,
      columns: [...d.columns, { id: newId('col'), name: t('table.newColumn'), type: 'text' }],
    }));
  const removeColumn = (id: string): void =>
    mutate((d) => ({
      columns: d.columns.filter((c) => c.id !== id),
      rows: d.rows.map((r) => {
        const cells: Record<string, string | number | boolean> = {};
        for (const k of Object.keys(r.cells)) if (k !== id) cells[k] = r.cells[k];
        return { ...r, cells };
      }),
    }));
  const renameColumn = (id: string, name: string): void =>
    mutate((d) => ({ ...d, columns: d.columns.map((c) => (c.id === id ? { ...c, name } : c)) }));
  const retypeColumn = (id: string, type: ColType): void =>
    mutate((d) => ({
      ...d,
      columns: d.columns.map((c) =>
        c.id === id ? { ...c, type, options: type === 'select' ? (c.options ?? []) : undefined } : c,
      ),
    }));
  const setColumnOptions = (id: string, options: string[]): void =>
    mutate((d) => ({ ...d, columns: d.columns.map((c) => (c.id === id ? { ...c, options } : c)) }));
  const setCell = (rowId: string, colId: string, v: string | number | boolean): void =>
    mutate((d) => ({
      ...d,
      rows: d.rows.map((r) => (r.id === rowId ? { ...r, cells: { ...r.cells, [colId]: v } } : r)),
    }));

  function exportCsv(): void {
    const blob = new Blob([tableToCsv(data)], { type: 'text/csv' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'table.csv';
    a.click();
    URL.revokeObjectURL(url);
  }

  const colById = useMemo(() => {
    const m = new Map<string, TableColumn>();
    columns.forEach((c) => m.set(c.id, c));
    return m;
  }, [columns]);

  return (
    <div
      className="table-editor"
      ref={rootRef}
      onKeyDown={onGridKeyDown}
      onContextMenu={(e) =>
        menu.open(e, [
          ...clipboardItems(t),
          { label: t('table.addRow'), onClick: addRow },
          { label: t('table.addColumn'), onClick: addColumn },
          { label: t('table.undo'), onClick: undo, disabled: past.length === 0 },
        ])
      }
    >
      {menu.node}
      <div className="author-doc-bar table-toolbar">
        <button className="import-btn" onClick={addRow}>
          <Icon name="plus" size={13} /> {t('table.addRow')}
        </button>
        <button className="import-btn" onClick={addColumn}>
          <Icon name="plus" size={13} /> {t('table.addColumn')}
        </button>
        <button className="import-btn" onClick={undo} disabled={past.length === 0} title={t('table.undo')}>
          <Icon name="undo" size={13} /> {t('table.undo')}
        </button>
        <span className="spacer" />
        <span className="muted small">{t('table.count').replace('{r}', String(rows.length)).replace('{c}', String(columns.length))}</span>
        <button className="import-btn" onClick={exportCsv} title={t('table.exportCsv')}>
          <Icon name="download" size={13} /> CSV
        </button>
      </div>

      <div className="table-scroll scroll">
        <table className="db-table">
          <thead>
            <tr>
              <th className="db-th db-rownum" aria-hidden />
              {columns.map((c) => (
                <th key={c.id} className="db-th">
                  <div className="db-th-inner">
                    <input
                      className="db-colname"
                      value={c.name}
                      onChange={(e) => renameColumn(c.id, e.target.value)}
                      placeholder={t('table.colName')}
                    />
                    <button
                      className="db-col-menu"
                      title={t('table.column')}
                      onClick={() => setMenuCol((m) => (m === c.id ? null : c.id))}
                    >
                      <Icon name="chevron-down" size={12} />
                    </button>
                  </div>
                  {menuCol === c.id && (
                    <div className="db-col-pop">
                      <label className="db-col-poprow">
                        <span className="muted small">{t('table.type')}</span>
                        <select value={c.type} onChange={(e) => retypeColumn(c.id, e.target.value as ColType)}>
                          {COL_TYPES.map((ty) => (
                            <option key={ty} value={ty}>
                              {t(`table.type_${ty}`)}
                            </option>
                          ))}
                        </select>
                      </label>
                      {c.type === 'select' && (
                        <label className="db-col-poprow">
                          <span className="muted small">{t('table.options')}</span>
                          <input
                            value={(c.options ?? []).join(', ')}
                            placeholder="a, b, c"
                            onChange={(e) =>
                              setColumnOptions(
                                c.id,
                                e.target.value
                                  .split(',')
                                  .map((s) => s.trim())
                                  .filter((s) => s !== ''),
                              )
                            }
                          />
                        </label>
                      )}
                      <button
                        className="db-col-del danger"
                        disabled={columns.length <= 1}
                        onClick={() => {
                          removeColumn(c.id);
                          setMenuCol(null);
                        }}
                      >
                        {t('table.deleteColumn')}
                      </button>
                    </div>
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={r.id} className="db-tr">
                <td className="db-rownum muted small">
                  <span className="db-rownum-n">{i + 1}</span>
                  <button className="db-row-del" title={t('table.deleteRow')} onClick={() => removeRow(r.id)}>
                    ×
                  </button>
                </td>
                {columns.map((c, j) => (
                  <td key={c.id} className="db-td">
                    <Cell col={colById.get(c.id) ?? c} value={r.cells[c.id]} onChange={(v) => setCell(r.id, c.id, v)} r={i} c={j} />
                  </td>
                ))}
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td className="db-empty muted small" colSpan={columns.length + 1}>
                  {t('table.empty')}
                </td>
              </tr>
            )}
          </tbody>
        </table>
        <button className="db-addrow-btn" onClick={addRow}>
          <Icon name="plus" size={13} /> {t('table.addRow')}
        </button>
      </div>
    </div>
  );
}

function Cell({
  col,
  value,
  onChange,
  r,
  c,
}: {
  col: TableColumn;
  value: string | number | boolean | undefined;
  onChange: (v: string | number | boolean) => void;
  // Grid coordinates — the container's keyboard navigation targets these (#322).
  r: number;
  c: number;
}): JSX.Element {
  const t = useT();
  switch (col.type) {
    case 'checkbox':
      return (
        <input
          type="checkbox"
          className="db-cell-check"
          checked={value === true}
          onChange={(e) => onChange(e.target.checked)}
          data-r={r}
          data-c={c}
        />
      );
    case 'number':
      return (
        <input
          type="number"
          className="db-cell db-cell-num"
          value={value === undefined ? '' : String(value)}
          onChange={(e) => onChange(e.target.value === '' ? '' : Number(e.target.value))}
          data-r={r}
          data-c={c}
        />
      );
    case 'date':
      return (
        <input
          type="date"
          className="db-cell db-cell-date"
          value={value === undefined ? '' : String(value)}
          onChange={(e) => onChange(e.target.value)}
          data-r={r}
          data-c={c}
        />
      );
    case 'select':
      return (
        <select
          className="db-cell db-cell-select"
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          data-r={r}
          data-c={c}
        >
          <option value="">{t('table.empty_cell')}</option>
          {(col.options ?? []).map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </select>
      );
    default:
      return (
        <input
          className="db-cell"
          value={value === undefined ? '' : String(value)}
          onChange={(e) => onChange(e.target.value)}
          data-r={r}
          data-c={c}
        />
      );
  }
}
