/*
 * Portions of this file are derived from next-ai-draw-io
 * (https://github.com/DayuanJiang/next-ai-draw-io), `lib/utils.ts`
 * (`applyDiagramOperations`, :498-731).
 * Copyright 2025 Dayuan Jiang.
 * Licensed under the Apache License, Version 2.0.
 *
 * What is taken is the DESIGN: the `{operation, cell_id, new_xml}` grammar, the
 * rule that `new_xml`'s own id must match `cell_id`, the cascade (children, then
 * edges that reference anything being removed), the protection of the root
 * cells, and several diagnostic sentences verbatim — those strings are read by
 * an agent, and their wording is what makes the retry converge.
 *
 * The IMPLEMENTATION is not a port; see the header below for the two reasons.
 *
 * See the repository NOTICE for attribution.
 */

/// Lane D1: `author_apply {mode:'ops'}` for diagram documents — ID-addressed
/// add / update / delete against the document the user already has, instead of
/// a whole-body rewrite (agent-desktop-coworking.md §4; ADR-064).
///
/// **Why a whole mode.** `mode:'replace'` makes an agent restate the entire
/// diagram to move one box. That is expensive, and worse it is lossy in a way
/// nobody notices: every cell the model did not bother to re-emit is deleted,
/// and the result validates perfectly. Ops name what changes and leave the rest
/// untouched by construction.
///
/// **Two deliberate departures from upstream**, both from the same root: our
/// caller writes into a document the USER owns, where upstream's writes into
/// one an LLM just generated.
///
///   1. **All-or-nothing.** Upstream applies what it can and returns the
///      failures alongside the result, so a five-op batch with one bad id still
///      writes four changes. That is right when the document is disposable and
///      wrong here: a partially applied batch is a document the user did not ask
///      for, and the agent is told "some errors" rather than "nothing happened".
///      The first failing op aborts the batch and `doc.body` is never touched —
///      the same rule the replace path already follows, and the same reason C1
///      refused to port `autoFixXml`.
///   2. **Not a DOM round trip.** Upstream parses, mutates and re-serializes the
///      whole document. `XMLSerializer` rewrites everything it touches —
///      attribute order, self-closing form, the XML declaration, whitespace — so
///      a one-cell edit returns a body that differs from the user's on nearly
///      every line. That lands in their linked file, in the agent-edit revert
///      diff, and in whatever VCS they keep it under. This module edits the
///      document as TEXT at element granularity: every byte outside the named
///      cells survives verbatim.
///
/// (2) has a second payoff. `DOMParser` exists in the renderer and not under
/// `node --test`, so a DOM implementation would ship the op engine — the piece
/// that decides what gets deleted from a user's diagram — with no unit test at
/// all. Nothing here needs a DOM, so all of it is tested.
///
/// The DOM still gets the last word: the composed result goes back through
/// `validateAuthorBody` → `prepareDiagramBody` → `validateMxCell`, which runs
/// the renderer's real parser. Ops are not a way around the validation the
/// replace path gets.

export type DiagramOpKind = 'add' | 'update' | 'delete';

/// One structured edit. `new_xml` is the cell's full element source — the same
/// text an agent would put in a `mode:'replace'` body, for one cell — and is
/// required for `add`/`update`, ignored for `delete`.
export interface DiagramOperation {
  operation: DiagramOpKind;
  cell_id: string;
  new_xml: string;
}

export interface DiagramOpsResult {
  xml: string;
  added: string[];
  updated: string[];
  /// Every id removed, document order, cascade included.
  deleted: string[];
  /// The subset of `deleted` that no operation named — what the cascade took.
  /// Reported rather than logged: deleting one box can remove six cells, and an
  /// agent that says "removed the box" when six things went is not lying on
  /// purpose, it simply was not told.
  cascaded: string[];
}

export type DiagramOpsOutcome = { ok: true; result: DiagramOpsResult } | { ok: false; message: string };

/// draw.io's own layer scaffold. Not content: `wrapWithMxFile` substitutes its
/// own pair, the editor cannot show them, and an op that removes or rewrites
/// either one breaks every cell that hangs off it.
const ROOT_CELL_IDS = new Set(['0', '1']);

// ── Reading the document as text ─────────────────────────────────────────────

/// Index of the `>` closing the tag that starts at `from`, or -1. Quote-aware:
/// a plain `indexOf('>')` cuts `style="shape=x;a>b"` in half and every
/// downstream answer is then wrong about where the tag ended.
function tagEnd(xml: string, from: number): number {
  let i = from + 1;
  let quote = '';
  while (i < xml.length) {
    const c = xml[i];
    if (quote !== '') {
      if (c === quote) quote = '';
    } else if (c === '"' || c === "'") {
      quote = c;
    } else if (c === '>') {
      return i;
    }
    i += 1;
  }
  return -1;
}

function decodeXmlText(s: string): string {
  return s.replace(/&(#x[0-9a-fA-F]+|#[0-9]+|[a-zA-Z]+);/g, (whole, ref: string) => {
    if (ref.startsWith('#x')) return String.fromCodePoint(Number.parseInt(ref.slice(2), 16));
    if (ref.startsWith('#')) return String.fromCodePoint(Number.parseInt(ref.slice(1), 10));
    switch (ref) {
      case 'lt':
        return '<';
      case 'gt':
        return '>';
      case 'amp':
        return '&';
      case 'quot':
        return '"';
      case 'apos':
        return "'";
      default:
        return whole;
    }
  });
}

const SPACE = /\s/;

/// Every attribute of one opening tag, decoded.
///
/// A real tokenizer rather than a per-name regex, because a per-name regex reads
/// attribute syntax out of attribute VALUES. draw.io's HTML labels are the case
/// that breaks it: `value="&lt;p id='x'&gt;Hi&lt;/p&gt;"` escapes the angle
/// brackets but not the inner single quotes, so the literal text ` id='x'`
/// survives inside the value and a `\sid\s*=` scan happily returns `x` as the
/// cell's id. Walking the tag cannot make that mistake — it knows it is inside a
/// value.
///
/// Values are decoded because `id="a&amp;b"` names the cell `a&b`: an agent that
/// read the document through `author_read` sends the decoded form back, and a
/// raw comparison would answer "not found" for a cell plainly on screen.
function parseAttrs(tag: string): Map<string, string> {
  const out = new Map<string, string>();
  let i = 1;
  while (i < tag.length && !SPACE.test(tag[i]) && tag[i] !== '>' && tag[i] !== '/') i += 1;
  while (i < tag.length) {
    while (i < tag.length && SPACE.test(tag[i])) i += 1;
    if (i >= tag.length || tag[i] === '>' || tag[i] === '/') break;
    const nameStart = i;
    while (i < tag.length && !SPACE.test(tag[i]) && tag[i] !== '=' && tag[i] !== '>' && tag[i] !== '/') i += 1;
    const name = tag.slice(nameStart, i);
    if (name === '') {
      i += 1; // never stall: an unexpected character is skipped, not re-read
      continue;
    }
    while (i < tag.length && SPACE.test(tag[i])) i += 1;
    if (tag[i] !== '=') {
      out.set(name, '');
      continue;
    }
    i += 1;
    while (i < tag.length && SPACE.test(tag[i])) i += 1;
    const quote = tag[i];
    const start = quote === '"' || quote === "'" ? (i += 1) : i;
    while (i < tag.length && (quote === '"' || quote === "'" ? tag[i] !== quote : !SPACE.test(tag[i]) && tag[i] !== '>')) i += 1;
    out.set(name, decodeXmlText(tag.slice(start, i)));
    if (quote === '"' || quote === "'") i += 1;
  }
  return out;
}

function openTagOf(text: string): string {
  const end = tagEnd(text, 0);
  return end === -1 ? text : text.slice(0, end + 1);
}

/// An entry's addressing attributes, looking through an `<object>` wrapper.
///
/// draw.io wraps any cell carrying custom properties as
/// `<object id="n1" label="…"><mxCell parent="1" …/></object>` — the id sits on
/// the wrapper and the graph links (`parent`, `source`, `target`) sit on the
/// inner cell. Upstream indexes `mxCell` elements only, so those cells are
/// invisible to it; here they are ordinary content the user drew, and "not
/// found" for a box on screen is the least useful refusal we could give.
function entryAttrs(text: string): Map<string, string> {
  const open = openTagOf(text);
  const attrs = parseAttrs(open);
  const rest = text.slice(open.length);
  const at = rest.indexOf('<mxCell');
  if (at === -1) return attrs;
  const end = tagEnd(rest, at);
  if (end === -1) return attrs;
  for (const [k, v] of parseAttrs(rest.slice(at, end + 1))) {
    if (!attrs.has(k)) attrs.set(k, v);
  }
  return attrs;
}

/// One top-level child of `<root>`, kept as its own source text plus the
/// whitespace in front of it. Deleting an entry takes its lead with it, so
/// removing a cell does not leave the blank line it was indented on.
interface Entry {
  lead: string;
  text: string;
  id: string;
  parent: string;
  source: string;
  target: string;
}

function makeEntry(lead: string, text: string): Entry {
  const attrs = entryAttrs(text);
  return {
    lead,
    text,
    id: attrs.get('id') ?? '',
    parent: attrs.get('parent') ?? '',
    source: attrs.get('source') ?? '',
    target: attrs.get('target') ?? '',
  };
}

interface Scan {
  /// Everything up to and including the `<root>` opening tag.
  head: string;
  entries: Entry[];
  /// Whitespace between the last entry and `</root>`.
  tailLead: string;
  /// `</root>` and everything after it.
  tail: string;
}

const COMMENT = '<!--';
const CDATA = '<![CDATA[';

/// Skip a construct that is not an element. Returns the index just past it, or
/// -1 when it never closes.
function skipNonElement(xml: string, at: number): number {
  if (xml.startsWith(COMMENT, at)) {
    const e = xml.indexOf('-->', at);
    return e === -1 ? -1 : e + 3;
  }
  if (xml.startsWith(CDATA, at)) {
    const e = xml.indexOf(']]>', at);
    return e === -1 ? -1 : e + 3;
  }
  if (xml.startsWith('<?', at) || xml.startsWith('<!', at)) {
    const e = tagEnd(xml, at);
    return e === -1 ? -1 : e + 1;
  }
  return at;
}

/// Walk to the `>` of the closing tag that balances the element opening at
/// `at`. Returns -1 when it never balances.
function elementEnd(xml: string, at: number): number {
  const open = tagEnd(xml, at);
  if (open === -1) return -1;
  if (xml[open - 1] === '/') return open;
  let depth = 1;
  let i = open + 1;
  while (i < xml.length) {
    const lt = xml.indexOf('<', i);
    if (lt === -1) return -1;
    const skipped = skipNonElement(xml, lt);
    if (skipped === -1) return -1;
    if (skipped !== lt) {
      i = skipped;
      continue;
    }
    const end = tagEnd(xml, lt);
    if (end === -1) return -1;
    if (xml[lt + 1] === '/') {
      depth -= 1;
      if (depth === 0) return end;
    } else if (xml[end - 1] !== '/') {
      depth += 1;
    }
    i = end + 1;
  }
  return -1;
}

const UNREADABLE =
  'could not read this diagram as draw.io XML — mode:\'ops\' needs an uncompressed <mxfile>…<root> document. ' +
  "Call author_read to see what the document holds, and use mode:'replace' if it is not one";

/// Split a document into `<root>`'s top-level children. Everything outside is
/// carried through untouched.
export function scanRoot(xml: string): { ok: true; scan: Scan } | { ok: false; message: string } {
  // A multi-page <mxfile> is refused rather than half-served. draw.io scopes ids
  // per page, so two pages can legitimately both hold a cell `n1` and there is
  // no single answer to "update n1". Upstream takes the first <root> it finds,
  // which silently edits page one and reports success for a cell the user is
  // looking at on page three.
  const pages = xml.match(/<diagram[\s/>]/g);
  if (pages !== null && pages.length > 1) {
    return {
      ok: false,
      message: `this document has ${String(pages.length)} pages and mode:'ops' addresses one — cell ids are only unique within a page. Use mode:'replace' with the whole <mxfile>`,
    };
  }

  const openAt = /<root[\s/>]/.exec(xml)?.index ?? -1;
  if (openAt === -1) return { ok: false, message: UNREADABLE };
  const openEnd = tagEnd(xml, openAt);
  if (openEnd === -1) return { ok: false, message: UNREADABLE };

  // `<root/>` — a legitimately empty page. Rewritten to the open/close pair so
  // an `add` has somewhere to land.
  if (xml[openEnd - 1] === '/') {
    return {
      ok: true,
      scan: { head: `${xml.slice(0, openAt)}<root>`, entries: [], tailLead: '', tail: `</root>${xml.slice(openEnd + 1)}` },
    };
  }

  const entries: Entry[] = [];
  let lead = '';
  let i = openEnd + 1;
  while (i < xml.length) {
    const lt = xml.indexOf('<', i);
    if (lt === -1) return { ok: false, message: UNREADABLE };
    lead += xml.slice(i, lt);
    const skipped = skipNonElement(xml, lt);
    if (skipped === -1) return { ok: false, message: UNREADABLE };
    if (skipped !== lt) {
      lead += xml.slice(lt, skipped);
      i = skipped;
      continue;
    }
    if (xml[lt + 1] === '/') {
      const end = tagEnd(xml, lt);
      if (end === -1) return { ok: false, message: UNREADABLE };
      const name = xml.slice(lt + 2, end).trim();
      if (name !== 'root') return { ok: false, message: UNREADABLE };
      return { ok: true, scan: { head: xml.slice(0, openEnd + 1), entries, tailLead: lead, tail: xml.slice(lt) } };
    }
    const end = elementEnd(xml, lt);
    if (end === -1) return { ok: false, message: UNREADABLE };
    entries.push(makeEntry(lead, xml.slice(lt, end + 1)));
    lead = '';
    i = end + 1;
  }
  return { ok: false, message: UNREADABLE };
}

/// What stays behind when an entry is deleted: its lead minus the trailing
/// whitespace run.
///
/// A deleted cell should take the blank line it sat on, and must NOT take
/// anything the user wrote. Those are the same string — an entry's `lead` holds
/// both the indentation and any comment before it — so a delete that dropped the
/// whole lead would silently remove `<!-- the approval path -->` along with the
/// box it annotated. Small, and exactly the class of quiet loss this mode exists
/// to avoid.
function leadResidue(lead: string): string {
  return lead.slice(0, lead.length - (/\s*$/.exec(lead)?.[0].length ?? 0));
}

function residueEntry(lead: string): Entry {
  return { lead, text: '', id: '', parent: '', source: '', target: '' };
}

function rebuild(scan: Scan, entries: readonly Entry[]): string {
  return `${scan.head}${entries.map((e) => `${e.lead}${e.text}`).join('')}${scan.tailLead}${scan.tail}`;
}

/// Read one cell out of an operation's `new_xml`. Exactly one element, because
/// an op names exactly one cell: upstream takes the first `mxCell` it finds and
/// drops whatever followed, so a two-cell `new_xml` silently applies half of
/// what the agent sent.
function parseFragment(newXml: string, kind: DiagramOpKind, cellId: string): { ok: true; entry: Entry } | { ok: false; message: string } {
  const scanned = scanRoot(`<root>${newXml}</root>`);
  if (!scanned.ok) {
    return { ok: false, message: `${kind}: new_xml for "${cellId}" is not a well-formed element — check that every tag is closed and every < and & in an attribute is escaped` };
  }
  const entries = scanned.scan.entries;
  if (entries.length === 0) {
    return { ok: false, message: `${kind}: new_xml must contain an mxCell element (got no element for "${cellId}")` };
  }
  if (entries.length > 1) {
    return {
      ok: false,
      message: `${kind}: new_xml for "${cellId}" holds ${String(entries.length)} elements and an operation addresses one cell — send one operation per cell`,
    };
  }
  const entry = entries[0];
  // Upstream's rule, kept: the id in the payload must agree with the id in the
  // op. They are two statements of the same fact and a disagreement means the
  // model lost track of which cell it was editing — the one case where guessing
  // which of the two it meant is worse than refusing.
  if (entry.id !== cellId) {
    return {
      ok: false,
      message: `${kind}: ID mismatch: cell_id is "${cellId}" but new_xml has id="${entry.id}"`,
    };
  }
  return { ok: true, entry: { ...entry, lead: '' } };
}

// ── The cascade ──────────────────────────────────────────────────────────────

/// Every id that goes when `seed` goes: the cell, its descendants (anything
/// whose `parent` chain reaches it — a group's members, a cell's label), and
/// every edge with an endpoint on any of them, plus those edges' own children.
///
/// A worklist rather than upstream's recursion, and the root-cell guard applies
/// to every hop rather than only the descendant one. It is not decoration: a
/// layer cell that (wrongly) declares `parent`/`source`/`target` pointing into
/// content — `<mxCell id="1" parent="n1"/>` — is a document draw.io would never
/// write but that a `mode:'replace'` body can perfectly well contain, and
/// nothing validates parent sanity. Without the guard, deleting `n1` would take
/// the layer with it and orphan every cell hanging off it.
function cascadeFrom(entries: readonly Entry[], seed: string): Set<string> {
  const out = new Set<string>();
  const work = [seed];
  while (work.length > 0) {
    const id = work.pop() as string;
    if (id === '' || ROOT_CELL_IDS.has(id) || out.has(id)) continue;
    out.add(id);
    for (const e of entries) {
      if (e.id === '' || out.has(e.id)) continue;
      if (e.parent === id || e.source === id || e.target === id) work.push(e.id);
    }
  }
  return out;
}

// ── Applying a batch ─────────────────────────────────────────────────────────

function fail(message: string): DiagramOpsOutcome {
  return { ok: false, message };
}

/// Apply `ops` to `xml`, or refuse and change nothing.
///
/// Operations run in the order given, against the state the earlier ones left —
/// so `delete n1` followed by `add n1` is a replace, and an `add` of an id an
/// earlier op removed is legal.
export function applyDiagramOperations(xml: string, ops: readonly DiagramOperation[]): DiagramOpsOutcome {
  if (ops.length === 0) {
    return fail("operations is empty — send at least one {operation, cell_id, new_xml} entry, or use mode:'replace' to rewrite the whole diagram");
  }
  const scanned = scanRoot(xml);
  if (!scanned.ok) return fail(scanned.message);
  const scan = scanned.scan;

  let entries = [...scan.entries];
  const seen = new Map<string, number>();
  for (const e of entries) {
    if (e.id === '') continue;
    seen.set(e.id, (seen.get(e.id) ?? 0) + 1);
  }
  const ambiguous = [...seen.entries()].filter(([, n]) => n > 1).map(([id]) => id);
  if (ambiguous.length > 0) {
    // An id addresses one cell or it addresses nothing. `validateMxCell` refuses
    // duplicates on the way in, so this is a document that arrived some other
    // way — and picking one of the two would edit a cell at random.
    return fail(
      `this diagram has more than one cell with id ${ambiguous.slice(0, 3).map((i) => `"${i}"`).join(', ')}, so an operation cannot address one — fix the duplicates with mode:'replace' first`,
    );
  }

  const added: string[] = [];
  const updated: string[] = [];
  const deleted: string[] = [];
  const cascaded: string[] = [];
  // Ids this batch has already removed. A cascade takes the edges hanging off a
  // deleted box, and an agent that also listed those edges explicitly was right
  // about the document — so a delete naming one is a no-op, while a delete
  // naming an id that never existed is still an error. Upstream cannot tell
  // those apart (it skips every miss silently) and so reports success for a
  // typo'd id.
  const removed = new Set<string>();

  for (const op of ops) {
    const id = op.cell_id;
    if (id === '') return fail(`${op.operation}: cell_id is required — every operation names the cell it edits`);
    const at = entries.findIndex((e) => e.id === id);

    if (op.operation === 'add') {
      if (at !== -1) return fail(`add: Cell with id="${id}" already exists — use operation "update" to change it`);
      if (ROOT_CELL_IDS.has(id)) return fail(`add: "${id}" is one of draw.io's own layer cells and already exists`);
      const frag = parseFragment(op.new_xml, 'add', id);
      if (!frag.ok) return fail(frag.message);
      // Inherit the indentation of the cell before it, so an added cell reads
      // like the rest of the file rather than being welded to `</root>`.
      const lead = [...entries].reverse().find((e) => e.text !== '')?.lead ?? scan.tailLead;
      entries = [...entries, { ...frag.entry, lead }];
      added.push(id);
      removed.delete(id);
      continue;
    }

    if (op.operation === 'update') {
      if (ROOT_CELL_IDS.has(id)) {
        return fail(`update: "${id}" is one of draw.io's own layer cells, not content — it cannot be edited`);
      }
      if (at === -1) return fail(`update: Cell with id="${id}" not found — call author_read for the current diagram`);
      const frag = parseFragment(op.new_xml, 'update', id);
      if (!frag.ok) return fail(frag.message);
      const next = [...entries];
      next[at] = { ...frag.entry, lead: entries[at].lead };
      entries = next;
      updated.push(id);
      continue;
    }

    if (ROOT_CELL_IDS.has(id)) return fail(`delete: Cannot delete root cell "${id}"`);
    if (at === -1) {
      if (removed.has(id)) continue;
      return fail(`delete: Cell with id="${id}" not found — call author_read for the current diagram`);
    }
    const going = cascadeFrom(entries, id);
    entries = entries.map((e) => (e.id !== '' && going.has(e.id) ? residueEntry(leadResidue(e.lead)) : e));
    for (const gone of going) {
      removed.add(gone);
      deleted.push(gone);
      if (gone !== id) cascaded.push(gone);
    }
  }

  return { ok: true, result: { xml: rebuild(scan, entries), added, updated, deleted, cascaded } };
}

/// The one-line summary the agent gets back, and the reason `cascaded` is
/// carried at all: "removed 1 cell" and "removed 1 cell, and 4 more that hung
/// off it" are different facts about the user's diagram.
export function diagramOpsSummary(r: DiagramOpsResult): string {
  const parts: string[] = [];
  if (r.added.length > 0) parts.push(`added ${r.added.join(', ')}`);
  if (r.updated.length > 0) parts.push(`updated ${r.updated.join(', ')}`);
  if (r.deleted.length > 0) parts.push(`deleted ${r.deleted.join(', ')}`);
  const head = parts.length > 0 ? parts.join('; ') : 'no cells changed';
  if (r.cascaded.length === 0) return head;
  return `${head} (${r.cascaded.join(', ')} ${r.cascaded.length === 1 ? 'was' : 'were'} removed by cascade — connected to a deleted cell)`;
}

/// Narrow an agent-supplied `operations` argument. Returns the message to refuse
/// with rather than a bare null: every rejection here is a shape an agent can
/// fix, and "operations must be an array" costs it a round trip that
/// "operations[2].cell_id must be a non-empty string" does not.
export function narrowDiagramOperations(value: unknown): { ok: true; ops: DiagramOperation[] } | { ok: false; message: string } {
  if (!Array.isArray(value)) {
    return { ok: false, message: "operations must be an array of {operation, cell_id, new_xml} entries when mode is 'ops'" };
  }
  const ops: DiagramOperation[] = [];
  for (const [i, raw] of value.entries()) {
    if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) {
      return { ok: false, message: `operations[${String(i)}] must be an object {operation, cell_id, new_xml}` };
    }
    const v = raw as Record<string, unknown>;
    const kind = v.operation;
    if (kind !== 'add' && kind !== 'update' && kind !== 'delete') {
      return { ok: false, message: `operations[${String(i)}].operation must be 'add', 'update' or 'delete' (got ${JSON.stringify(kind) ?? 'undefined'})` };
    }
    const cellId = typeof v.cell_id === 'string' ? v.cell_id : '';
    if (cellId === '') {
      return { ok: false, message: `operations[${String(i)}].cell_id must be a non-empty string — the id of the cell this operation edits` };
    }
    const newXml = typeof v.new_xml === 'string' ? v.new_xml : '';
    if (kind !== 'delete' && newXml === '') {
      return { ok: false, message: `operations[${String(i)}].new_xml is required for ${kind} — the cell's full element source` };
    }
    ops.push({ operation: kind, cell_id: cellId, new_xml: newXml });
  }
  return { ok: true, ops };
}

/// Total agent-supplied bytes in a batch — what the size cap and the approval
/// card count. The document's own bytes are not in it: an op batch's cost to the
/// user is what the agent is adding, not how big their diagram already is.
export function diagramOpsBytes(ops: readonly DiagramOperation[]): number {
  return ops.reduce((n, o) => n + o.cell_id.length + o.new_xml.length, 0);
}
