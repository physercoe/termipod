/// Pure parsing and sorting helpers for Inspect's read-only CSV/TSV preview.
/// The Author table model deliberately stays separate: Inspect shows the bytes
/// as they are and never manufactures typed/editable document state from them.

export type Delimiter = ',' | '\t';

export interface DelimitedRow {
  /// Original 1-based data-row number (the header is not counted).
  number: number;
  cells: string[];
}

export interface DelimitedTable {
  headers: string[];
  rows: DelimitedRow[];
  delimiter: Delimiter;
  unevenRows: number;
}

export type DelimitedParseResult =
  | { ok: true; table: DelimitedTable }
  | { ok: false; message: string };

/** RFC-4180-style reader, also used for TSV (quotes protect tabs there). */
export function parseDelimited(text: string, delimiter: Delimiter): DelimitedParseResult {
  const records: string[][] = [];
  let row: string[] = [];
  let field = '';
  let quoted = false;
  let afterQuote = false;
  const input = text.replace(/\r\n?/g, '\n');

  const finishField = (): void => {
    row.push(field);
    field = '';
    afterQuote = false;
  };
  const finishRow = (): void => {
    finishField();
    records.push(row);
    row = [];
  };

  for (let i = 0; i < input.length; i += 1) {
    const ch = input[i];
    if (quoted) {
      if (ch === '"') {
        if (input[i + 1] === '"') {
          field += '"';
          i += 1;
        } else {
          quoted = false;
          afterQuote = true;
        }
      } else {
        field += ch;
      }
      continue;
    }

    if (afterQuote && ch !== delimiter && ch !== '\n' && ch !== ' ' && ch !== '\t') {
      return { ok: false, message: `unexpected character after a closing quote at character ${i + 1}` };
    }
    if (afterQuote && (ch === ' ' || (ch === '\t' && delimiter !== '\t'))) continue;

    if (ch === '"') {
      if (field !== '') return { ok: false, message: `quote inside an unquoted field at character ${i + 1}` };
      quoted = true;
    } else if (ch === delimiter) {
      finishField();
    } else if (ch === '\n') {
      finishRow();
    } else {
      field += ch;
    }
  }

  if (quoted) return { ok: false, message: 'the final quoted field is not closed' };
  if (field !== '' || row.length > 0 || afterQuote) finishRow();
  while (records.length > 0 && records[records.length - 1].every((cell) => cell === '')) records.pop();

  if (records.length === 0) return { ok: true, table: { headers: [], rows: [], delimiter, unevenRows: 0 } };

  const width = Math.max(1, ...records.map((record) => record.length));
  const first = records[0];
  const expectedWidth = first.length;
  const headers = Array.from({ length: width }, (_, index) => {
    const label = first[index]?.trim() ?? '';
    return label !== '' ? label : `Column ${index + 1}`;
  });
  let unevenRows = 0;
  const rows = records.slice(1).map((record, index) => {
    if (record.length !== expectedWidth) unevenRows += 1;
    return {
      number: index + 1,
      cells: Array.from({ length: width }, (_, column) => record[column] ?? ''),
    };
  });
  return { ok: true, table: { headers, rows, delimiter, unevenRows } };
}

function numericValue(value: string): number | null {
  const trimmed = value.trim();
  if (trimmed === '' || !/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$/i.test(trimmed)) return null;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : null;
}

/** Numeric columns feel numeric; mixed/text columns use natural text order. */
export function compareDelimitedCells(a: string, b: string): number {
  const an = numericValue(a);
  const bn = numericValue(b);
  if (an !== null && bn !== null) return an - bn;
  return a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' });
}

export function matchesDelimitedQuery(row: DelimitedRow, query: string): boolean {
  const needle = query.trim().toLocaleLowerCase();
  return needle === '' || row.cells.some((cell) => cell.toLocaleLowerCase().includes(needle));
}
