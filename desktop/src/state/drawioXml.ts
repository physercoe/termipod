/*
 * Portions of this file are derived from next-ai-draw-io
 * (https://github.com/DayuanJiang/next-ai-draw-io), `lib/utils.ts`.
 * Copyright 2025 Dayuan Jiang.
 * Licensed under the Apache License, Version 2.0.
 *
 * Ported and adapted for TermiPod (coworking lane C1): the validator, its
 * helpers and `wrapWithMxFile`. Diagnostic message text is kept verbatim —
 * those strings are read by an agent, and their wording (naming the fix, not
 * just the fault) is the part that makes the loop converge.
 *
 * See the repository NOTICE for attribution.
 */

/// draw.io XML validation for `author_apply {kind:'diagram'}` (coworking C1,
/// ADR-064: *"malformed writes refuse with the validator's diagnosis and change
/// nothing"*).
///
/// **`autoFixXml` is deliberately NOT ported.** Upstream's repair pass is
/// excellent for its own use — an LLM generating a diagram from scratch, where
/// a mangled shape is worth silently repairing. Its last-resort loop deletes
/// `mxCell` elements one at a time until the document parses, reporting it only
/// as a line in a `fixes` array. In *this* codebase `author_apply` writes into
/// a document the user owns, so that same loop deletes the user's shapes and
/// calls it a success — the exact silent-loss class ADR-064 D5 bans, and the
/// same shape as the `parseTable` hole A5 exists to close. Repair can land with
/// lane A/D, where each of the 26 fixes can be judged individually and any that
/// drops content becomes a refusal instead.
///
/// So: validate, refuse, and hand the agent a diagnosis it can act on.

/// Attributes whose duplication is structural rather than cosmetic — a repeated
/// `style` is sloppy, a repeated `id` or `parent` changes what the cell IS.
const STRUCTURAL_ATTRS = ['id', 'parent', 'source', 'target', 'edge', 'vertex', 'as'];

const VALID_ENTITIES = new Set(['lt', 'gt', 'amp', 'quot', 'apos']);

/// Above this the regex sweeps get expensive. Not a refusal — a large diagram
/// is legitimate — just the point where we stop pretending validation is free.
const MAX_XML_SIZE = 1_000_000;

interface ParsedTag {
  tagName: string;
  isClosing: boolean;
  isSelfClosing: boolean;
}

/// Split into tags while respecting quoted attribute values. A plain
/// `/<[^>]+>/` splits `style="a>b"` in the middle of the value and then reports
/// a tag mismatch that is not there.
function parseXmlTags(xml: string): ParsedTag[] {
  const tags: ParsedTag[] = [];
  let i = 0;
  while (i < xml.length) {
    const tagStart = xml.indexOf('<', i);
    if (tagStart === -1) break;
    let tagEnd = tagStart + 1;
    let inQuote = false;
    let quoteChar = '';
    while (tagEnd < xml.length) {
      const c = xml[tagEnd];
      if (inQuote) {
        if (c === quoteChar) inQuote = false;
      } else if (c === '"' || c === "'") {
        inQuote = true;
        quoteChar = c;
      } else if (c === '>') {
        break;
      }
      tagEnd++;
    }
    if (tagEnd >= xml.length) break;
    const tag = xml.substring(tagStart, tagEnd + 1);
    i = tagEnd + 1;
    const m = /^<(\/?)([a-zA-Z][a-zA-Z0-9:_-]*)/.exec(tag);
    if (m === null) continue;
    tags.push({ tagName: m[2], isClosing: m[1] === '/', isSelfClosing: tag.endsWith('/>') });
  }
  return tags;
}

function checkDuplicateAttributes(xml: string): string | null {
  const structural = new Set(STRUCTURAL_ATTRS);
  const tagPattern = /<[^>]+>/g;
  let tagMatch: RegExpExecArray | null;
  while ((tagMatch = tagPattern.exec(xml)) !== null) {
    const attrPattern = /\s([a-zA-Z_:][a-zA-Z0-9_:.-]*)\s*=/g;
    const counts = new Map<string, number>();
    let attrMatch: RegExpExecArray | null;
    while ((attrMatch = attrPattern.exec(tagMatch[0])) !== null) {
      counts.set(attrMatch[1], (counts.get(attrMatch[1]) ?? 0) + 1);
    }
    const dups = [...counts.entries()].filter(([n, c]) => c > 1 && structural.has(n)).map(([n]) => n);
    if (dups.length > 0) {
      return `Invalid XML: Duplicate structural attribute(s): ${dups.join(', ')}. Remove duplicate attributes.`;
    }
  }
  return null;
}

function checkDuplicateIds(xml: string): string | null {
  const idPattern = /\bid\s*=\s*["']([^"']+)["']/gi;
  const ids = new Map<string, number>();
  let m: RegExpExecArray | null;
  while ((m = idPattern.exec(xml)) !== null) ids.set(m[1], (ids.get(m[1]) ?? 0) + 1);
  const dups = [...ids.entries()].filter(([, c]) => c > 1).map(([id, c]) => `'${id}' (${c}x)`);
  if (dups.length > 0) {
    return `Invalid XML: Found duplicate ID(s): ${dups.slice(0, 3).join(', ')}. All id attributes must be unique.`;
  }
  return null;
}

function checkTagMismatches(xml: string): string | null {
  const tags = parseXmlTags(xml.replace(/<!--[\s\S]*?-->/g, ''));
  const stack: string[] = [];
  for (const { tagName, isClosing, isSelfClosing } of tags) {
    if (isClosing) {
      if (stack.length === 0) return `Invalid XML: Closing tag </${tagName}> without matching opening tag`;
      const expected = stack.pop();
      if (expected?.toLowerCase() !== tagName.toLowerCase()) {
        return `Invalid XML: Expected closing tag </${expected}> but found </${tagName}>`;
      }
    } else if (!isSelfClosing) {
      stack.push(tagName);
    }
  }
  if (stack.length > 0) {
    return `Invalid XML: Document has ${stack.length} unclosed tag(s): ${stack.join(', ')}`;
  }
  return null;
}

function checkCharacterReferences(xml: string): string | null {
  const pattern = /&#x?[^;]+;?/g;
  let m: RegExpExecArray | null;
  while ((m = pattern.exec(xml)) !== null) {
    const ref = m[0];
    if (ref.startsWith('&#x')) {
      if (!ref.endsWith(';')) return `Invalid XML: Missing semicolon after hex reference: ${ref}`;
      const digits = ref.substring(3, ref.length - 1);
      if (digits.length === 0 || !/^[0-9a-fA-F]+$/.test(digits)) {
        return `Invalid XML: Invalid hex character reference: ${ref}`;
      }
    } else if (ref.startsWith('&#')) {
      if (!ref.endsWith(';')) return `Invalid XML: Missing semicolon after decimal reference: ${ref}`;
      const digits = ref.substring(2, ref.length - 1);
      if (digits.length === 0 || !/^[0-9]+$/.test(digits)) {
        return `Invalid XML: Invalid decimal character reference: ${ref}`;
      }
    }
  }
  return null;
}

function checkEntityReferences(xml: string): string | null {
  const body = xml.replace(/<!--[\s\S]*?-->/g, '');
  // No /g on a `.test()`: harmless here (a literal mints a fresh RegExp per
  // call, and this one is tested once), but a stateful `lastIndex` on a
  // predicate is a trap waiting for the next person who calls it twice.
  if (/&(?!(?:lt|gt|amp|quot|apos|#))/.test(body)) {
    return 'Invalid XML: Found unescaped & character(s). Replace & with &amp;';
  }
  const pattern = /&([a-zA-Z][a-zA-Z0-9]*);/g;
  let m: RegExpExecArray | null;
  while ((m = pattern.exec(body)) !== null) {
    if (!VALID_ENTITIES.has(m[1])) {
      return `Invalid XML: Invalid entity reference: &${m[1]}; - use only valid XML entities (lt, gt, amp, quot, apos)`;
    }
  }
  return null;
}

function checkNestedMxCells(xml: string): string | null {
  const pattern = /<\/?mxCell[^>]*>/g;
  const stack: number[] = [];
  let m: RegExpExecArray | null;
  while ((m = pattern.exec(xml)) !== null) {
    const tag = m[0];
    if (tag.startsWith('</mxCell>')) {
      if (stack.length > 0) stack.pop();
    } else if (!tag.endsWith('/>')) {
      // A cell's own `valueLabel`/`geometry` children are legitimately inside
      // it; only cell-inside-cell is the error.
      if (!/\sas\s*=\s*["'](valueLabel|geometry)["']/.test(tag)) {
        stack.push(m.index);
        if (stack.length > 1) {
          return 'Invalid XML: Found nested mxCell tags. Cells should be siblings, not nested inside other mxCell elements.';
        }
      }
    }
  }
  return null;
}

/// The DOM half — the most accurate check, and the only one that catches a
/// generic syntax error. Returns `undefined` when no parser is available.
///
/// Split out and given the constructor explicitly for one reason: `DOMParser`
/// exists in the renderer and not under `node --test`, so upstream's
/// try/catch-and-fall-through means the suite silently exercises a DIFFERENT
/// code path than production. Injecting it lets the tests drive both.
export function domCheck(xml: string, Parser: typeof DOMParser | undefined): string | null | undefined {
  if (Parser === undefined) return undefined;
  let doc: Document;
  try {
    doc = new Parser().parseFromString(xml, 'text/xml');
  } catch {
    return undefined;
  }
  if (doc.querySelector('parsererror') !== null) {
    return 'Invalid XML: The XML contains syntax errors (likely unescaped special characters like <, >, & in attribute values). Please escape special characters: use &lt; for <, &gt; for >, &amp; for &, &quot; for ". Regenerate the diagram with properly escaped values.';
  }
  for (const cell of doc.querySelectorAll('mxCell')) {
    if (cell.parentElement?.tagName === 'mxCell') {
      const id = cell.getAttribute('id') ?? 'unknown';
      return `Invalid XML: Found nested mxCell (id="${id}"). Cells should be siblings, not nested inside other mxCell elements.`;
    }
  }
  return null;
}

/// The parser-free half: ten checks that hold with or without a DOM. Exported
/// so the suite can assert each one on its own; `validateMxCell` is what
/// callers use.
export function structuralCheck(xml: string): string | null {
  if (/^\s*<!\[CDATA\[/.test(xml)) {
    return 'Invalid XML: XML is wrapped in CDATA section - remove <![CDATA[ from start and ]]> from end';
  }
  const dupAttr = checkDuplicateAttributes(xml);
  if (dupAttr !== null) return dupAttr;

  const attrValues = /=\s*"([^"]*)"/g;
  let av: RegExpExecArray | null;
  while ((av = attrValues.exec(xml)) !== null) {
    if (/</.test(av[1]) && !/&lt;/.test(av[1])) {
      return 'Invalid XML: Unescaped < character in attribute values. Replace < with &lt;';
    }
  }

  const dupId = checkDuplicateIds(xml);
  if (dupId !== null) return dupId;
  const mismatch = checkTagMismatches(xml);
  if (mismatch !== null) return mismatch;
  const charRef = checkCharacterReferences(xml);
  if (charRef !== null) return charRef;

  const comments = /<!--([\s\S]*?)-->/g;
  let c: RegExpExecArray | null;
  while ((c = comments.exec(xml)) !== null) {
    if (/--/.test(c[1])) return 'Invalid XML: Comment contains -- (double hyphen) which is not allowed';
  }

  const entity = checkEntityReferences(xml);
  if (entity !== null) return entity;

  if (/<mxCell[^>]*\sid\s*=\s*["']\s*["'][^>]*>/.test(xml)) {
    return 'Invalid XML: Found mxCell element(s) with empty id attribute';
  }
  return checkNestedMxCells(xml);
}

/// Validate draw.io XML. `null` means valid; a string is the diagnosis to hand
/// back to the agent.
///
/// Both halves run, DOM first, and **neither is a superset of the other**:
///
///   - the structural checks catch what a well-formed document can still get
///     wrong (duplicate ids, a bare `&`, a `--` inside a comment), so skipping
///     them when a parser exists would make validation weaker in the renderer
///     than under test;
///   - the DOM check catches a **self-closing nested `mxCell`**, which the
///     regex sweep cannot: it tracks a stack of *opening* cell tags, and a
///     self-closing tag opens nothing.
///
/// That second point is why lane A must run this in the RENDERER, not main
/// (A2 already notes main has no DOMParser). Without one, a nested cell of that
/// shape validates clean.
export function validateMxCell(xml: string, Parser: typeof DOMParser | undefined = globalThis.DOMParser): string | null {
  if (xml.length > MAX_XML_SIZE) {
    return `Invalid XML: Document is ${xml.length} bytes, over the ${MAX_XML_SIZE}-byte limit. Split the diagram or send fewer cells.`;
  }
  const dom = domCheck(xml, Parser);
  if (typeof dom === 'string') return dom;
  return structuralCheck(xml);
}

const ROOT_CELLS = '<mxCell id="0"/><mxCell id="1" parent="0"/>';

/// Normalise whatever an agent sent into a complete `<mxfile>`. Agents emit
/// every level of this document — a bare list of cells, a `<root>`, an
/// `<mxGraphModel>`, or the whole file — and draw.io only loads the whole file.
///
/// The two root cells (`id="0"` and `id="1"`) are draw.io's own layer scaffold,
/// not content: any the agent supplied are dropped and ours are substituted, so
/// a model that helpfully includes them cannot produce duplicate ids.
export function wrapWithMxFile(xml: string): string {
  if (xml.trim() === '') {
    return `<mxfile><diagram name="Page-1" id="page-1"><mxGraphModel><root>${ROOT_CELLS}</root></mxGraphModel></diagram></mxfile>`;
  }
  if (xml.includes('<mxfile')) return xml;
  if (xml.includes('<mxGraphModel')) return `<mxfile><diagram name="Page-1" id="page-1">${xml}</diagram></mxfile>`;

  let content = xml;
  if (content.includes('<root>')) content = content.replace(/<\/?root>/g, '').trim();

  // Strip a trailing wrapper the model closed around the cells (any provider
  // does this). Only closing tags are stripped — anything else after the last
  // cell is content we must not silently drop, and the validator will refuse it.
  const lastSelfClose = content.lastIndexOf('/>');
  const lastCellClose = content.lastIndexOf('</mxCell>');
  const lastValidEnd = Math.max(lastSelfClose, lastCellClose);
  if (lastValidEnd !== -1) {
    const endOffset = lastCellClose > lastSelfClose ? '</mxCell>'.length : '/>'.length;
    const suffix = content.slice(lastValidEnd + endOffset);
    if (/^(\s*<\/[^>]+>)*\s*$/.test(suffix)) content = content.slice(0, lastValidEnd + endOffset);
  }

  content = content
    .replace(/<mxCell[^>]*\bid=["']0["'][^>]*(?:\/>|><\/mxCell>)/g, '')
    .replace(/<mxCell[^>]*\bid=["']1["'][^>]*(?:\/>|><\/mxCell>)/g, '')
    .trim();

  return `<mxfile><diagram name="Page-1" id="page-1"><mxGraphModel><root>${ROOT_CELLS}${content}</root></mxGraphModel></diagram></mxfile>`;
}

/// The `author_apply` entry point: normalise, then validate what will actually
/// be written. Validating the raw input instead would pass a bare cell list
/// (well-formed on its own) and then write a wrapper that duplicates an id the
/// agent supplied.
export function prepareDiagramBody(xml: string): { xml: string; error: null } | { xml: null; error: string } {
  const wrapped = wrapWithMxFile(xml);
  const error = validateMxCell(wrapped);
  return error === null ? { xml: wrapped, error: null } : { xml: null, error };
}
