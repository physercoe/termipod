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
  /// An unrecognized body opens read-only and is never serialized back — the
  /// `canvas.ts` rule (see `Board.readOnly`), applied here (coworking A5).
  ///
  /// Before this, `parseTable` answered an unparseable body with a blank
  /// three-row grid and no signal, and `TableEditor.mutate` serializes on every
  /// change — so ONE click on a table document whose body failed to parse wrote
  /// the blank grid over it and the original was gone. That was live with no
  /// agent involved; `author_apply` would only have made it routine.
  readOnly?: boolean;
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

/// Parse a table document body.
///
/// An **empty** body is a new document — there is nothing to lose, so it opens
/// as an editable blank grid. Anything else that fails to parse is content we
/// could not read, and it opens **read-only**: a blank grid the editor is free
/// to serialize back would replace the user's rows with nothing.
export function parseTable(body: string, nameCol: string): TableData {
  if (body.trim() === '') return emptyTable(nameCol);
  try {
    const d = JSON.parse(body) as Partial<TableData>;
    if (d !== null && Array.isArray(d.columns) && Array.isArray(d.rows)) {
      return { columns: d.columns, rows: d.rows };
    }
  } catch {
    /* fall through */
  }
  return { ...emptyTable(nameCol), readOnly: true };
}

export const serializeTable = (d: TableData): string => JSON.stringify(d);

/// What a mounted grid should do when its `value` prop changes (coworking B4).
///
/// The editor holds parsed `TableData` and re-emits a serialized body on every
/// mutation, so `value` changes for two very different reasons and the answer
/// differs for each:
///
///   - **the echo** — the store handing back the body this grid just wrote.
///     Re-parsing it would discard the object the user is typing into, taking
///     the row/column identities and the caret with it. Answer: `null`, do
///     nothing.
///   - **an external write** — an agent's `author_apply`, a B6 revert, a reload
///     of the linked file. The grid must adopt it, or the user keeps looking at
///     a table the document no longer contains.
///
/// `pushUndo` says whether the state being replaced belongs on the undo stack.
/// An external write should be undoable (B2's "Cmd+Z undoes the agent", in this
/// editor) — but a read-only PLACEHOLDER must never go on it. `undo` serializes
/// whatever it pops, so a blank grid on that stack is one keystroke from being
/// written over the document it merely stands in for: the A5 silent-empty class,
/// arriving through undo instead of through a click.
export function reconcileExternalTable(
  lastEmitted: string,
  incoming: string,
  current: TableData,
  nameCol: string,
): { next: TableData; pushUndo: boolean } | null {
  if (incoming === lastEmitted) return null;
  return { next: parseTable(incoming, nameCol), pushUndo: current.readOnly !== true };
}

/// Does this text look like a table document (our `{columns, rows}` JSON)? Used to
/// content-sniff a `.json` file on open so an *arbitrary* JSON file opens as text,
/// not hijacked into the grid editor.
export function isTableBody(text: string): boolean {
  try {
    const d = JSON.parse(text) as Partial<TableData>;
    return d !== null && Array.isArray(d.columns) && Array.isArray(d.rows);
  } catch {
    return false;
  }
}

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

/// Lower a table BODY to CSV, refusing one we could not read (coworking A5).
///
/// The second mouth of the same hole the `readOnly` flag closes. The CSV path
/// lowers a table through the parser on the way out, so an unreadable body
/// silently became a zero-row file — written over whatever path the user picked
/// in the save dialog. Refusing surfaces as a save error the caller already
/// reports; saving as `.json` stays byte-verbatim and is the one operation that
/// can still round-trip the user's bytes back out of the app.
export function tableBodyToCsv(body: string, nameCol: string): string {
  const data = parseTable(body, nameCol);
  if (data.readOnly === true) throw new Error('This table could not be read, so it cannot be exported as CSV.');
  return tableToCsv(data);
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
