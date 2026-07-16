/// J2 Author — the **table / database** document model. A lightweight
/// Notion/Obsidian-style grid: typed columns (text · number · checkbox · select ·
/// date) over rows of cells. It is one kind of Author document; the in-app board
/// is this JSON, but on disk a table is a **real `.csv` file** (spreadsheet
/// interchange), so `tableToCsv` / `csvToTable` bridge the two. This module is the
/// pure model + (de)serialization; `ui/TableEditor.tsx` is the editor.

export type ColType = 'text' | 'number' | 'checkbox' | 'select' | 'date';
export const COL_TYPES: ColType[] = ['text', 'number', 'checkbox', 'select', 'date'];

export interface TableColumn {
  id: string;
  name: string;
  type: ColType;
  options?: string[]; // for `select`
}
export interface TableRow {
  id: string;
  cells: Record<string, string | number | boolean>;
}
export interface TableData {
  columns: TableColumn[];
  rows: TableRow[];
}

let seq = 0;
export function newId(prefix: string): string {
  seq += 1;
  return `${prefix}${Date.now().toString(36)}${seq}`;
}

/// A fresh table: one text column and three empty rows — the same gentle default
/// Notion opens a new database with.
export function emptyTable(nameCol: string): TableData {
  const col: TableColumn = { id: newId('col'), name: nameCol, type: 'text' };
  const rows: TableRow[] = [0, 1, 2].map(() => ({ id: newId('row'), cells: {} }));
  return { columns: [col], rows };
}

export function parseTable(body: string, nameCol: string): TableData {
  try {
    const d = JSON.parse(body) as Partial<TableData>;
    if (d !== null && Array.isArray(d.columns) && Array.isArray(d.rows)) {
      return { columns: d.columns, rows: d.rows };
    }
  } catch {
    /* fall through */
  }
  return emptyTable(nameCol);
}

export const serializeTable = (d: TableData): string => JSON.stringify(d);

function csvEscape(v: string): string {
  return /[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;
}
export function cellText(v: string | number | boolean | undefined, type: ColType): string {
  if (v === undefined || v === null) return '';
  if (type === 'checkbox') return v === true ? 'true' : 'false';
  return String(v);
}

/// Serialize to CSV (the on-disk format). Column *types* are not encoded — a CSV
/// is untyped, so a saved table re-opens with every column as text. That is the
/// standard spreadsheet round-trip and keeps the file usable by Excel/Numbers/
/// Notion/Obsidian.
export function tableToCsv(data: TableData): string {
  const header = data.columns.map((c) => csvEscape(c.name)).join(',');
  if (data.rows.length === 0) return header;
  const body = data.rows
    .map((r) => data.columns.map((c) => csvEscape(cellText(r.cells[c.id], c.type))).join(','))
    .join('\n');
  return `${header}\n${body}`;
}

// RFC-4180-ish CSV reader: handles quoted fields, escaped quotes (""), embedded
// commas/newlines, and CRLF. Returns rows of raw string fields.
function parseCsv(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let field = '';
  let inQuotes = false;
  const s = text.replace(/\r\n?/g, '\n');
  for (let i = 0; i < s.length; i += 1) {
    const ch = s[i];
    if (inQuotes) {
      if (ch === '"') {
        if (s[i + 1] === '"') {
          field += '"';
          i += 1;
        } else {
          inQuotes = false;
        }
      } else {
        field += ch;
      }
      continue;
    }
    if (ch === '"') {
      inQuotes = true;
    } else if (ch === ',') {
      row.push(field);
      field = '';
    } else if (ch === '\n') {
      row.push(field);
      rows.push(row);
      row = [];
      field = '';
    } else {
      field += ch;
    }
  }
  if (field !== '' || row.length > 0) {
    row.push(field);
    rows.push(row);
  }
  // Drop fully-empty trailing lines.
  return rows.filter((r) => !(r.length === 1 && r[0] === ''));
}

/// Build a table from CSV: the first row is the header (column names), every
/// column is text. Empty input yields a fresh empty table.
export function csvToTable(csv: string, nameFallback: string): TableData {
  const records = parseCsv(csv);
  if (records.length === 0) return emptyTable(nameFallback);
  const headers = records[0];
  const columns: TableColumn[] = headers.map((h, i) => ({
    id: `col${i}`,
    name: h.trim() !== '' ? h : `${nameFallback} ${i + 1}`,
    type: 'text',
  }));
  const rows: TableRow[] = records.slice(1).map((rec, ri) => {
    const cells: Record<string, string | number | boolean> = {};
    columns.forEach((c, i) => {
      if (rec[i] !== undefined && rec[i] !== '') cells[c.id] = rec[i];
    });
    return { id: `row${ri}`, cells };
  });
  return { columns, rows };
}
