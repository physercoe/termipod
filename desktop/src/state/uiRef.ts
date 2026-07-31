/// The UIRef grammar (D6 — docs/plans/desktop-ui-context-and-pointing.md
/// §3.4b, ADR-062 D-2). Deixis is symmetric: the user points at the agent with
/// the annotation overlay, and the agent points back — as a clickable chip in
/// the transcript, and as an ephemeral highlight over the surface itself.
///
/// Both directions need one written form for "this thing on screen". ADR-062
/// defines the UIRef as structured JSON; agents write prose, so the wire form
/// an agent can TYPE is a URI over the same fields:
///
///     ui://replay?dataset_id=ds_1&episode_id=ep_2&cursor=1234
///     ui://debug?file=src/foo.ts&selection=42,58
///     ui://read?tab_id=wt_3
///
/// The scheme is the one already minted for the focus resource (`ui://focus`),
/// so nothing new is introduced — the surface takes the host slot and the
/// entity ids are query params. The JSON form (what `ui_highlight` takes as an
/// argument) converts to and from it, so a tool call and a transcript chip
/// point at the same thing by construction.
///
/// Import-free on purpose: the transcript renderer, the highlight overlay and
/// the `node --test` suites all read it, and the Electron half converts the
/// tool's JSON through the same code.

/// A reference to something on the desktop's screen. `surface` is a workbench
/// job id (or a pseudo-surface — `kimiweb`, `vault`); `params` are ids, paths,
/// fragment-stripped URLs and coordinates. Never content: a UIRef is a JOIN
/// KEY into the entity graph the agent already reaches with its own tools, not
/// a data export (ADR-062 D-2).
export interface UiRef {
  surface: string;
  params: Record<string, string>;
}

/// Params are `key=value` with a small vocabulary; anything longer than this
/// is not a reference. The cap also bounds what a malicious agent can push
/// into a chip label.
const MAX_PARAM_LEN = 200;
const MAX_PARAMS = 8;
const MAX_SURFACE_LEN = 32;

/// Surfaces are workbench job ids: lowercase word characters. Anything else is
/// not parsed — the policy table is keyed by these, and a ref we cannot key is
/// a ref we cannot govern.
const SURFACE_RE = /^[a-z][a-z0-9_-]*$/;

/// The token as it appears in agent prose. Deliberately strict about the tail:
/// a trailing `.` or `)` belongs to the sentence, not the ref.
export const UI_REF_TOKEN_RE = /ui:\/\/[a-z][a-z0-9_-]*(?:\?[A-Za-z0-9_=&,.:/@%+-]*)?/g;

/// decodeURIComponent throws on malformed escapes (`%`, `%zz`) — and this
/// grammar reads AGENT PROSE, where "100% done" is a sentence, not an
/// escape. A throw here would take the whole transcript surface down on
/// render, and the message persists, so it would crash again on every
/// re-mount. A pair we cannot decode is junk, not an exception.
function safeDecode(part: string): string | null {
  try {
    return decodeURIComponent(part);
  } catch {
    return null;
  }
}

export function parseUiRefUri(uri: string): UiRef | null {
  if (!uri.startsWith('ui://')) return null;
  const rest = uri.slice('ui://'.length);
  const q = rest.indexOf('?');
  const surface = (q < 0 ? rest : rest.slice(0, q)).trim();
  if (surface === '' || surface.length > MAX_SURFACE_LEN || !SURFACE_RE.test(surface)) return null;
  const params: Record<string, string> = {};
  if (q >= 0) {
    for (const pair of rest.slice(q + 1).split('&')) {
      if (pair === '') continue;
      const eq = pair.indexOf('=');
      if (eq <= 0) continue;
      const key = safeDecode(pair.slice(0, eq));
      const value = safeDecode(pair.slice(eq + 1));
      if (key === null || value === null || key === '' || value === '' || value.length > MAX_PARAM_LEN) continue;
      if (Object.keys(params).length >= MAX_PARAMS) break;
      params[key] = value;
    }
  }
  return { surface, params };
}

export function formatUiRefUri(ref: UiRef): string {
  const entries = Object.entries(ref.params).filter(([k, v]) => k !== '' && v !== '');
  if (entries.length === 0) return `ui://${ref.surface}`;
  const query = entries.map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`).join('&');
  return `ui://${ref.surface}?${query}`;
}

// ── The JSON form (ADR-062 D-2 / the ui_highlight argument) ──────────────────

/// Convert the ADR's nested JSON shape into the flat one. The blocks are the
/// same vocabulary the focus projection emits (`ui_policy.ts`), so an agent
/// can hand back a slice of what `ui_get_focus` gave it and have it resolve.
/// Unknown blocks are ignored rather than rejected: a ref is a best-effort
/// pointer, and refusing the whole thing over one stray key would make the
/// round trip brittle.
export function uiRefFromJson(value: unknown): UiRef | null {
  if (typeof value === 'string') return parseUiRefUri(value);
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null;
  const obj = value as Record<string, unknown>;
  const surface = obj.surface;
  if (typeof surface !== 'string' || !SURFACE_RE.test(surface) || surface.length > MAX_SURFACE_LEN) return null;
  const params: Record<string, string> = {};
  const take = (key: string, raw: unknown): void => {
    if (Object.keys(params).length >= MAX_PARAMS) return;
    if (typeof raw === 'string' && raw !== '' && raw.length <= MAX_PARAM_LEN) params[key] = raw;
    else if (typeof raw === 'number' && Number.isFinite(raw)) params[key] = String(raw);
    else if (Array.isArray(raw) && raw.every((n) => typeof n === 'number')) params[key] = raw.join(',');
  };
  for (const [key, raw] of Object.entries(obj)) {
    if (key === 'surface') continue;
    if (raw !== null && typeof raw === 'object' && !Array.isArray(raw)) {
      // A block: `{ entity: {…} }`, `{ path: {…} }` — flatten its fields.
      for (const [sub, subRaw] of Object.entries(raw as Record<string, unknown>)) take(sub, subRaw);
    } else {
      take(key, raw);
    }
  }
  return { surface, params };
}

// ── Rendering ────────────────────────────────────────────────────────────────

/// The chip's label. Reads as the plan writes it: `replay · ep_… @ 1234`,
/// `src/foo.ts:42`. Falls back to the surface name, never to an empty chip.
export function uiRefLabel(ref: UiRef): string {
  const p = ref.params;
  const file = p.file ?? p.path;
  if (file !== undefined) {
    const line = firstLine(p.selection);
    return line === null ? file : `${file}:${String(line)}`;
  }
  const entity = p.episode_id ?? p.dataset_id ?? p.run_id ?? p.task_id ?? p.project_id ?? p.document_id ?? p.agent_id;
  if (entity !== undefined) {
    const cursor = p.cursor;
    return cursor !== undefined ? `${ref.surface} · ${entity} @ ${cursor}` : `${ref.surface} · ${entity}`;
  }
  const tab = p.tab_id ?? p.pane_id;
  return tab !== undefined ? `${ref.surface} · ${tab}` : ref.surface;
}

function firstLine(selection: string | undefined): number | null {
  if (selection === undefined) return null;
  const head = Number.parseInt(selection.split(',')[0] ?? '', 10);
  return Number.isFinite(head) ? head : null;
}

/// Rewrite bare `ui://…` tokens in agent prose into markdown links, so the
/// transcript's existing link renderer can paint them as chips.
///
/// Done as a string pre-pass rather than a markdown plugin because it must be
/// provable without a renderer, and because the ONE thing it must never do is
/// touch code: an agent explaining a URI inside a fence is showing it, not
/// pointing with it. Fenced blocks and inline-code spans are skipped verbatim.
export function linkifyUiRefs(text: string): string {
  let out = '';
  for (const segment of splitCodeSpans(text)) {
    if (segment.code) {
      out += segment.text;
      continue;
    }
    out += segment.text.replace(UI_REF_TOKEN_RE, (token) => {
      const ref = parseUiRefUri(token);
      // A token we cannot parse stays prose — better a literal string than a
      // chip that points nowhere.
      return ref === null ? token : `[${uiRefLabel(ref)}](${token})`;
    });
  }
  return out;
}

interface Segment {
  text: string;
  code: boolean;
}

/// Split on fenced blocks (``` … ```) and inline code spans (` … `). Kept
/// deliberately simple — it only has to be conservative: anything it calls
/// code is passed through untouched, which is the safe direction.
function splitCodeSpans(text: string): Segment[] {
  const out: Segment[] = [];
  const re = /```[\s\S]*?(?:```|$)|`[^`\n]*`/g;
  let last = 0;
  for (let m = re.exec(text); m !== null; m = re.exec(text)) {
    if (m.index > last) out.push({ text: text.slice(last, m.index), code: false });
    out.push({ text: m[0], code: true });
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push({ text: text.slice(last), code: false });
  return out;
}
