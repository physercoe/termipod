import { lazy, Suspense, useDeferredValue, useEffect, useMemo, useRef, useState } from 'react';
import { TableVirtuoso, type ItemProps, type TableComponents } from 'react-virtuoso';
import { useT } from '../i18n';
import {
  hasAnyAttachment,
  isInternalTag,
  primaryAttachment,
  REF_TYPES,
  useLibrary,
  type Attachment,
  type Reference,
  type RefType,
  type WorkLink,
} from '../state/library';
import { hasAttachment, loadAttachmentBlob, useZoteroStorage } from '../state/zoteroStorage';
import {
  activeAttachmentRoot,
  deleteManagedAttachmentFile,
  pickAndCopyAttachment,
  useAttachmentConfig,
  writeNoteImage,
} from '../state/attachments';
import { syncLibrary } from '../state/librarySync';
import { syncAnnotations } from '../state/annotationSync';
import { useSession } from '../state/session';
import {
  detectIdentifier,
  enrichWithUnpaywall,
  lsGet,
  lsSet,
  scrapeMetadata,
  SOURCES,
  sourceById,
  type DiscoveryPaper,
  type ScrapePatch,
  type ScrapeSeed,
} from '../discovery';
import { hostOf, isTauri, revealPath } from '../platform';
import { BrowserView } from './BrowserView';
import { AgentCompanion } from '../ui/AgentCompanion';
import { Markdown } from '../ui/Markdown';
import { MarkdownReader } from '../ui/MarkdownReader';
import { NoteTab } from '../ui/NoteTab';
import { MarkdownEditor } from '../ui/MarkdownEditor';
import { Icon, type IconName } from '../ui/Icon';
import { PasswordInput } from '../ui/PasswordInput';
import { OpenLinkContext, useOpenLink } from '../ui/OpenLinkContext';
import { PdfCanvas } from '../ui/PdfCanvas';
import { useTextPrompt } from '../ui/PromptModal';
// epub.js is heavy and epub is a rare path — lazy-load it into its own chunk.
const EpubView = lazy(() => import('../ui/EpubView').then((m) => ({ default: m.EpubView })));
// Milkdown (ProseMirror) is heavy — load it only when a WYSIWYG editor opens.
const WysiwygEditor = lazy(() => import('../ui/WysiwygEditor').then((m) => ({ default: m.WysiwygEditor })));
import { ResizeHandle, VResizeHandle } from '../ui/ResizeHandle';
import { useContextMenu } from '../ui/ContextMenu';
import { WebdavModal } from '../ui/WebdavModal';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';


const clamp = (n: number, lo: number, hi: number): number => Math.min(hi, Math.max(lo, n));

function loadWidth(key: string, fallback: number): number {
  const v = Number(localStorage.getItem(key));
  return Number.isFinite(v) && v > 0 ? v : fallback;
}
function saveWidth(key: string, v: number): void {
  try {
    localStorage.setItem(key, String(v));
  } catch {
    /* ignore */
  }
}

/// J1 — Read papers/reports in depth, as a **reference library** (Zotero-shaped)
/// fused with **discovery** (Semantic Scholar). Three panes: collections/tags
/// rail · items list · inspector (Info / Read / Notes / Cite). Discover mode
/// searches Semantic Scholar (TLDR + abstract + citations + open-access PDF) and
/// imports a result into the library in one click. Storage is device-local this
/// round; the hub-backed library + PDF blobs + agent-driven extraction (Elicit /
/// Undermind patterns) are specced in `reference-library-and-reading.md`.

type Mode = 'library' | 'discover';
type Tab = 'info' | 'read' | 'notes' | 'cite' | 'meta' | 'assistant';
const ALL = '__all__';

// An open tab in the reader region: a PDF reader (a library item) or an in-app
// browser (an arbitrary URL). `activeTab === null` shows the library instead.
interface ReadTab {
  id: string;
  kind: 'pdf' | 'web' | 'note';
  refId?: string;
  attId?: string; // which attachment of the reference this tab opened
  url?: string;
  title: string;
}
let tabSeq = 0;
function nextTabId(): string {
  tabSeq += 1;
  return `tab${Date.now().toString(36)}${tabSeq}`;
}

// Sortable columns for the Zotero-style library table.
type SortKey = 'title' | 'creator' | 'year' | 'venue' | 'type';
type SortDir = 'asc' | 'desc';
const SORT_COLS: { key: SortKey; labelKey: string }[] = [
  { key: 'title', labelKey: 'read.colTitle' },
  { key: 'creator', labelKey: 'read.colCreator' },
  { key: 'year', labelKey: 'read.colYear' },
  { key: 'venue', labelKey: 'read.colVenue' },
  { key: 'type', labelKey: 'read.colType' },
];
function sortVal(r: Reference, key: SortKey): string | number {
  switch (key) {
    case 'title':
      return r.title.toLowerCase();
    case 'creator':
      return (r.authors[0] ?? '').toLowerCase();
    case 'year':
      return r.year ?? 0;
    case 'venue':
      return (r.venue ?? '').toLowerCase();
    case 'type':
      return r.type;
  }
}

function splitList(s: string, sep: string): string[] {
  return s
    .split(sep)
    .map((x) => x.trim())
    .filter((x) => x !== '');
}

// ── Virtualized library table (#311) ────────────────────────────────────────
// The Zotero-style table renders one <tr> per row; a 5–10k library stalled it.
// TableVirtuoso keeps only the visible rows live. Row-level state/handlers reach
// the (stable, module-level) row component through Virtuoso's `context`, so the
// components never remount — the perf win. The cells come from `itemContent`.
interface LibRowCtx {
  selected: string | null;
  onSelect: (id: string) => void;
  onOpen: (id: string) => void; // open the reader (double-click / Enter with a viewable attachment)
  onMenu: (id: string, x: number, y: number) => void;
  hasPdf: (r: Reference) => boolean;
}

// Custom table wrapper so the column widths (colgroup) + class survive.
const LibTable: TableComponents<Reference, LibRowCtx>['Table'] = (props) => (
  <table {...props} className="read-table">
    <colgroup>
      <col style={{ width: '44%' }} />
      <col style={{ width: '20%' }} />
      <col style={{ width: '3.5rem' }} />
      <col style={{ width: '22%' }} />
      <col style={{ width: '6rem' }} />
    </colgroup>
    {props.children}
  </table>
);

const LibTableRow = (props: ItemProps<Reference> & { context?: LibRowCtx }): JSX.Element => {
  const { item: r, context, children, ...rest } = props;
  const ctx = context as LibRowCtx;
  const isSel = ctx.selected === r.id;
  return (
    <tr
      {...rest}
      className={isSel ? 'active' : ''}
      tabIndex={0}
      aria-selected={isSel}
      onClick={() => ctx.onSelect(r.id)}
      onDoubleClick={() => {
        if (ctx.hasPdf(r)) ctx.onOpen(r.id);
      }}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          if (ctx.hasPdf(r)) ctx.onOpen(r.id);
          else ctx.onSelect(r.id);
        } else if (e.key === ' ') {
          e.preventDefault();
          ctx.onSelect(r.id);
        }
      }}
      onContextMenu={(e) => {
        e.preventDefault();
        ctx.onMenu(r.id, e.clientX, e.clientY);
      }}
    >
      {children}
    </tr>
  );
};

// Stable component map — an inline object would remount every row each render.
const LIB_TABLE_COMPONENTS: TableComponents<Reference, LibRowCtx> = {
  Table: LibTable,
  TableRow: LibTableRow as TableComponents<Reference, LibRowCtx>['TableRow'],
};

// Human labels for the Zotero detail fields; unknown keys fall back to a
// camelCase-split. Keeps acronym fields (ISBN/ISSN/DOI) intact.
const FIELD_LABELS: Record<string, string> = {
  publisher: 'Publisher',
  place: 'Place',
  pages: 'Pages',
  volume: 'Volume',
  issue: 'Issue',
  ISBN: 'ISBN',
  ISSN: 'ISSN',
  series: 'Series',
  edition: 'Edition',
  language: 'Language',
  shortTitle: 'Short title',
  journalAbbreviation: 'Journal abbr.',
  libraryCatalog: 'Library catalog',
  accessDate: 'Accessed',
  extra: 'Extra',
  repository: 'Repository',
  archiveID: 'Archive ID',
  rights: 'Rights',
  numPages: 'Pages',
  numberOfVolumes: 'Volumes',
  company: 'Company',
  websiteType: 'Website type',
  programmingLanguage: 'Language',
  runningTime: 'Running time',
};

function humanizeKey(k: string): string {
  if (FIELD_LABELS[k] !== undefined) return FIELD_LABELS[k];
  const spaced = k.replace(/([a-z])([A-Z])/g, '$1 $2');
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

function paperToRef(p: DiscoveryPaper): Omit<Reference, 'id' | 'addedAt'> {
  return {
    type: p.arxivId !== undefined ? 'preprint' : 'article',
    title: p.title,
    authors: p.authors,
    year: p.year,
    venue: p.venue,
    doi: p.doi,
    arxivId: p.arxivId,
    url: p.url,
    pdfUrl: p.pdfUrl,
    abstract: p.abstract,
    tldr: p.tldr,
    citationCount: p.citationCount,
    source: 'semantic-scholar',
    externalId: p.paperId,
    tags: [],
    collectionIds: [],
    notes: '',
  };
}

// The identifiers the scraper resolves a work from. `externalId` is the OpenAlex
// work URL for OpenAlex-imported items, so pass it as the openAlexId seed.
function refToSeed(r: Reference): ScrapeSeed {
  const oaId = r.externalId !== undefined && /^https?:\/\/openalex\.org\/W\d+$/i.test(r.externalId) ? r.externalId : undefined;
  return { doi: r.doi, arxivId: r.arxivId, openAlexId: oaId, title: r.title, url: r.url, abstract: r.abstract };
}

// Apply a scrape patch to an existing reference: enrichment fields always
// overwrite (they're derived), core bibliographic fields are backfilled only
// when the current value is empty so a re-scrape never clobbers hand edits.
function patchToRefFields(patch: ScrapePatch, cur?: Reference): Partial<Reference> {
  const empty = (v: unknown): boolean => v === undefined || v === '' || (Array.isArray(v) && v.length === 0);
  const out: Partial<Reference> = {
    referenceCount: patch.referenceCount,
    citedByCount: patch.citedByCount,
    references: patch.references,
    citations: patch.citations,
    journal: patch.journal,
    openAccess: patch.openAccess,
    topics: patch.topics,
    resourceLinks: patch.resourceLinks,
    enrichedAt: patch.enrichedAt,
    enrichSource: patch.enrichSource,
  };
  if (patch.title !== undefined && (cur === undefined || empty(cur.title))) out.title = patch.title;
  if (patch.authors !== undefined && (cur === undefined || empty(cur.authors))) out.authors = patch.authors;
  if (patch.year !== undefined && (cur === undefined || cur.year === undefined)) out.year = patch.year;
  if (patch.venue !== undefined && (cur === undefined || empty(cur.venue))) out.venue = patch.venue;
  if (patch.doi !== undefined && (cur === undefined || empty(cur.doi))) out.doi = patch.doi;
  if (patch.arxivId !== undefined && (cur === undefined || empty(cur.arxivId))) out.arxivId = patch.arxivId;
  if (patch.abstract !== undefined && (cur === undefined || empty(cur.abstract))) out.abstract = patch.abstract;
  if (patch.pdfUrl !== undefined && (cur === undefined || empty(cur.pdfUrl))) out.pdfUrl = patch.pdfUrl;
  if (patch.detailsAdd !== undefined) out.details = { ...(cur?.details ?? {}), ...patch.detailsAdd };
  return out;
}

function citeApa(r: Reference): string {
  const authors = r.authors.length > 0 ? r.authors.join(', ') : '—';
  const yr = r.year !== undefined ? ` (${r.year}).` : '.';
  const venue = r.venue !== undefined && r.venue !== '' ? ` ${r.venue}.` : '';
  const doi = r.doi !== undefined ? ` https://doi.org/${r.doi}` : r.url !== undefined ? ` ${r.url}` : '';
  return `${authors}${yr} ${r.title}.${venue}${doi}`.trim();
}

function citeBibtex(r: Reference): string {
  const first = r.authors[0]?.split(/\s+/).pop() ?? 'ref';
  const key = `${first}${r.year ?? ''}`.replace(/[^A-Za-z0-9]/g, '');
  const kind = r.type === 'book' ? 'book' : r.type === 'preprint' ? 'misc' : 'article';
  const lines = [`@${kind}{${key},`, `  title = {${r.title}},`];
  if (r.authors.length > 0) lines.push(`  author = {${r.authors.join(' and ')}},`);
  if (r.year !== undefined) lines.push(`  year = {${r.year}},`);
  if (r.venue !== undefined && r.venue !== '') lines.push(`  journal = {${r.venue}},`);
  if (r.doi !== undefined) lines.push(`  doi = {${r.doi}},`);
  if (r.url !== undefined) lines.push(`  url = {${r.url}},`);
  lines.push('}');
  return lines.join('\n');
}

function copy(text: string): void {
  try {
    void navigator.clipboard?.writeText(text);
  } catch {
    /* ignore — clipboard may be unavailable under the sandbox */
  }
}

// The kinds of attachment the reader can render. PDF + EPUB decode from an
// ArrayBuffer (pdf.js / epub.js); image/video/audio/html render from an object
// URL; text is read as a string.
type ViewKind = 'pdf' | 'epub' | 'image' | 'video' | 'audio' | 'html' | 'text';

const EXT_KIND: Record<string, ViewKind> = {
  pdf: 'pdf',
  epub: 'epub',
  png: 'image', jpg: 'image', jpeg: 'image', gif: 'image', webp: 'image', svg: 'image', avif: 'image', bmp: 'image',
  mp4: 'video', webm: 'video', m4v: 'video', mov: 'video', ogv: 'video',
  mp3: 'audio', m4a: 'audio', wav: 'audio', oga: 'audio', ogg: 'audio', flac: 'audio', aac: 'audio',
  html: 'html', htm: 'html', xhtml: 'html', mht: 'html', mhtml: 'html',
  txt: 'text', md: 'text', markdown: 'text', csv: 'text', tsv: 'text', log: 'text', json: 'text',
};

function viewKindFor(file: string): ViewKind {
  const ext = file.split('.').pop()?.toLowerCase() ?? '';
  return EXT_KIND[ext] ?? 'html'; // unknown → try an iframe; the webview sniffs it
}

// A line icon + human label per attachment kind — used by the info-tab card.
const KIND_ICON: Record<ViewKind, IconName> = {
  pdf: 'file-text', epub: 'book', image: 'image', video: 'film', audio: 'music', html: 'globe', text: 'note',
};
const KIND_LABEL: Record<ViewKind, string> = {
  pdf: 'read.kPdf', epub: 'read.kEpub', image: 'read.kImage', video: 'read.kVideo',
  audio: 'read.kAudio', html: 'read.kHtml', text: 'read.kText',
};

// The resolved, ready-to-render payload for an attachment. Discriminated so the
// render only ever reads the field it set.
type Payload =
  | { t: 'buf'; kind: 'pdf' | 'epub'; buf: ArrayBuffer }
  | { t: 'url'; kind: 'image' | 'video' | 'audio' | 'html'; url: string }
  | { t: 'text'; text: string };

// Viewer for a local attachment. Bytes are resolved from the linked storage
// folder (a live File in the browser build, or read through the Rust core under
// Tauri), then dispatched by type: PDFs render via bundled pdf.js (PdfCanvas) and
// EPUBs via epub.js (EpubView) — canvas/DOM rendering works on every platform,
// unlike the old `<iframe src=blob>` which WebView2 (Windows/Edge) refused
// ("此页面已被 Microsoft Edge 阻止"). Images/video/audio/html render from an object
// URL that is revoked on unmount so bytes aren't retained after the reader closes.
function AttachmentView({
  att,
  referenceId,
  onSaveSelection,
  onImageToNote,
  docUrl,
  detailsOpen,
  onToggleDetails,
}: {
  att: Attachment;
  referenceId?: string;
  onSaveSelection?: (text: string) => void;
  onImageToNote?: (dataUri: string) => void;
  // For the PDF path only: reader-chrome actions rendered inside the PDF toolbar
  // (the reader has no separate title/action row above the PDF).
  docUrl?: string;
  detailsOpen?: boolean;
  onToggleDetails?: () => void;
}): JSX.Element {
  const t = useT();
  const rels = useZoteroStorage((s) => s.rels);
  const files = useZoteroStorage((s) => s.files);
  const path = useZoteroStorage((s) => s.path);
  const [payload, setPayload] = useState<Payload | null>(null);
  // null = loading; 'notfound' = not in the linked folder; 'error' = read/decode
  // failure. These are distinct: only 'notfound' means "check the folder link".
  const [err, setErr] = useState<null | 'notfound' | 'error'>(null);
  const kind = viewKindFor(att.file);
  useEffect(() => {
    let alive = true;
    let made: string | null = null;
    setPayload(null);
    setErr(null);
    void loadAttachmentBlob({ rels, files, path }, att)
      .then(async (blob) => {
        if (!alive) return;
        if (blob === null) {
          setErr('notfound');
          return;
        }
        // Trust a declared PDF content-type even when the extension is unknown.
        const eff: ViewKind = blob.type === 'application/pdf' ? 'pdf' : kind;
        if (eff === 'pdf' || eff === 'epub') {
          const buf = await blob.arrayBuffer();
          if (alive) setPayload({ t: 'buf', kind: eff, buf });
        } else if (eff === 'text') {
          const text = await blob.text();
          if (alive) setPayload({ t: 'text', text });
        } else {
          made = URL.createObjectURL(blob);
          if (alive) setPayload({ t: 'url', kind: eff, url: made });
          else URL.revokeObjectURL(made);
        }
      })
      .catch(() => {
        // A read/decode failure (corrupt file, permission) — NOT a missing file,
        // and previously left the view stuck on "loading" forever.
        if (alive) setErr('error');
      });
    return () => {
      alive = false;
      if (made !== null) URL.revokeObjectURL(made);
    };
  }, [att.key, att.file, rels, files, path, kind]);

  if (err === 'notfound') return <div className="muted region-pad">{t('read.pdfNotFound')}</div>;
  if (err === 'error') return <div className="error region-pad">{t('read.attachmentError')}</div>;
  if (payload === null) return <div className="muted region-pad">{t('read.loadingFile')}</div>;
  if (payload.t === 'buf' && payload.kind === 'pdf')
    return (
      <PdfCanvas
        data={payload.buf}
        fileName={att.file}
        referenceId={referenceId}
        onSaveSelection={onSaveSelection}
        onImageToNote={onImageToNote}
        docUrl={docUrl}
        detailsOpen={detailsOpen}
        onToggleDetails={onToggleDetails}
      />
    );
  if (payload.t === 'buf' && payload.kind === 'epub')
    return (
      <Suspense fallback={<div className="muted region-pad">{t('read.loadingFile')}</div>}>
        <EpubView data={payload.buf} fileName={att.file} onSaveSelection={onSaveSelection} />
      </Suspense>
    );
  if (payload.t === 'text')
    return /\.(md|markdown)$/i.test(att.file) ? (
      // Markdown attachments render as a document: formatted prose (fills the
      // pane — no 76ch cap), inline math (`$…$` + `\(…\)`/`\[…\]`), and a left
      // headings outline (parity with the PDF reader).
      <div className="att-md">
        <MarkdownReader text={payload.text} />
      </div>
    ) : (
      <pre className="att-text region-pad">{payload.text}</pre>
    );
  if (payload.t === 'url' && payload.kind === 'image')
    return (
      <div className="att-image-wrap">
        <img className="att-image" src={payload.url} alt={att.file} />
      </div>
    );
  if (payload.t === 'url' && payload.kind === 'video')
    return <video className="att-media" src={payload.url} controls />;
  if (payload.t === 'url' && payload.kind === 'audio')
    return (
      <div className="att-audio-wrap region-pad">
        <audio src={payload.url} controls />
      </div>
    );
  if (payload.t === 'url') return <iframe className="pdf-frame" title={att.file} src={payload.url} />;
  return <div className="muted region-pad">{t('read.loadingFile')}</div>;
}

// A compact attachment summary for the Info tab: what the linked file is (type +
// extension), where it lives (absolute path under Tauri, or session-linked in the
// browser build, or a "not found / link a folder" hint), and a preview — a
// thumbnail for images, a type glyph otherwise. Reads its own storage slices so
// it stays self-contained.
function AttachmentInfo({
  att,
  onOpen,
  onRemove,
}: {
  att: Attachment;
  onOpen?: () => void;
  onRemove?: () => void;
}): JSX.Element {
  const t = useT();
  const rels = useZoteroStorage((s) => s.rels);
  const files = useZoteroStorage((s) => s.files);
  const path = useZoteroStorage((s) => s.path);
  const storageLinked = useZoteroStorage((s) => s.count > 0);
  const k = `${att.key ?? ''}/${att.file}`;
  // A managed attachment carries its own absolute path (self-resolving); a Zotero
  // one is present only when found in the linked folder's index.
  const managed = att.source === 'managed' && att.path !== undefined && att.path !== '';
  const present = managed || rels.has(k) || files.has(k);
  const kind = viewKindFor(att.file);
  const ext = att.file.split('.').pop()?.toLowerCase() ?? '';
  const rel = rels.get(k);
  // The absolute on-disk path, when known — lets us reveal the file in the OS file
  // manager. Managed files have it directly; Zotero files derive it from the link.
  const absPath = managed
    ? att.path ?? null
    : present && rel !== undefined && path !== null
      ? `${path}/${rel}`
      : null;
  const location = present
    ? absPath ?? t('read.attSession')
    : storageLinked
      ? t('read.attMissing')
      : t('read.attNotLinked');

  // Two-step remove confirm (avoids a mis-click deleting an attachment).
  const [confirming, setConfirming] = useState(false);
  // Thumbnail for image attachments only (cheap; other kinds show a glyph).
  const [thumb, setThumb] = useState<string | null>(null);
  useEffect(() => {
    if (kind !== 'image' || !present) {
      setThumb(null);
      return;
    }
    let alive = true;
    let made: string | null = null;
    void loadAttachmentBlob({ rels, files, path }, att).then((blob) => {
      if (!alive || blob === null) return;
      made = URL.createObjectURL(blob);
      setThumb(made);
    });
    return () => {
      alive = false;
      if (made !== null) URL.revokeObjectURL(made);
    };
  }, [k, kind, present, rels, files, path]);

  return (
      <div className="att-card">
        <div className="att-thumb" data-kind={kind}>
          {thumb !== null ? <img src={thumb} alt={att.file} /> : <Icon name={KIND_ICON[kind]} size={26} className="att-glyph" />}
        </div>
        <div className="att-fields">
          <div className="att-field">
            <span className="att-k">{t('read.attFile')}</span>
            <span className="att-v mono">{att.file}</span>
          </div>
          <div className="att-field">
            <span className="att-k">{t('read.attType')}</span>
            <span className="att-v">
              {t(KIND_LABEL[kind])}
              {ext !== '' ? ` · .${ext}` : ''}
            </span>
          </div>
          <div className="att-field">
            <span className="att-k">{t('read.attLocation')}</span>
            <span className={present ? 'att-v mono' : 'att-v mono muted'}>{location}</span>
            {absPath !== null && (
              <button className="link-btn att-reveal" title={t('read.attReveal')} onClick={() => revealPath(absPath)}>
                <Icon name="folder" size={14} />
              </button>
            )}
          </div>
          <div className="att-actions">
            {absPath !== null && (
              <button className="small att-locate" onClick={() => revealPath(absPath)}>
                <Icon name="folder" size={14} />
                {t('read.attReveal')}
              </button>
            )}
            {present && onOpen !== undefined && (
              <button className="primary small att-open" onClick={onOpen}>
                <Icon name="window" />
                {t('read.attOpen')}
              </button>
            )}
            {onRemove !== undefined &&
              (confirming ? (
                <span className="att-confirm">
                  <span className="muted small">{t('read.attRemoveConfirm')}</span>
                  <button
                    className="small att-remove"
                    onClick={() => {
                      setConfirming(false);
                      onRemove();
                    }}
                  >
                    {t('read.confirmDeleteYes')}
                  </button>
                  <button className="small" onClick={() => setConfirming(false)}>
                    {t('common.cancel')}
                  </button>
                </span>
              ) : (
                <button className="small att-remove" title={t('read.attRemove')} onClick={() => setConfirming(true)}>
                  <Icon name="trash" size={14} />
                  {t('read.attRemove')}
                </button>
              ))}
          </div>
        </div>
      </div>
  );
}

// ---- Inspector -------------------------------------------------------------

// A list of works in the citation graph (references or citations). Each title
// opens in the in-app browser tab (DOI landing or OpenAlex page).
// `total` is the full count from the citation graph (cited_by_count /
// referenceCount). The list itself is only a sample (top-cited, capped in the
// scraper), so when total > sample we say "showing N of TOTAL" — otherwise the
// bare "(50)" reads as a hard cap hiding the rest.
function WorkList({ label, works, total }: { label: string; works?: WorkLink[]; total?: number }): JSX.Element | null {
  const t = useT();
  const openLink = useOpenLink();
  if (works === undefined || works.length === 0) return null;
  const href = (w: WorkLink): string => (w.doi !== undefined ? `https://doi.org/${w.doi}` : (w.id ?? ''));
  const count =
    total !== undefined && total > works.length
      ? t('read.workSample').replace('{n}', String(works.length)).replace('{total}', total.toLocaleString())
      : String(works.length);
  return (
    <div className="ref-meta-sec">
      <div className="ref-section-label">
        {label} <span className="muted small">({count})</span>
      </div>
      <ul className="ref-worklist">
        {works.map((w, i) => (
          <li key={(w.id ?? '') + i} className="ref-work">
            <button className="ref-work-title" disabled={href(w) === ''} onClick={() => openLink(href(w))}>
              {w.title}
            </button>
            {w.year !== undefined && <span className="muted small ref-work-year">· {w.year}</span>}
          </li>
        ))}
      </ul>
    </div>
  );
}

// The Meta tab: the rich metadata the plain form doesn't cover — citation-graph
// counts, journal metrics (an IF-like signal), open-access status, topics,
// code/data links, and the reference + cited-by lists. Populated by the scraper.
// NOTE: the prop is `reference`, not `ref` — `ref` is a React-reserved prop that
// is never passed to a function component, so a prop literally named `ref` arrives
// as `undefined` and the first `ref.` access throws (blanking the whole app when
// there is no error boundary). We alias it to a local `ref` to keep the body terse.
function RefMeta({
  reference: ref,
  scraping,
  msg,
  onScrape,
}: {
  reference: Reference;
  scraping: boolean;
  msg: string | null;
  onScrape: () => void;
}): JSX.Element {
  const t = useT();
  const openLink = useOpenLink();
  const j = ref.journal;
  const cited = ref.citedByCount ?? ref.citationCount;
  const enriched = ref.enrichedAt !== undefined;
  const hasMetrics = cited !== undefined || ref.referenceCount !== undefined || j?.twoYearMeanCitedness !== undefined;
  return (
    <div className="ref-meta region-pad">
      <div className="ref-meta-actions">
        <button className="primary small" disabled={scraping} onClick={onScrape}>
          {scraping ? t('read.scraping') : enriched ? t('read.rescrape') : t('read.scrape')}
        </button>
        {enriched && (
          <span className="muted small">
            {t('read.enrichedVia')
              .replace('{src}', ref.enrichSource ?? '')
              .replace('{time}', new Date(ref.enrichedAt ?? 0).toLocaleDateString())}
          </span>
        )}
      </div>
      {msg !== null && <div className="ref-meta-msg muted small">{msg}</div>}
      {!enriched && msg === null && <div className="muted small">{t('read.scrapeHint')}</div>}

      {hasMetrics && (
        <div className="ref-metrics">
          {cited !== undefined && (
            <div className="ref-metric">
              <span className="ref-metric-n">{cited}</span>
              <span className="ref-metric-l">{t('read.mCitedBy')}</span>
            </div>
          )}
          {ref.referenceCount !== undefined && (
            <div className="ref-metric">
              <span className="ref-metric-n">{ref.referenceCount}</span>
              <span className="ref-metric-l">{t('read.mReferences')}</span>
            </div>
          )}
          {j?.twoYearMeanCitedness !== undefined && (
            <div className="ref-metric" title={t('read.mImpactHint')}>
              <span className="ref-metric-n">{j.twoYearMeanCitedness.toFixed(1)}</span>
              <span className="ref-metric-l">{t('read.mImpact')}</span>
            </div>
          )}
          {j?.hIndex !== undefined && (
            <div className="ref-metric" title={j.name ?? ''}>
              <span className="ref-metric-n">{j.hIndex}</span>
              <span className="ref-metric-l">{t('read.mHindex')}</span>
            </div>
          )}
        </div>
      )}
      {j?.name !== undefined && (
        <div className="muted small">
          {t('read.mJournal').replace('{name}', j.name)}
          {j.isOa === true ? ' · OA' : ''}
        </div>
      )}

      {ref.topics !== undefined && ref.topics.length > 0 && (
        <div className="ref-chips">
          {ref.topics.map((tp) => (
            <span key={tp} className="ref-chip">
              {tp}
            </span>
          ))}
        </div>
      )}

      {ref.resourceLinks !== undefined && ref.resourceLinks.length > 0 && (
        <div className="ref-meta-sec">
          <div className="ref-section-label">{t('read.mResources')}</div>
          {ref.resourceLinks.map((l) => (
            <button key={l.url} className="ref-reslink" onClick={() => openLink(l.url)}>
              <span className={`pill small${l.kind === 'code' ? ' ok' : ''}`}>{l.kind}</span>
              <span className="ref-reslink-host">{l.host}</span>
              <span className="ref-reslink-url mono">{l.url}</span>
            </button>
          ))}
        </div>
      )}

      <WorkList label={t('read.mRefList')} works={ref.references} total={ref.referenceCount} />
      <WorkList label={t('read.mCiteList')} works={ref.citations} total={ref.citedByCount ?? ref.citationCount} />
    </div>
  );
}

function Inspector({
  refId,
  onOpenReader,
  onOpenNote,
  onCollapse,
  embedded,
}: {
  refId: string;
  onOpenReader?: (id: string, attId?: string) => void;
  onOpenNote?: (refId: string) => void;
  onCollapse?: () => void;
  embedded?: boolean;
}): JSX.Element {
  const t = useT();
  const ref = useLibrary((s) => s.references.find((r) => r.id === refId));
  const collections = useLibrary((s) => s.collections);
  const update = useLibrary((s) => s.updateReference);
  const remove = useLibrary((s) => s.removeReference);
  const addAttachmentToRef = useLibrary((s) => s.addAttachment);
  const removeAttachmentFromRef = useLibrary((s) => s.removeAttachment);
  const [attBusy, setAttBusy] = useState(false);
  const [attErr, setAttErr] = useState<string | null>(null);
  const [notesMode, setNotesMode] = useState<'wysiwyg' | 'source' | 'preview'>(
    () => (localStorage.getItem('termipod.read.notesMode') as 'wysiwyg' | 'source' | 'preview') || 'wysiwyg',
  );
  function pickNotesMode(m: 'wysiwyg' | 'source' | 'preview'): void {
    setNotesMode(m);
    try {
      localStorage.setItem('termipod.read.notesMode', m);
    } catch {
      /* ignore */
    }
  }

  async function exportNotes(): Promise<void> {
    if (ref === undefined || !isTauri()) return;
    const base = (ref.title !== '' ? ref.title : 'note').slice(0, 60).replace(/[^\w.-]+/g, '-');
    try {
      const { invoke } = await import('@tauri-apps/api/core');
      await invoke('doc_save', { content: ref.notes, defaultName: `${base}.md` });
    } catch {
      /* cancelled / unavailable */
    }
  }
  const rels = useZoteroStorage((s) => s.rels);
  const files = useZoteroStorage((s) => s.files);
  const storageLinked = useZoteroStorage((s) => s.count > 0);
  const openLink = useOpenLink();
  const [tab, setTab] = useState<Tab>('info');
  // Edit vs preview for the reading body is an EXPLICIT state, not derived from
  // whether the body is empty — deriving it flips to preview on the first
  // keystroke (the block would go read-only after one character). Default to
  // editing when the body starts empty.
  const [editingBody, setEditingBody] = useState(false);
  // In-app delete confirmation. `window.confirm` is unreliable in the Tauri
  // webview (WebView2 returns without showing a dialog → the item deleted with no
  // prompt), so the confirm is an explicit two-step inline state instead.
  const [confirming, setConfirming] = useState(false);
  // The scraper enriches the item with citation-graph + metrics + code/data links.
  const [scraping, setScraping] = useState(false);
  const [scrapeMsg, setScrapeMsg] = useState<string | null>(null);
  useEffect(() => {
    const b = useLibrary.getState().references.find((r) => r.id === refId)?.bodyMarkdown ?? '';
    setEditingBody(b === '');
    setConfirming(false);
    setScrapeMsg(null);
  }, [refId]);

  async function runScrape(): Promise<void> {
    const cur = useLibrary.getState().references.find((r) => r.id === refId);
    if (cur === undefined) return;
    setScraping(true);
    setScrapeMsg(null);
    try {
      const res = await scrapeMetadata(refToSeed(cur));
      if (res.patch === null) {
        setScrapeMsg(t('read.scrapeNone'));
      } else {
        update(cur.id, patchToRefFields(res.patch, cur));
        setScrapeMsg(
          res.found.length > 0 ? t('read.scrapeDone').replace('{what}', res.found.join(', ')) : t('read.scrapeThin'),
        );
      }
    } catch {
      setScrapeMsg(t('read.scrapeFailed'));
    } finally {
      setScraping(false);
    }
  }

  if (ref === undefined) return <div className="muted region-pad">{t('read.pickItem')}</div>;

  const atts = ref.attachments ?? [];
  const primary = primaryAttachment(ref);
  const attPresent = hasAttachment({ rels, files }, primary);
  // The quick-open button reflects the primary attachment's actual kind — a hard
  // "PDF" label/icon is misleading for an EPUB, markdown, image, … attachment.
  const primaryKind = primary !== undefined ? viewKindFor(primary.file) : 'pdf';

  async function onAddAttachment(): Promise<void> {
    if (ref === undefined) return;
    setAttBusy(true);
    setAttErr(null);
    try {
      const added = await pickAndCopyAttachment();
      if (added !== null) {
        addAttachmentToRef(ref.id, {
          file: added.file,
          contentType: added.contentType,
          source: 'managed',
          key: added.key,
          path: added.path,
        });
      }
    } catch (e) {
      setAttErr(e instanceof Error ? e.message : String(e));
    } finally {
      setAttBusy(false);
    }
  }

  function onRemoveAttachment(a: Attachment): void {
    if (ref === undefined) return;
    // Only delete bytes for files we created; a Zotero attachment is just unlinked
    // (its file stays in the user's Zotero library, untouched).
    if (a.source === 'managed') void deleteManagedAttachmentFile(a.path);
    removeAttachmentFromRef(ref.id, a.id);
  }

  const tabs: { id: Tab; label: string }[] = [
    { id: 'info', label: t('read.tabInfo') },
    { id: 'read', label: t('read.tabRead') },
    { id: 'notes', label: t('read.tabNotes') },
    { id: 'cite', label: t('read.tabCite') },
    { id: 'meta', label: t('read.tabMeta') },
    // The assistant sits alongside the other tabs in the reader so it can be used
    // while reading the PDF; it's reader-only (embedded) to keep the list side lean.
    ...(embedded === true ? [{ id: 'assistant' as Tab, label: t('read.tabAssistant') }] : []),
  ];

  // Context handed to the reader's assistant — the paper's identity + any notes.
  const assistantContext = {
    label: ref.title !== '' ? ref.title : t('read.untitled'),
    build: (): string => {
      const parts = [`Paper: "${ref.title}"`];
      if (ref.authors.length > 0) parts.push(`Authors: ${ref.authors.join(', ')}`);
      if (ref.year !== undefined) parts.push(`Year: ${ref.year}`);
      if (ref.abstract !== undefined && ref.abstract !== '') parts.push(`Abstract: ${ref.abstract}`);
      if (ref.notes !== undefined && ref.notes.trim() !== '') parts.push(`My notes so far:\n${ref.notes}`);
      return parts.join('\n');
    },
  };

  return (
    <div className="ref-inspector">
      <div className="ref-tabs">
        {onCollapse !== undefined && (
          <button className="read-fold" title={t('read.collapse')} onClick={onCollapse}>
            <Icon name="chevron-right" />
          </button>
        )}
        {tabs.map((tb) => (
          <button
            key={tb.id}
            role="tab"
            aria-selected={tab === tb.id}
            className={tab === tb.id ? 'ref-tab active' : 'ref-tab'}
            onClick={() => setTab(tb.id)}
          >
            {tb.label}
          </button>
        ))}
        <span className="spacer" />
        {primary !== undefined &&
          !embedded &&
          (attPresent ? (
            <button
              className="ref-pdf-btn"
              title={t('read.openInReader')}
              onClick={() => onOpenReader?.(ref.id, primary.id)}
            >
              <Icon name={KIND_ICON[primaryKind]} size={14} />
              {t(KIND_LABEL[primaryKind])}
            </button>
          ) : (
            <button
              className="ref-pdf-btn muted"
              title={storageLinked ? t('read.pdfNotFound') : t('read.pdfLinkHint')}
              onClick={() => setTab('read')}
            >
              <Icon name={KIND_ICON[primaryKind]} size={14} />
              {t(KIND_LABEL[primaryKind])}
            </button>
          ))}
        <button
          className="ref-scrape-btn"
          disabled={scraping}
          title={t('read.scrapeTitle')}
          onClick={() => {
            setTab('meta');
            void runScrape();
          }}
        >
          {scraping ? <span className="ref-scrape-busy">…</span> : <Icon name="refresh" />}
          {t('read.scrape')}
        </button>
        {confirming ? (
          <span className="ref-confirm">
            <span className="muted small">{t('read.confirmDelete')}</span>
            <button
              className="link-btn danger"
              onClick={() => {
                remove(ref.id);
                setConfirming(false);
              }}
            >
              {t('read.confirmDeleteYes')}
            </button>
            <button className="link-btn" onClick={() => setConfirming(false)}>
              {t('common.cancel')}
            </button>
          </span>
        ) : (
          <button className="link-btn danger" onClick={() => setConfirming(true)}>
            {t('read.delete')}
          </button>
        )}
      </div>

      {tab === 'assistant' ? (
        <div className="ref-assistant-body">
          <AgentCompanion
            storageKey="termipod.read.reader.agent"
            context={assistantContext}
            onInsert={(text) => {
              const prev = ref.notes ?? '';
              update(ref.id, { notes: prev.trim() === '' ? text : `${prev}\n\n${text}` });
            }}
          />
        </div>
      ) : (
      <div className="ref-tab-body scroll">
        {tab === 'info' && (
          <div className="ref-form">
            <label className="wide">
              {t('read.fType')}
              <select value={ref.type} onChange={(e) => update(ref.id, { type: e.target.value as RefType })}>
                {REF_TYPES.map((k) => (
                  <option key={k} value={k}>
                    {k}
                  </option>
                ))}
              </select>
            </label>
            <label className="wide">
              {t('read.fTitle')}
              <input value={ref.title} autoFocus={ref.title === ''} onChange={(e) => update(ref.id, { title: e.target.value })} />
            </label>
            <label className="wide">
              {t('read.fAuthors')}
              <input
                value={ref.authors.join('; ')}
                onChange={(e) => update(ref.id, { authors: splitList(e.target.value, ';') })}
                placeholder="Ada Lovelace; Alan Turing"
              />
            </label>
            <div className="ref-form-row">
              <label>
                {t('read.fYear')}
                <input
                  value={ref.year !== undefined ? String(ref.year) : ''}
                  onChange={(e) => {
                    const n = parseInt(e.target.value, 10);
                    update(ref.id, { year: Number.isFinite(n) ? n : undefined });
                  }}
                  inputMode="numeric"
                />
              </label>
              <label className="grow">
                {t('read.fVenue')}
                <input value={ref.venue ?? ''} onChange={(e) => update(ref.id, { venue: e.target.value })} />
              </label>
            </div>
            <div className="ref-form-row">
              <label className="grow">
                <span className="ref-field-head">
                  {t('read.fDoi')}
                  {ref.doi !== undefined && ref.doi !== '' && (
                    <button
                      className="link-btn ref-open-link"
                      title={t('read.openExternal')}
                      onClick={() => openLink(`https://doi.org/${ref.doi ?? ''}`)}
                    >
                      <Icon name="external" size={13} />
                    </button>
                  )}
                </span>
                <input value={ref.doi ?? ''} onChange={(e) => update(ref.id, { doi: e.target.value || undefined })} />
              </label>
              <label className="grow">
                <span className="ref-field-head">
                  {t('read.fUrl')}
                  {ref.url !== undefined && ref.url !== '' && (
                    <button
                      className="link-btn ref-open-link"
                      title={t('read.openExternal')}
                      onClick={() => openLink(ref.url ?? '')}
                    >
                      <Icon name="external" size={13} />
                    </button>
                  )}
                </span>
                <input value={ref.url ?? ''} onChange={(e) => update(ref.id, { url: e.target.value || undefined })} />
              </label>
            </div>
            <label className="wide">
              {t('read.fTags')}
              <input
                value={ref.tags.join(', ')}
                onChange={(e) => update(ref.id, { tags: splitList(e.target.value, ',') })}
                placeholder="transformers, rlhf"
              />
            </label>
            {collections.length > 0 && (
              <div className="wide">
                <div className="muted small">{t('read.collections')}</div>
                <div className="ref-col-checks">
                  {collections.map((c) => {
                    const on = ref.collectionIds.includes(c.id);
                    return (
                      <label key={c.id} className="ref-col-check">
                        <input
                          type="checkbox"
                          checked={on}
                          onChange={() =>
                            update(ref.id, {
                              collectionIds: on
                                ? ref.collectionIds.filter((x) => x !== c.id)
                                : [...ref.collectionIds, c.id],
                            })
                          }
                        />
                        {c.name}
                      </label>
                    );
                  })}
                </div>
              </div>
            )}
            <div className="wide ref-attach-info">
              <div className="att-head-row">
                <span className="muted small">{t('read.attHead')}</span>
                {isTauri() && (
                  <button className="link-btn att-add" disabled={attBusy} onClick={() => void onAddAttachment()}>
                    <Icon name="plus" size={14} /> {attBusy ? t('read.attAdding') : t('read.attAdd')}
                  </button>
                )}
              </div>
              {attErr !== null && <div className="muted small att-err">{attErr}</div>}
              {atts.length === 0 ? (
                <div className="muted small">{t('read.attNone')}</div>
              ) : (
                atts.map((a) => (
                  <AttachmentInfo
                    key={a.id}
                    att={a}
                    onOpen={() => onOpenReader?.(ref.id, a.id)}
                    onRemove={() => onRemoveAttachment(a)}
                  />
                ))
              )}
            </div>
            {ref.citationCount !== undefined && (
              <div className="muted small wide">{t('read.citedBy').replace('{n}', String(ref.citationCount))}</div>
            )}
            {ref.details !== undefined && Object.keys(ref.details).length > 0 && (
              <div className="wide">
                <div className="muted small">{t('read.details')}</div>
                <dl className="ref-details">
                  {Object.entries(ref.details).map(([k, v]) => (
                    <div key={k} className="ref-detail-row">
                      <dt>{humanizeKey(k)}</dt>
                      <dd>{v}</dd>
                    </div>
                  ))}
                </dl>
              </div>
            )}
          </div>
        )}

        {tab === 'read' && (
          <div className="region-pad doc-body">
            {ref.tldr !== undefined && (
              <div className="ref-tldr">
                <span className="ref-tldr-tag">TLDR</span> {ref.tldr}
              </div>
            )}
            {ref.pdfUrl !== undefined && (
              <div className="ref-pdf muted small">
                {t('read.pdf')}: <span className="mono">{ref.pdfUrl}</span>
              </div>
            )}
            {primary !== undefined && (
              <div className="ref-attach">
                {attPresent ? (
                  <button className="primary small" onClick={() => onOpenReader?.(ref.id, primary.id)}>
                    <Icon name="window" />
                    {t('read.openInReader')}
                  </button>
                ) : storageLinked ? (
                  <div className="muted small">
                    {t('read.pdfNotFound')} <span className="mono">{primary.file}</span>
                  </div>
                ) : (
                  <div className="muted small">
                    {t('read.pdfLinkHint')} <span className="mono">{primary.file}</span>
                  </div>
                )}
              </div>
            )}
            {ref.abstract !== undefined && ref.abstract !== '' && (
              <>
                <div className="ref-section-label">{t('read.abstract')}</div>
                <p className="ref-abstract">{ref.abstract}</p>
              </>
            )}
            {editingBody ? (
              <>
                <textarea
                  className="editor-pane ref-body-edit"
                  value={ref.bodyMarkdown ?? ''}
                  onChange={(e) => update(ref.id, { bodyMarkdown: e.target.value })}
                  placeholder={t('read.bodyPlaceholder')}
                />
                {(ref.bodyMarkdown ?? '') !== '' && (
                  <button className="link-btn" onClick={() => setEditingBody(false)}>
                    {t('read.previewBody')}
                  </button>
                )}
              </>
            ) : (
              <>
                <Markdown text={ref.bodyMarkdown ?? ''} singleDollarMath />
                <button className="link-btn" onClick={() => setEditingBody(true)}>
                  {t('read.editBody')}
                </button>
              </>
            )}
          </div>
        )}

        {tab === 'notes' && (
          <div className="ref-notes">
            <div className="ref-notes-bar">
              <div className="seg">
                <button
                  className={notesMode === 'wysiwyg' ? 'seg-btn active' : 'seg-btn'}
                  onClick={() => pickNotesMode('wysiwyg')}
                >
                  {t('read.notesWysiwyg')}
                </button>
                <button
                  className={notesMode === 'source' ? 'seg-btn active' : 'seg-btn'}
                  onClick={() => pickNotesMode('source')}
                >
                  {t('read.notesSource')}
                </button>
                <button
                  className={notesMode === 'preview' ? 'seg-btn active' : 'seg-btn'}
                  onClick={() => pickNotesMode('preview')}
                >
                  {t('read.notesPreview')}
                </button>
              </div>
              <span className="spacer" />
              {onOpenNote !== undefined && (
                <button className="link-btn" title={t('read.openNoteTab')} onClick={() => onOpenNote(ref.id)}>
                  <Icon name="external" size={14} /> {t('read.openNoteTab')}
                </button>
              )}
              {isTauri() && (
                <button className="link-btn" title={t('read.notesExport')} onClick={() => void exportNotes()}>
                  <Icon name="download" size={14} /> {t('read.notesExport')}
                </button>
              )}
            </div>
            {notesMode === 'preview' ? (
              <div className="ref-notes-preview doc-body region-pad">
                <Markdown text={ref.notes} singleDollarMath />
              </div>
            ) : notesMode === 'source' ? (
              <MarkdownEditor
                key={`src-${ref.id}`}
                value={ref.notes}
                onChange={(v) => update(ref.id, { notes: v })}
                placeholder={t('read.notesPlaceholder')}
              />
            ) : (
              <Suspense fallback={<div className="muted region-pad">{t('read.loadingFile')}</div>}>
                <WysiwygEditor
                  key={`wys-${ref.id}`}
                  value={ref.notes}
                  onChange={(v) => update(ref.id, { notes: v })}
                  placeholder={t('read.notesPlaceholder')}
                />
              </Suspense>
            )}
          </div>
        )}

        {tab === 'cite' && (
          <div className="ref-cite region-pad">
            <div className="ref-cite-block">
              <div className="ref-cite-head">
                <span className="muted small">APA</span>
                <span className="spacer" />
                <button className="link-btn" onClick={() => copy(citeApa(ref))}>
                  {t('read.copy')}
                </button>
              </div>
              <div className="ref-cite-text">{citeApa(ref)}</div>
            </div>
            <div className="ref-cite-block">
              <div className="ref-cite-head">
                <span className="muted small">BibTeX</span>
                <span className="spacer" />
                <button className="link-btn" onClick={() => copy(citeBibtex(ref))}>
                  {t('read.copy')}
                </button>
              </div>
              <pre className="ref-cite-text mono">{citeBibtex(ref)}</pre>
            </div>
          </div>
        )}

        {tab === 'meta' && <RefMeta reference={ref} scraping={scraping} msg={scrapeMsg} onScrape={() => void runScrape()} />}
      </div>
      )}
    </div>
  );
}

// ---- Reader ----------------------------------------------------------------

// A dedicated reading view (one open PDF tab). The PDF is the main pane; a
// resizable side column reuses the Inspector (Info / Read / Notes / Cite) so
// notes are written next to the document. Multiple of these live behind the tab
// strip; switching tabs swaps which one renders (director: "the PDF viewer can be
// opened in several tabs at the same time").
function ReaderView({
  refId,
  attId,
  onGone,
  onOpenReader,
  onOpenNote,
}: {
  refId: string;
  attId?: string;
  onGone: () => void;
  onOpenReader?: (id: string, attId?: string) => void;
  onOpenNote?: (refId: string) => void;
}): JSX.Element {
  const t = useT();
  const ref = useLibrary((s) => s.references.find((r) => r.id === refId));
  const update = useLibrary((s) => s.updateReference);
  const openLink = useOpenLink();
  const [sideW, setSideW] = useState(() => loadWidth('termipod.read.readerSideW', 420));
  const [sideOpen, setSideOpen] = useState(true);
  // Mirror of sideW for the resize handler (read live width across drag ticks +
  // decide to auto-collapse, without a functional-updater side effect).
  const sideWRef = useRef(sideW);
  sideWRef.current = sideW;

  // Append a text selection from the PDF into this reference's notes.
  function saveSelection(text: string): void {
    const cur = useLibrary.getState().references.find((r) => r.id === refId);
    const prev = cur?.notes ?? '';
    update(refId, { notes: prev.trim() === '' ? text : `${prev}\n\n${text}` });
  }

  // Append an area screenshot to the notes as a markdown image. The bytes are
  // written as a managed attachment (de-inlined) and referenced by a short
  // `termipod-att://` scheme instead of a giant base64 blob in the note string;
  // falls back to inline data-URI in the browser build (no file access).
  async function saveImageToNote(dataUri: string): Promise<void> {
    let embed = `![figure](${dataUri})`;
    const comma = dataUri.indexOf(',');
    if (comma > 0) {
      try {
        const ref2 = await writeNoteImage(dataUri.slice(comma + 1), `figure-${Date.now()}.png`);
        if (ref2 !== null) embed = `![figure](${ref2})`;
      } catch {
        /* keep the data-URI fallback */
      }
    }
    const cur = useLibrary.getState().references.find((r) => r.id === refId);
    const prev = cur?.notes ?? '';
    update(refId, { notes: prev.trim() === '' ? embed : `${prev}\n\n${embed}` });
  }

  useEffect(() => {
    if (ref === undefined) onGone(); // deleted while open — drop the tab
  }, [ref, onGone]);
  if (ref === undefined) return <></>;

  // The attachment this tab opened (by id), else the reference's first.
  const att = ref.attachments?.find((a) => a.id === attId) ?? primaryAttachment(ref);
  const url = ref.url;
  // A PDF hosts these actions (open-URL + details toggle) inside its own toolbar,
  // so no title/action row is rendered above it — saves vertical space, and the
  // tab strip already labels the document. Other attachment kinds (image, video,
  // text, …) have no embedded toolbar, so keep a slim action strip here.
  const isPdf = att !== undefined && viewKindFor(att.file) === 'pdf';
  const toggleDetails = (): void => setSideOpen((v) => !v);
  return (
    <div className="reader-view">
      {!isPdf && (
        <div className="reader-topbar reader-topbar-actions">
          <span className="spacer" />
          {url !== undefined && url !== '' && (
            <button className="link-btn" title={t('read.openUrl')} onClick={() => openLink(url)}>
              {t('read.openUrl')} <Icon name="external" size={13} />
            </button>
          )}
          <button
            className="link-btn"
            title={sideOpen ? t('read.hideDetails') : t('read.showDetails')}
            onClick={toggleDetails}
          >
            <Icon name={sideOpen ? 'chevron-right' : 'chevron-left'} size={15} />
          </button>
        </div>
      )}
      <div className="reader-body">
        <div className="reader-doc">
          {att !== undefined ? (
            <AttachmentView
              att={att}
              referenceId={refId}
              onSaveSelection={saveSelection}
              onImageToNote={saveImageToNote}
              docUrl={url}
              detailsOpen={sideOpen}
              onToggleDetails={toggleDetails}
            />
          ) : (
            <div className="muted region-pad">{t('read.noPdf')}</div>
          )}
        </div>
        {sideOpen && (
          <>
            <ResizeHandle
              onResize={(dx) => {
                const raw = sideWRef.current - dx;
                // Dragged past the min toward the edge → auto-collapse (reopen with
                // the details toggle — in the PDF toolbar, or the top action strip
                // for other attachment kinds).
                if (raw < 260) {
                  setSideOpen(false);
                  return;
                }
                const n = clamp(raw, 300, 760);
                sideWRef.current = n;
                setSideW(n);
                saveWidth('termipod.read.readerSideW', n);
              }}
            />
            <aside className="reader-side" style={{ width: sideW }}>
              <Inspector refId={refId} embedded onOpenReader={onOpenReader} onOpenNote={onOpenNote} />
            </aside>
          </>
        )}
      </div>
    </div>
  );
}

// ---- Discover --------------------------------------------------------------

const SOURCE_LS = 'termipod.discover.source';

function DiscoverPanel({ onSelect }: { onSelect: (id: string) => void }): JSX.Element {
  const t = useT();
  const openLink = useOpenLink();
  const add = useLibrary((s) => s.addReference);
  const references = useLibrary((s) => s.references);
  const [q, setQ] = useState('');
  const [results, setResults] = useState<DiscoveryPaper[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [sourceId, setSourceId] = useState<string>(() => localStorage.getItem(SOURCE_LS) ?? 'openalex');
  const [showKey, setShowKey] = useState(false);
  const [findPdfs, setFindPdfs] = useState(true);
  // When the query is a bare DOI / arXiv id / OpenAlex id, offer a direct
  // scrape-and-add instead of a keyword search.
  const idSeed = useMemo(() => detectIdentifier(q), [q]);
  const [addingId, setAddingId] = useState(false);

  const source = sourceById(sourceId);
  const [key, setKey] = useState(() => (source.keyKey !== undefined ? lsGet(source.keyKey) : ''));

  const importedIds = useMemo(
    () => new Set(references.map((r) => r.externalId).filter((x): x is string => x !== undefined)),
    [references],
  );

  function pickSource(id: string): void {
    setSourceId(id);
    try {
      localStorage.setItem(SOURCE_LS, id);
    } catch {
      /* ignore */
    }
    const s = sourceById(id);
    setKey(s.keyKey !== undefined ? lsGet(s.keyKey) : '');
    setShowKey(false);
    setErr(null);
  }

  async function run(): Promise<void> {
    if (q.trim() === '') return;
    setBusy(true);
    setErr(null);
    try {
      let res = await source.search(q, 25);
      // Backfill open-access PDF links (Unpaywall) for results with a DOI but no
      // PDF — more "PDF" badges appear regardless of which source was used.
      if (findPdfs) res = await enrichWithUnpaywall(res);
      setResults(res);
    } catch (e) {
      const msg = e instanceof Error ? e.message : '';
      if (msg === 'needs-key') {
        setShowKey(true);
        setErr(t('read.needsKey'));
      } else {
        setErr(msg === 'rate-limited' ? t('read.rateLimited') : t('read.searchFailed'));
      }
      setResults([]);
    } finally {
      setBusy(false);
    }
  }

  function importPaper(p: DiscoveryPaper): void {
    const id = add(paperToRef(p));
    onSelect(id);
  }

  async function addById(): Promise<void> {
    if (idSeed === null) return;
    setAddingId(true);
    setErr(null);
    try {
      const res = await scrapeMetadata(idSeed);
      const p = res.patch;
      if (p === null || (p.title ?? '') === '') {
        setErr(t('read.idNotFound'));
        return;
      }
      const id = add({
        type: p.arxivId !== undefined ? 'preprint' : 'article',
        title: p.title ?? '',
        authors: p.authors ?? [],
        year: p.year,
        venue: p.venue,
        doi: p.doi,
        arxivId: p.arxivId,
        url: idSeed.url,
        pdfUrl: p.pdfUrl,
        abstract: p.abstract,
        source: 'scrape',
        externalId: res.identifier,
        referenceCount: p.referenceCount,
        citedByCount: p.citedByCount,
        references: p.references,
        citations: p.citations,
        journal: p.journal,
        openAccess: p.openAccess,
        topics: p.topics,
        resourceLinks: p.resourceLinks,
        details: p.detailsAdd,
        enrichedAt: p.enrichedAt,
        enrichSource: p.enrichSource,
        tags: [],
        collectionIds: [],
        notes: '',
      });
      onSelect(id);
    } catch {
      setErr(t('read.searchFailed'));
    } finally {
      setAddingId(false);
    }
  }

  return (
    <div className="discover-pane">
      <div className="discover-bar">
        <input
          className="discover-input"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void run();
          }}
          placeholder={t('read.discoverPlaceholder')}
          autoFocus
        />
        <button className="primary" disabled={busy || q.trim() === ''} onClick={() => void run()}>
          {busy ? t('read.searching') : t('read.search')}
        </button>
        {idSeed !== null && (
          <button className="discover-addid" disabled={addingId} title={t('read.addByIdHint')} onClick={() => void addById()}>
            {addingId ? t('read.adding') : t('read.addById')}
          </button>
        )}
      </div>
      <div className="discover-sources">
        {SOURCES.map((s) => (
          <button
            key={s.id}
            className={`discover-src${sourceId === s.id ? ' active' : ''}`}
            title={s.note}
            onClick={() => pickSource(s.id)}
          >
            {s.label}
          </button>
        ))}
      </div>
      <div className="discover-source muted small">
        <span>{source.note ?? ''}</span>
        <span className="spacer" />
        <label className="discover-pdftoggle">
          <input type="checkbox" checked={findPdfs} onChange={(e) => setFindPdfs(e.target.checked)} />
          {t('read.findPdfs')}
        </label>
        {source.keyKey !== undefined && (
          <button className="link-btn" onClick={() => setShowKey((v) => !v)}>
            {key !== '' ? t('read.apiKeySet') : t('read.apiKeyAdd')}
          </button>
        )}
      </div>
      {showKey && source.keyKey !== undefined && (
        <div className="discover-key">
          <PasswordInput
            className="discover-key-input"
            value={key}
            placeholder={t('read.apiKeyPlaceholder')}
            onChange={(e) => {
              setKey(e.target.value);
              if (source.keyKey !== undefined) lsSet(source.keyKey, e.target.value);
            }}
          />
          {source.keyUrl !== undefined && (
            <button className="link-btn" onClick={() => openLink(source.keyUrl ?? '')}>
              {t('read.getApiKey')} <Icon name="external" size={13} />
            </button>
          )}
        </div>
      )}
      {err !== null && <div className="error region-pad">{err}</div>}
      <div className="discover-results scroll">
        {!busy && results.length === 0 && err === null && (
          <div className="muted region-pad">{t('read.discoverHint')}</div>
        )}
        {results.map((p) => {
          const imported = p.paperId !== '' && importedIds.has(p.paperId);
          return (
            <div key={p.paperId || p.title} className="discover-card">
              <div className="discover-card-title">{p.title}</div>
              <div className="discover-card-meta muted small">
                {p.authors.slice(0, 4).join(', ')}
                {p.authors.length > 4 ? ' et al.' : ''}
                {p.year !== undefined ? ` · ${p.year}` : ''}
                {p.venue !== undefined ? ` · ${p.venue}` : ''}
                {p.citationCount !== undefined ? ` · ${p.citationCount} cited` : ''}
              </div>
              {p.tldr !== undefined && (
                <div className="discover-tldr">
                  <span className="ref-tldr-tag">TLDR</span> {p.tldr}
                </div>
              )}
              <div className="discover-card-actions">
                <button className="primary small" disabled={imported} onClick={() => importPaper(p)}>
                  {imported ? t('read.inLibrary') : t('read.addToLibrary')}
                </button>
                {p.pdfUrl !== undefined && <span className="pill ok small">PDF</span>}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ---- ReadSurface -----------------------------------------------------------

export function ReadSurface(): JSX.Element {
  const t = useT();
  const { ask, node: promptNode } = useTextPrompt();
  const references = useLibrary((s) => s.references);
  const collections = useLibrary((s) => s.collections);
  const addReference = useLibrary((s) => s.addReference);
  const addCollection = useLibrary((s) => s.addCollection);
  const removeCollection = useLibrary((s) => s.removeCollection);
  const renameCollection = useLibrary((s) => s.renameCollection);
  const renameTag = useLibrary((s) => s.renameTag);
  const removeTag = useLibrary((s) => s.removeTag);
  const removeReference = useLibrary((s) => s.removeReference);
  const updateReference = useLibrary((s) => s.updateReference);
  const addAttachment = useLibrary((s) => s.addAttachment);
  const importReferences = useLibrary((s) => s.importReferences);
  const linkFolder = useZoteroStorage((s) => s.linkFolder);
  const linkNative = useZoteroStorage((s) => s.linkNative);
  const reindex = useZoteroStorage((s) => s.reindex);
  const storageCount = useZoteroStorage((s) => s.count);
  const storagePath = useZoteroStorage((s) => s.path);

  const [mode, setMode] = useState<Mode>('library');
  const [collection, setCollection] = useState<string>(ALL);
  const [tag, setTag] = useState<string | null>(null);
  const [tagFilter, setTagFilter] = useState('');
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState<string | null>(null);
  const [sortKey, setSortKey] = useState<SortKey>('title');
  const [sortDir, setSortDir] = useState<SortDir>('asc');
  // Open reader / browser tabs (director: PDFs open in several tabs at once, and
  // links open in a dedicated in-app browser tab). `activeTab === null` = library.
  const [tabs, setTabs] = useState<ReadTab[]>([]);
  const [activeTab, setActiveTab] = useState<string | null>(null);
  const [importing, setImporting] = useState(false);
  const [importMsg, setImportMsg] = useState<string | null>(null);
  // Right-click menu for a library row (Zotero-style item context menu). Delete
  // is two-step (`menuConfirm`) — `window.confirm` is unreliable in WebView2.
  const [rowMenu, setRowMenu] = useState<{ x: number; y: number; id: string } | null>(null);
  const [menuConfirm, setMenuConfirm] = useState(false);
  // Right-click on the collections pane's blank space → New collection (the
  // per-row rename/delete menus are the bespoke colMenu/tagMenu below).
  const railBlankMenu = useContextMenu();
  // Right-click menus for the rail (Zotero-style): rename/delete a collection or a
  // tag. Collections carry an id; tags are keyed by their (string) name.
  const [colMenu, setColMenu] = useState<{ x: number; y: number; id: string } | null>(null);
  const [tagMenu, setTagMenu] = useState<{ x: number; y: number; name: string } | null>(null);
  const [railW, setRailW] = useState(() => loadWidth('termipod.read.railW', 220));
  // Height of the collections pane in the rail; the tags pane fills the rest. The
  // divider between them (VResizeHandle) drags this vertically (Zotero-style).
  const [colPaneH, setColPaneH] = useState(() => loadWidth('termipod.read.colPaneH', 240));
  const railRef = useRef<HTMLElement>(null);
  const [inspW, setInspW] = useState(() => loadWidth('termipod.read.inspW', 380));
  const [railCollapsed, setRailCollapsed] = useState(() => localStorage.getItem('termipod.read.railFold') === '1');
  const [inspCollapsed, setInspCollapsed] = useState(() => localStorage.getItem('termipod.read.inspFold') === '1');
  const [showAgent, setShowAgent] = useState(false);
  const [showWebdav, setShowWebdav] = useState(false);
  const [agentW, setAgentW] = useState(() => loadWidth('termipod.read.agentW', 360));
  const client = useSession((s) => s.client);
  const [syncing, setSyncing] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const dirRef = useRef<HTMLInputElement>(null);

  function foldRail(v: boolean): void {
    setRailCollapsed(v);
    try {
      localStorage.setItem('termipod.read.railFold', v ? '1' : '0');
    } catch {
      /* ignore */
    }
  }
  function foldInsp(v: boolean): void {
    setInspCollapsed(v);
    try {
      localStorage.setItem('termipod.read.inspFold', v ? '1' : '0');
    } catch {
      /* ignore */
    }
  }

  function openPdfTab(refId: string, attId?: string): void {
    const r = useLibrary.getState().references.find((x) => x.id === refId);
    const targetAtt = attId ?? primaryAttachment(r ?? { attachments: [] })?.id;
    // A given attachment reuses its tab; a different attachment of the same item
    // opens its own tab.
    const existing = tabs.find((tb) => tb.kind === 'pdf' && tb.refId === refId && tb.attId === targetAtt);
    if (existing !== undefined) {
      setActiveTab(existing.id);
      return;
    }
    const id = nextTabId();
    const baseTitle = r !== undefined && r.title !== '' ? r.title : t('read.untitled');
    // Disambiguate the tab by filename when the item has more than one attachment.
    const attFile = r?.attachments?.find((a) => a.id === targetAtt)?.file;
    const title = (r?.attachments?.length ?? 0) > 1 && attFile !== undefined ? `${baseTitle} · ${attFile}` : baseTitle;
    setTabs((ts) => [...ts, { id, kind: 'pdf', refId, attId: targetAtt, title }]);
    setActiveTab(id);
  }

  function openWebTab(url: string): void {
    if (url === '') return;
    const id = nextTabId();
    setTabs((ts) => [...ts, { id, kind: 'web', url, title: hostOf(url) }]);
    setActiveTab(id);
  }

  function openNoteTab(refId: string): void {
    const existing = tabs.find((tb) => tb.kind === 'note' && tb.refId === refId);
    if (existing !== undefined) {
      setActiveTab(existing.id);
      return;
    }
    const r = useLibrary.getState().references.find((x) => x.id === refId);
    const baseTitle = r !== undefined && r.title !== '' ? r.title : t('read.untitled');
    const id = nextTabId();
    setTabs((ts) => [...ts, { id, kind: 'note', refId, title: `${baseTitle} · ${t('read.noteTabSuffix')}` }]);
    setActiveTab(id);
  }

  function closeTab(id: string): void {
    setTabs((ts) => ts.filter((tb) => tb.id !== id));
    setActiveTab((a) => (a === id ? null : a));
  }

  // Dismiss any open context menu (row / collection / tag) on an outside click,
  // scroll, or Escape.
  const anyMenuOpen = rowMenu !== null || colMenu !== null || tagMenu !== null;
  useEffect(() => {
    if (!anyMenuOpen) return;
    const close = (): void => {
      setRowMenu(null);
      setColMenu(null);
      setTagMenu(null);
    };
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') close();
    };
    window.addEventListener('click', close);
    window.addEventListener('scroll', close, true);
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('click', close);
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('keydown', onKey);
    };
  }, [anyMenuOpen]);

  // Pick a file and attach it to the item (managed copy). Mirrors the Inspector's
  // Add-file flow so an attachment can be added straight from the row menu.
  async function ctxAddAttachment(id: string): Promise<void> {
    setRowMenu(null);
    try {
      const added = await pickAndCopyAttachment();
      if (added !== null) {
        addAttachment(id, {
          file: added.file,
          contentType: added.contentType,
          source: 'managed',
          key: added.key,
          path: added.path,
        });
      }
    } catch (e) {
      setImportMsg(e instanceof Error ? e.message : String(e));
    }
  }

  // Re-index the persisted storage-folder path on mount (Tauri) so the link
  // survives a restart instead of being lost (director report). Also resolve the
  // default attachment-store dir so "Add file" has a root even before Settings.
  useEffect(() => {
    if (isTauri()) {
      void reindex();
      void useAttachmentConfig.getState().resolveDefault();
    }
  }, [reindex]);

  // `webkitdirectory` isn't in the input TS types — set it (plus the vendor
  // aliases) imperatively so the browser-build picker selects a folder.
  useEffect(() => {
    const el = dirRef.current;
    if (el === null) return;
    el.setAttribute('webkitdirectory', '');
    el.setAttribute('directory', '');
    el.setAttribute('mozdirectory', '');
  }, []);

  // Native folder dialog under Tauri (persisted, survives restart); the browser
  // build falls back to the session-only webkitdirectory picker.
  async function onLinkStorage(): Promise<void> {
    if (isTauri()) {
      // Seed the picker at the real storage location — the currently-linked folder
      // if any, else the active attachment root — so it doesn't open at whatever
      // dir another tab/workspace last browsed.
      const start = storagePath ?? activeAttachmentRoot() ?? undefined;
      const err = await linkNative(start);
      if (err !== null) setImportMsg(err);
    } else {
      dirRef.current?.click();
    }
  }

  // Attachments exist but no folder is linked — prompt a (re-)link.
  const needsRelink = useMemo(
    // Only Zotero-sourced attachments need the linked folder; managed ones resolve
    // by their own path.
    () =>
      storageCount === 0 &&
      references.some((r) => (r.attachments ?? []).some((a) => a.source === 'zotero')),
    [storageCount, references],
  );

  function onPickStorage(e: React.ChangeEvent<HTMLInputElement>): void {
    const list = e.target.files;
    if (list !== null && list.length > 0) linkFolder(list);
  }

  // Import a Zotero `zotero.sqlite` library — parsed in-WebView via sql.js, so
  // no bytes leave the device and no Rust/hub round-trip is needed.
  async function onImportFile(e: React.ChangeEvent<HTMLInputElement>): Promise<void> {
    const file = e.target.files?.[0];
    e.target.value = ''; // let the same file be re-picked later
    if (file === undefined) return;
    setImporting(true);
    setImportMsg(null);
    try {
      const { parseZoteroSqlite } = await import('../import/zoteroImport');
      const bytes = new Uint8Array(await file.arrayBuffer());
      const items = await parseZoteroSqlite(bytes);
      const res = importReferences(items);
      setMode('library');
      setImportMsg(
        t('read.importResult')
          .replace('{a}', String(res.added))
          .replace('{u}', String(res.updated))
          .replace('{c}', String(res.collectionsCreated)),
      );
    } catch {
      setImportMsg(t('read.importFailed'));
    } finally {
      setImporting(false);
    }
  }

  const allTags = useMemo(() => {
    const s = new Set<string>();
    // Hide internal tags (e.g. "/unread") from the facet — including any already
    // sitting in an imported/hub-synced library, where the automatic-tag type is
    // no longer known so name is the only signal we have.
    references.forEach((r) => r.tags.forEach((tg) => !isInternalTag(tg) && s.add(tg)));
    return [...s].sort();
  }, [references]);
  // The tag pane's filter box (Zotero-style): narrows the shown tags by substring.
  const shownTags = useMemo(() => {
    const q = tagFilter.trim().toLowerCase();
    return q === '' ? allTags : allTags.filter((tg) => tg.toLowerCase().includes(q));
  }, [allTags, tagFilter]);

  // Defer the filter term so typing stays responsive: React runs the (potentially
  // large) re-filter/re-sort at a lower priority and can interrupt it (#311).
  const deferredQuery = useDeferredValue(query);
  const items = useMemo(() => {
    const ql = deferredQuery.trim().toLowerCase();
    const filtered = references.filter((r) => {
      if (collection !== ALL && !r.collectionIds.includes(collection)) return false;
      if (tag !== null && !r.tags.includes(tag)) return false;
      if (ql !== '' && !`${r.title} ${r.authors.join(' ')} ${r.venue ?? ''}`.toLowerCase().includes(ql)) return false;
      return true;
    });
    const dir = sortDir === 'asc' ? 1 : -1;
    return [...filtered].sort((a, b) => {
      const av = sortVal(a, sortKey);
      const bv = sortVal(b, sortKey);
      if (av < bv) return -dir;
      if (av > bv) return dir;
      return a.title.toLowerCase() < b.title.toLowerCase() ? -1 : 1;
    });
  }, [references, collection, tag, deferredQuery, sortKey, sortDir]);

  // Stable-ish context for the virtualized rows (changes only when selection or
  // the openers change, which is exactly when visible rows must re-render).
  const libRowCtx = useMemo<LibRowCtx>(
    () => ({
      selected,
      onSelect: (id) => setSelected(id),
      onOpen: (id) => openPdfTab(id),
      onMenu: (id, x, y) => {
        setSelected(id);
        setMenuConfirm(false);
        setRowMenu({ x, y, id });
      },
      hasPdf: (r) => hasAnyAttachment(r),
    }),
    // openPdfTab is a stable closure over refs/state setters; selection drives re-render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [selected],
  );

  function toggleSort(key: SortKey): void {
    if (key === sortKey) setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
    else {
      setSortKey(key);
      setSortDir('asc');
    }
  }

  const activeTabObj = activeTab !== null ? tabs.find((tb) => tb.id === activeTab) : undefined;
  const selectedRef = selected !== null ? references.find((r) => r.id === selected) : undefined;

  // In-app prompts (window.prompt renders an unreliable native `tauri.localhost`
  // dialog in the webview — see useTextPrompt).
  async function newCollection(): Promise<void> {
    const name = await ask(t('read.newCollectionPrompt'));
    if (name !== null && name.trim() !== '') setCollection(addCollection(name.trim()));
  }
  async function promptRenameCollection(id: string, curName: string): Promise<void> {
    const name = await ask(t('read.renameCollectionPrompt'), curName);
    if (name !== null && name.trim() !== '') renameCollection(id, name.trim());
  }
  async function promptRenameTag(name: string): Promise<void> {
    const next = await ask(t('read.renameTagPrompt'), name);
    if (next !== null && next.trim() !== '' && next.trim() !== name) {
      renameTag(name, next.trim());
      if (tag === name) setTag(next.trim());
    }
  }

  async function onSync(): Promise<void> {
    if (client === null || syncing) return;
    setSyncing(true);
    setImportMsg(null);
    try {
      const r = await syncLibrary(client);
      // Annotations hang off their parent reference's hub id, so they must sync
      // AFTER the library (the push above stamps the hubIds they resolve against).
      const a = await syncAnnotations(client);
      let msg = t('read.syncDone')
        .replace('{up}', String(r.pushed + r.created))
        .replace('{down}', String(r.pulledAdded))
        .replace('{fail}', String(r.failed + a.failed));
      if (a.pushed + a.created + a.pulledAdded > 0) {
        msg += ` · ${t('read.syncAnnotations')
          .replace('{up}', String(a.pushed + a.created))
          .replace('{down}', String(a.pulledAdded))}`;
      }
      setImportMsg(msg);
    } catch (e) {
      setImportMsg(t('read.syncFailed').replace('{err}', e instanceof Error ? e.message : String(e)));
    } finally {
      setSyncing(false);
    }
  }

  function addBlank(): void {
    const id = addReference({
      type: 'article',
      title: '',
      authors: [],
      source: 'manual',
      tags: [],
      collectionIds: collection !== ALL ? [collection] : [],
      notes: '',
    });
    setMode('library');
    setSelected(id);
  }

  return (
    <WorkbenchSurface
      job="read"
      actions={
        <>
          <input
            ref={fileRef}
            type="file"
            accept=".sqlite,application/x-sqlite3,application/vnd.sqlite3"
            style={{ display: 'none' }}
            onChange={(e) => void onImportFile(e)}
          />
          <button
            className="import-btn"
            disabled={importing}
            title={t('read.importHint')}
            onClick={() => fileRef.current?.click()}
          >
            {importing ? t('read.importing') : t('read.importZotero')}
          </button>
          <input ref={dirRef} type="file" style={{ display: 'none' }} onChange={onPickStorage} />
          <button
            className={needsRelink ? 'import-btn attn' : 'import-btn'}
            title={t('read.linkStorageHint')}
            onClick={() => void onLinkStorage()}
          >
            {storageCount > 0
              ? t('read.storageLinked').replace('{n}', String(storageCount))
              : t('read.linkStorage')}
          </button>
          {isTauri() && (
            <button className="import-btn" title={t('read.webdavHint')} onClick={() => setShowWebdav(true)}>
              <Icon name="cloud" size={14} /> {t('read.syncFiles')}
            </button>
          )}
          <div className="seg">
            <button className={mode === 'library' ? 'seg-btn active' : 'seg-btn'} onClick={() => setMode('library')}>
              {t('read.modeLibrary')}
            </button>
            <button className={mode === 'discover' ? 'seg-btn active' : 'seg-btn'} onClick={() => setMode('discover')}>
              {t('read.modeDiscover')}
            </button>
          </div>
          {client !== null && (
            <button className="import-btn" disabled={syncing} title={t('read.syncHint')} onClick={() => void onSync()}>
              {syncing ? t('read.syncing') : t('read.syncHub')}
            </button>
          )}
          <button
            className={showAgent ? 'import-btn attn' : 'import-btn'}
            title={t('author.assistantHint')}
            onClick={() => setShowAgent((v) => !v)}
          >
            {t('author.assistant')}
          </button>
        </>
      }
    >
      <OpenLinkContext.Provider value={openWebTab}>
      {showWebdav && <WebdavModal onClose={() => setShowWebdav(false)} />}
      {importMsg !== null && (
        <div className="read-import-msg">
          <span>{importMsg}</span>
          <span className="spacer" />
          <button className="link-btn" onClick={() => setImportMsg(null)}>
            <Icon name="close" size={13} />
          </button>
        </div>
      )}
      {tabs.length > 0 && (
        <div className="read-tabstrip" role="tablist" aria-label={t('read.tabLibrary')}>
          <button
            role="tab"
            aria-selected={activeTab === null}
            tabIndex={activeTab === null ? 0 : -1}
            className={`read-tabitem${activeTab === null ? ' active' : ''}`}
            onClick={() => setActiveTab(null)}
          >
            {t('read.tabLibrary')}
          </button>
          {tabs.map((tb) => (
            <span key={tb.id} role="presentation" className={`read-tabitem${activeTab === tb.id ? ' active' : ''}`}>
              <button
                role="tab"
                aria-selected={activeTab === tb.id}
                tabIndex={activeTab === tb.id ? 0 : -1}
                className="read-tabitem-label"
                title={tb.title}
                onClick={() => setActiveTab(tb.id)}
              >
                <Icon
                  name={tb.kind === 'web' ? 'globe' : tb.kind === 'note' ? 'note' : 'file-text'}
                  size={13}
                  className="read-tabitem-kind"
                />
                {tb.title}
              </button>
              <button className="read-tabitem-x" title={t('read.closeTab')} onClick={() => closeTab(tb.id)}>
                <Icon name="close" size={13} />
              </button>
            </span>
          ))}
        </div>
      )}
      {activeTabObj !== undefined ? (
        activeTabObj.kind === 'pdf' && activeTabObj.refId !== undefined ? (
          <ReaderView
            refId={activeTabObj.refId}
            attId={activeTabObj.attId}
            onGone={() => closeTab(activeTabObj.id)}
            onOpenReader={openPdfTab}
            onOpenNote={openNoteTab}
          />
        ) : activeTabObj.kind === 'note' && activeTabObj.refId !== undefined ? (
          <NoteTab refId={activeTabObj.refId} />
        ) : (
          <BrowserView initialUrl={activeTabObj.url ?? ''} />
        )
      ) : (
        <>
          {needsRelink && (
            <div className="read-import-msg attn">
              <span>{t('read.relinkStorage')}</span>
              <span className="spacer" />
              <button className="link-btn" onClick={() => void onLinkStorage()}>
                {t('read.linkStorage')}
              </button>
            </div>
          )}
          <div className="read-layout">
        {railCollapsed ? (
          <button className="read-pane-expand" title={t('read.showSidebar')} onClick={() => foldRail(false)}>
            <Icon name="chevron-right" />
          </button>
        ) : (
          <>
        <aside className="read-rail" style={{ width: railW }} ref={railRef}>
          <div className="read-rail-head">
            <button className="read-fold" title={t('read.collapse')} onClick={() => foldRail(true)}>
              <Icon name="chevron-left" />
            </button>
          </div>
          {/* Collections and tags are separate scroll panes (Zotero-style): each
              has its own scrollbar, and the divider between them drags vertically
              to reallocate height. The tag pane is always present (with its filter
              box), even when the library has no tags yet. */}
          <div
            className="read-rail-pane"
            style={{ height: colPaneH }}
            onContextMenu={(e) => {
              // Only the blank area — a right-click on a collection row keeps its
              // own rename/delete menu (that handler runs first and preventDefaults).
              if ((e.target as HTMLElement).closest('.read-col') !== null) return;
              railBlankMenu.open(e, [{ label: t('read.newCollection'), onClick: () => void newCollection() }]);
            }}
          >
            <div className="read-rail-group">
              <button
                className={`read-col${collection === ALL ? ' active' : ''}`}
                onClick={() => {
                  setCollection(ALL);
                  setTag(null);
                }}
              >
                {t('read.allItems')}
                <span className="spacer" />
                <span className="muted small">{references.length}</span>
              </button>
              {collections.map((c) => (
                <button
                  key={c.id}
                  className={`read-col${collection === c.id ? ' active' : ''}`}
                  onClick={() => setCollection(c.id)}
                  onContextMenu={(e) => {
                    e.preventDefault();
                    setRowMenu(null);
                    setTagMenu(null);
                    setColMenu({ x: e.clientX, y: e.clientY, id: c.id });
                  }}
                >
                  {c.name}
                  <span className="spacer" />
                  <span
                    className="read-col-x"
                    title={t('read.removeCollection')}
                    onClick={(e) => {
                      e.stopPropagation();
                      removeCollection(c.id);
                      if (collection === c.id) setCollection(ALL);
                    }}
                  >
                    <Icon name="close" size={12} />
                  </span>
                </button>
              ))}
              <button className="read-col add" onClick={newCollection}>
                <Icon name="plus" size={13} />
                {t('read.newCollection')}
              </button>
            </div>
          </div>
          <VResizeHandle
            onResize={(dy) => {
              const railH = railRef.current?.clientHeight ?? 0;
              // Keep both panes usable: collections ≥ 80px, tags ≥ ~140px.
              const max = railH > 0 ? Math.max(80, railH - 140) : 600;
              setColPaneH((h) => {
                const n = clamp(h + dy, 80, max);
                saveWidth('termipod.read.colPaneH', n);
                return n;
              });
            }}
          />
          <div className="read-rail-pane read-rail-pane-tags grow">
            <div className="read-tags-scroll">
              <div className="read-rail-group">
                <div className="read-rail-label">{t('read.tags')}</div>
                <div className="read-tags">
                  {shownTags.map((tg) => (
                    <button
                      key={tg}
                      className={`read-tag${tag === tg ? ' active' : ''}`}
                      onClick={() => setTag(tag === tg ? null : tg)}
                      onContextMenu={(e) => {
                        e.preventDefault();
                        setRowMenu(null);
                        setColMenu(null);
                        setTagMenu({ x: e.clientX, y: e.clientY, name: tg });
                      }}
                    >
                      {tg}
                    </button>
                  ))}
                  {shownTags.length === 0 && (
                    <div className="muted small read-tags-empty">
                      {allTags.length === 0 ? t('read.noTags') : t('read.noTagMatch')}
                    </div>
                  )}
                </div>
              </div>
            </div>
            {/* Filter box pinned to the bottom of the tag pane (Zotero-style). */}
            <div className="read-tag-filter">
              <input
                value={tagFilter}
                onChange={(e) => setTagFilter(e.target.value)}
                placeholder={t('read.filterTags')}
                aria-label={t('read.filterTags')}
              />
              {tagFilter !== '' && (
                <button className="read-tag-filter-x" title={t('common.cancel')} onClick={() => setTagFilter('')}>
                  <Icon name="close" size={13} />
                </button>
              )}
            </div>
          </div>
        </aside>

        <ResizeHandle
          onResize={(dx) =>
            setRailW((w) => {
              const n = clamp(w + dx, 150, 460);
              saveWidth('termipod.read.railW', n);
              return n;
            })
          }
        />
          </>
        )}

        <div className="read-center">
          {mode === 'discover' ? (
            <DiscoverPanel
              onSelect={(id) => {
                setMode('library');
                setSelected(id);
              }}
            />
          ) : (
            <div className="read-list-col">
              <div className="read-list-bar">
                <input
                  className="read-search"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder={t('read.filter')}
                />
                <button onClick={addBlank}>+ {t('read.add')}</button>
              </div>
              <div className="read-table-wrap scroll">
                {items.length === 0 ? (
                  <div className="muted region-pad">
                    {references.length === 0 ? t('read.emptyLibrary') : t('read.noMatch')}
                  </div>
                ) : (
                  <TableVirtuoso
                    data={items}
                    context={libRowCtx}
                    style={{ height: '100%' }}
                    components={LIB_TABLE_COMPONENTS}
                    computeItemKey={(_i, r) => r.id}
                    fixedHeaderContent={() => (
                      <tr>
                        {SORT_COLS.map((c) => (
                          <th
                            key={c.key}
                            className={`read-th${sortKey === c.key ? ' sorted' : ''}`}
                            aria-sort={
                              sortKey === c.key ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'
                            }
                          >
                            <button
                              type="button"
                              className="read-th-btn"
                              aria-label={t('a11y.sortBy').replace('{col}', t(c.labelKey))}
                              onClick={() => toggleSort(c.key)}
                            >
                              {t(c.labelKey)}
                              <span className="read-th-arrow">
                                {sortKey === c.key && (
                                  <Icon name={sortDir === 'asc' ? 'chevron-up' : 'chevron-down'} size={12} />
                                )}
                              </span>
                            </button>
                          </th>
                        ))}
                      </tr>
                    )}
                    itemContent={(_i, r) => {
                      const rowPrimary = primaryAttachment(r);
                      const rowKind = rowPrimary !== undefined ? viewKindFor(rowPrimary.file) : 'pdf';
                      return (
                        <>
                          <td className="read-td-title">
                            {hasAnyAttachment(r) && (
                              <span
                                className="read-pdf-dot"
                                title={t('read.openInReader')}
                                onClick={(e) => {
                                  e.stopPropagation();
                                  openPdfTab(r.id);
                                }}
                              >
                                <Icon name={KIND_ICON[rowKind]} size={13} />
                              </span>
                            )}
                            {r.title !== '' ? r.title : t('read.untitled')}
                          </td>
                          <td>
                            {r.authors[0] ?? ''}
                            {r.authors.length > 1 ? ' et al.' : ''}
                          </td>
                          <td className="tnum">{r.year ?? ''}</td>
                          <td className="read-td-venue">{r.venue ?? ''}</td>
                          <td className="read-td-type">{r.type}</td>
                        </>
                      );
                    }}
                  />
                )}
              </div>
            </div>
          )}
        </div>

        {inspCollapsed ? (
          <button className="read-pane-expand" title={t('read.showDetails')} onClick={() => foldInsp(false)}>
            <Icon name="chevron-left" />
          </button>
        ) : (
          <>
        <ResizeHandle
          onResize={(dx) =>
            setInspW((w) => {
              const n = clamp(w - dx, 280, 820);
              saveWidth('termipod.read.inspW', n);
              return n;
            })
          }
        />

        <aside className="read-inspector-pane" style={{ width: inspW }}>
          {selected !== null ? (
            <Inspector
              refId={selected}
              onOpenReader={openPdfTab}
              onOpenNote={openNoteTab}
              onCollapse={() => foldInsp(true)}
            />
          ) : (
            <div className="ref-inspector-empty-wrap">
              <div className="ref-tabs">
                <span className="spacer" />
                <button className="read-fold" title={t('read.collapse')} onClick={() => foldInsp(true)}>
                  <Icon name="chevron-right" />
                </button>
              </div>
              <div className="muted region-pad ref-inspector-empty">{t('read.pickItem')}</div>
            </div>
          )}
        </aside>
          </>
        )}
        {showAgent && (
          <>
            <ResizeHandle
              onResize={(dx) =>
                setAgentW((w) => {
                  const n = clamp(w - dx, 280, 720);
                  saveWidth('termipod.read.agentW', n);
                  return n;
                })
              }
            />
            <aside className="read-agent" style={{ width: agentW }}>
              <AgentCompanion
                storageKey="termipod.read.agent"
                context={
                  selectedRef !== undefined
                    ? {
                        label: selectedRef.title !== '' ? selectedRef.title : t('read.untitled'),
                        build: () => {
                          const parts = [`Paper: "${selectedRef.title}"`];
                          if (selectedRef.authors.length > 0) parts.push(`Authors: ${selectedRef.authors.join(', ')}`);
                          if (selectedRef.year !== undefined) parts.push(`Year: ${selectedRef.year}`);
                          if (selectedRef.abstract !== undefined && selectedRef.abstract !== '')
                            parts.push(`Abstract: ${selectedRef.abstract}`);
                          return parts.join('\n');
                        },
                      }
                    : undefined
                }
              />
            </aside>
          </>
        )}
          </div>
        </>
      )}
      {rowMenu !== null &&
        (() => {
          const r = references.find((x) => x.id === rowMenu.id);
          if (r === undefined) return null;
          const hasPdf = hasAnyAttachment(r);
          return (
            <div
              className="read-ctxmenu"
              style={{ left: rowMenu.x, top: rowMenu.y }}
              onContextMenu={(e) => e.preventDefault()}
              onClick={(e) => e.stopPropagation()}
            >
              {hasPdf && (
                <button
                  className="read-ctx-item"
                  onClick={() => {
                    setRowMenu(null);
                    openPdfTab(r.id);
                  }}
                >
                  <Icon name="window" size={14} /> {t('read.openInReader')}
                </button>
              )}
              <button
                className="read-ctx-item"
                onClick={() => {
                  setRowMenu(null);
                  openNoteTab(r.id);
                }}
              >
                <Icon name="note" size={14} /> {t('read.openNoteTab')}
              </button>
              {isTauri() && (
                <button className="read-ctx-item" onClick={() => void ctxAddAttachment(r.id)}>
                  <Icon name="plus" size={14} /> {t('read.ctxAddAttachment')}
                </button>
              )}
              <div className="read-ctx-sep" />
              <button
                className="read-ctx-item"
                onClick={() => {
                  setRowMenu(null);
                  copy(citeApa(r));
                }}
              >
                <Icon name="copy" size={14} /> {t('read.ctxCopyCite')}
              </button>
              <button
                className="read-ctx-item"
                onClick={() => {
                  setRowMenu(null);
                  copy(citeBibtex(r));
                }}
              >
                <Icon name="copy" size={14} /> {t('read.ctxCopyBibtex')}
              </button>
              <button
                className="read-ctx-item"
                onClick={() => {
                  setRowMenu(null);
                  copy(r.title !== '' ? r.title : t('read.untitled'));
                }}
              >
                <Icon name="copy" size={14} /> {t('read.ctxCopyTitle')}
              </button>
              {collection !== ALL && (
                <>
                  <div className="read-ctx-sep" />
                  <button
                    className="read-ctx-item"
                    onClick={() => {
                      setRowMenu(null);
                      updateReference(r.id, {
                        collectionIds: r.collectionIds.filter((c) => c !== collection),
                      });
                    }}
                  >
                    <Icon name="close" size={14} /> {t('read.ctxRemoveFromCollection')}
                  </button>
                </>
              )}
              <div className="read-ctx-sep" />
              {menuConfirm ? (
                <button
                  className="read-ctx-item danger"
                  onClick={() => {
                    setRowMenu(null);
                    if (selected === r.id) setSelected(null);
                    removeReference(r.id);
                  }}
                >
                  <Icon name="trash" size={14} /> {t('read.ctxDeleteConfirm')}
                </button>
              ) : (
                <button
                  className="read-ctx-item danger"
                  onClick={(e) => {
                    e.stopPropagation();
                    setMenuConfirm(true);
                  }}
                >
                  <Icon name="trash" size={14} /> {t('read.ctxDelete')}
                </button>
              )}
            </div>
          );
        })()}

      {railBlankMenu.node}

      {/* Collection context menu (right-click a collection in the rail). */}
      {colMenu !== null &&
        (() => {
          const c = collections.find((x) => x.id === colMenu.id);
          if (c === undefined) return null;
          return (
            <div
              className="read-ctxmenu"
              style={{ left: colMenu.x, top: colMenu.y }}
              onContextMenu={(e) => e.preventDefault()}
              onClick={(e) => e.stopPropagation()}
            >
              <button
                className="read-ctx-item"
                onClick={() => {
                  setColMenu(null);
                  void promptRenameCollection(c.id, c.name);
                }}
              >
                <Icon name="pen" size={14} /> {t('read.renameCollection')}
              </button>
              <button
                className="read-ctx-item"
                onClick={() => {
                  setColMenu(null);
                  void newCollection();
                }}
              >
                <Icon name="plus" size={14} /> {t('read.newCollection')}
              </button>
              <div className="read-ctx-sep" />
              <button
                className="read-ctx-item danger"
                onClick={() => {
                  setColMenu(null);
                  removeCollection(c.id);
                  if (collection === c.id) setCollection(ALL);
                }}
              >
                <Icon name="trash" size={14} /> {t('read.deleteCollection')}
              </button>
            </div>
          );
        })()}

      {/* Tag context menu (right-click a tag in the rail). Rename/delete sweep the
          whole library, since tags are plain strings on each reference. */}
      {tagMenu !== null && (
        <div
          className="read-ctxmenu"
          style={{ left: tagMenu.x, top: tagMenu.y }}
          onContextMenu={(e) => e.preventDefault()}
          onClick={(e) => e.stopPropagation()}
        >
          <button
            className="read-ctx-item"
            onClick={() => {
              const name = tagMenu.name;
              setTagMenu(null);
              void promptRenameTag(name);
            }}
          >
            <Icon name="pen" size={14} /> {t('read.renameTag')}
          </button>
          <div className="read-ctx-sep" />
          <button
            className="read-ctx-item danger"
            onClick={() => {
              const name = tagMenu.name;
              setTagMenu(null);
              removeTag(name);
              if (tag === name) setTag(null);
            }}
          >
            <Icon name="trash" size={14} /> {t('read.deleteTag')}
          </button>
        </div>
      )}
      {promptNode}
      </OpenLinkContext.Provider>
    </WorkbenchSurface>
  );
}
