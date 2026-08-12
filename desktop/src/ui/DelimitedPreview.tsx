import { useMemo, useState } from 'react';
import type { CSSProperties, PointerEvent as ReactPointerEvent } from 'react';
import { TableVirtuoso, type TableComponents } from 'react-virtuoso';
import { useT } from '../i18n';
import {
  compareDelimitedCells,
  matchesDelimitedQuery,
  parseDelimited,
  type DelimitedRow,
  type Delimiter,
} from '../state/inspectDelimited';
import { Icon } from './Icon';

const PreviewTable: TableComponents<DelimitedRow>['Table'] = (props) => (
  <table {...props} className="inspect-csv-table">
    {props.children}
  </table>
);

const PREVIEW_COMPONENTS: TableComponents<DelimitedRow> = {
  Table: PreviewTable,
  TableRow: (props) => <tr {...props} className="inspect-csv-row" />,
};

type SortState = { column: number; direction: 'asc' | 'desc' } | null;

/// Read-only, virtualized CSV/TSV grid for Inspect. It intentionally has no
/// mutation affordances: Inspect explains bytes; Author owns document editing.
export function DelimitedPreview({ text, delimiter }: { text: string; delimiter: Delimiter }): JSX.Element {
  const t = useT();
  const parsed = useMemo(() => parseDelimited(text, delimiter), [delimiter, text]);
  const [query, setQuery] = useState('');
  const [sort, setSort] = useState<SortState>(null);
  const [widths, setWidths] = useState<Record<number, number>>({});

  const rows = useMemo(() => {
    if (!parsed.ok) return [];
    const filtered = parsed.table.rows.filter((row) => matchesDelimitedQuery(row, query));
    if (sort === null) return filtered;
    return [...filtered].sort((a, b) => {
      const order = compareDelimitedCells(a.cells[sort.column] ?? '', b.cells[sort.column] ?? '');
      return sort.direction === 'asc' ? order : -order;
    });
  }, [parsed, query, sort]);

  function toggleSort(column: number): void {
    setSort((current) => {
      if (current?.column !== column) return { column, direction: 'asc' };
      return { column, direction: current.direction === 'asc' ? 'desc' : 'asc' };
    });
  }

  function startResize(event: ReactPointerEvent<HTMLSpanElement>, column: number): void {
    event.preventDefault();
    event.stopPropagation();
    const startWidth = event.currentTarget.closest('th')?.getBoundingClientRect().width ?? widths[column] ?? 180;
    const startX = event.clientX;
    document.body.classList.add('inspect-csv-resizing');
    const move = (moveEvent: PointerEvent): void => {
      const width = Math.max(90, Math.min(640, startWidth + moveEvent.clientX - startX));
      setWidths((current) => ({ ...current, [column]: width }));
    };
    const finish = (): void => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', finish);
      window.removeEventListener('pointercancel', finish);
      document.body.classList.remove('inspect-csv-resizing');
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', finish);
    window.addEventListener('pointercancel', finish);
  }

  function columnStyle(column: number): CSSProperties {
    const width = widths[column] ?? 180;
    return { width, minWidth: width, maxWidth: width };
  }

  if (!parsed.ok) {
    return (
      <div className="inspect-csv-error region-pad">
        <Icon name="alert" size={16} />
        <div>
          <div>{t('inspect.csvInvalid')}</div>
          <div className="small muted">{parsed.message}</div>
        </div>
      </div>
    );
  }

  const { headers, unevenRows } = parsed.table;
  if (headers.length === 0) return <div className="muted region-pad">{t('inspect.csvEmpty')}</div>;

  return (
    <div className="inspect-csv-preview">
      <div className="inspect-csv-toolbar">
        <div className="inspect-csv-search">
          <Icon name="search" size={14} />
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t('inspect.csvFilter')}
            aria-label={t('inspect.csvFilter')}
          />
        </div>
        <span className="spacer" />
        <span className="muted small">
          {t('inspect.csvCount')
            .replace('{shown}', String(rows.length))
            .replace('{rows}', String(parsed.table.rows.length))
            .replace('{cols}', String(headers.length))}
        </span>
      </div>
      {unevenRows > 0 && (
        <div className="inspect-csv-warning">
          <Icon name="alert" size={13} />
          {t('inspect.csvUneven').replace('{n}', String(unevenRows))}
        </div>
      )}
      <div className="inspect-csv-grid">
        {rows.length === 0 ? (
          <div className="muted region-pad">{t('inspect.noMatches')}</div>
        ) : (
          <TableVirtuoso
            data={rows}
            style={{ height: '100%' }}
            components={PREVIEW_COMPONENTS}
            computeItemKey={(_index, row) => row.number}
            fixedHeaderContent={() => (
              <tr>
                <th className="inspect-csv-rownum" aria-label={t('inspect.csvRow')}>#</th>
                {headers.map((header, column) => {
                  const active = sort?.column === column;
                  return (
                    <th key={`${column}:${header}`} style={columnStyle(column)} aria-sort={active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>
                      <button type="button" onClick={() => toggleSort(column)} title={header}>
                        <span>{header}</span>
                        {active && <Icon name={sort.direction === 'asc' ? 'chevron-up' : 'chevron-down'} size={12} />}
                      </button>
                      <span
                        className="inspect-csv-resizer"
                        role="separator"
                        tabIndex={0}
                        aria-orientation="vertical"
                        aria-label={t('inspect.csvResize').replace('{col}', header)}
                        onPointerDown={(event) => startResize(event, column)}
                        onKeyDown={(event) => {
                          if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
                          event.preventDefault();
                          event.stopPropagation();
                          const delta = event.key === 'ArrowRight' ? 10 : -10;
                          setWidths((current) => ({ ...current, [column]: Math.max(90, Math.min(640, (current[column] ?? 180) + delta)) }));
                        }}
                      />
                    </th>
                  );
                })}
              </tr>
            )}
            itemContent={(_index, row) => (
              <>
                <td className="inspect-csv-rownum">{row.number}</td>
                {headers.map((_header, column) => (
                  <td key={column} style={columnStyle(column)} title={row.cells[column] ?? ''}>{row.cells[column] || '\u00a0'}</td>
                ))}
              </>
            )}
          />
        )}
      </div>
    </div>
  );
}
