import { useEffect, useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import {
  REF_TYPES,
  useLibrary,
  type Reference,
  type RefType,
  type WorkLink,
} from '../state/library';
import { hasAttachment, loadAttachmentBlob, useZoteroStorage } from '../state/zoteroStorage';
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
import { isTauri } from '../platform';
import { BrowserView } from './BrowserView';
import { Markdown } from '../ui/Markdown';
import { OpenLinkContext, useOpenLink } from '../ui/OpenLinkContext';
import { PdfCanvas } from '../ui/PdfCanvas';
import { ResizeHandle } from '../ui/ResizeHandle';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';

function hostOf(url: string): string {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
}

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
type Tab = 'info' | 'read' | 'notes' | 'cite' | 'meta';
const ALL = '__all__';

// An open tab in the reader region: a PDF reader (a library item) or an in-app
// browser (an arbitrary URL). `activeTab === null` shows the library instead.
interface ReadTab {
  id: string;
  kind: 'pdf' | 'web';
  refId?: string;
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

// Viewer for a local attachment. Bytes are resolved from the linked storage
// folder (a live File in the browser build, or read through the Rust core under
// Tauri). PDFs render via bundled pdf.js (PdfCanvas) — canvas rendering works on
// every platform, unlike the old `<iframe src=blob>` which WebView2 (Windows/
// Edge) refused ("此页面已被 Microsoft Edge 阻止") and whose in-PDF links hijacked
// the SPA. Non-PDF attachments (HTML snapshots) keep the iframe path; that object
// URL is revoked on unmount so bytes aren't retained after the reader closes.
function PdfView({ att }: { att: { key: string; file: string } }): JSX.Element {
  const t = useT();
  const rels = useZoteroStorage((s) => s.rels);
  const files = useZoteroStorage((s) => s.files);
  const path = useZoteroStorage((s) => s.path);
  const [buf, setBuf] = useState<ArrayBuffer | null>(null);
  const [htmlUrl, setHtmlUrl] = useState<string | null>(null);
  const [err, setErr] = useState(false);
  const isPdf = /\.pdf$/i.test(att.file);
  useEffect(() => {
    let alive = true;
    let made: string | null = null;
    setBuf(null);
    setHtmlUrl(null);
    setErr(false);
    void loadAttachmentBlob({ rels, files, path }, att).then(async (blob) => {
      if (!alive) return;
      if (blob === null) {
        setErr(true);
        return;
      }
      if (isPdf || blob.type === 'application/pdf') {
        const ab = await blob.arrayBuffer();
        if (alive) setBuf(ab);
      } else {
        made = URL.createObjectURL(blob);
        setHtmlUrl(made);
      }
    });
    return () => {
      alive = false;
      if (made !== null) URL.revokeObjectURL(made);
    };
  }, [att.key, att.file, rels, files, path, isPdf]);
  if (err) return <div className="muted region-pad">{t('read.pdfNotFound')}</div>;
  if (buf !== null) return <PdfCanvas data={buf} fileName={att.file} />;
  if (htmlUrl !== null) return <iframe className="pdf-frame" title={att.file} src={htmlUrl} />;
  return <div className="muted region-pad">{t('read.loadingPdf')}</div>;
}

// ---- Inspector -------------------------------------------------------------

// A list of works in the citation graph (references or citations). Each title
// opens in the in-app browser tab (DOI landing or OpenAlex page).
function WorkList({ label, works }: { label: string; works?: WorkLink[] }): JSX.Element | null {
  const openLink = useOpenLink();
  if (works === undefined || works.length === 0) return null;
  const href = (w: WorkLink): string => (w.doi !== undefined ? `https://doi.org/${w.doi}` : (w.id ?? ''));
  return (
    <div className="ref-meta-sec">
      <div className="ref-section-label">
        {label} <span className="muted small">({works.length})</span>
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
function RefMeta({
  ref,
  scraping,
  msg,
  onScrape,
}: {
  ref: Reference;
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

      <WorkList label={t('read.mRefList')} works={ref.references} />
      <WorkList label={t('read.mCiteList')} works={ref.citations} />
    </div>
  );
}

function Inspector({
  refId,
  onOpenReader,
  onCollapse,
  embedded,
}: {
  refId: string;
  onOpenReader?: (id: string) => void;
  onCollapse?: () => void;
  embedded?: boolean;
}): JSX.Element {
  const t = useT();
  const ref = useLibrary((s) => s.references.find((r) => r.id === refId));
  const collections = useLibrary((s) => s.collections);
  const update = useLibrary((s) => s.updateReference);
  const remove = useLibrary((s) => s.removeReference);
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

  const att = ref.zoteroStorage;
  const attPresent = hasAttachment({ rels, files }, att);

  const tabs: { id: Tab; label: string }[] = [
    { id: 'info', label: t('read.tabInfo') },
    { id: 'read', label: t('read.tabRead') },
    { id: 'notes', label: t('read.tabNotes') },
    { id: 'cite', label: t('read.tabCite') },
    { id: 'meta', label: t('read.tabMeta') },
  ];

  return (
    <div className="ref-inspector">
      <div className="ref-tabs">
        {onCollapse !== undefined && (
          <button className="read-fold" title={t('read.collapse')} onClick={onCollapse}>
            ›
          </button>
        )}
        {tabs.map((tb) => (
          <button key={tb.id} className={tab === tb.id ? 'ref-tab active' : 'ref-tab'} onClick={() => setTab(tb.id)}>
            {tb.label}
          </button>
        ))}
        <span className="spacer" />
        {att !== undefined &&
          !embedded &&
          (attPresent ? (
            <button
              className="ref-pdf-btn"
              title={t('read.openInReader')}
              onClick={() => onOpenReader?.(ref.id)}
            >
              ⧉ PDF
            </button>
          ) : (
            <button
              className="ref-pdf-btn muted"
              title={storageLinked ? t('read.pdfNotFound') : t('read.pdfLinkHint')}
              onClick={() => setTab('read')}
            >
              PDF
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
          {scraping ? '⋯' : '⟳'} {t('read.scrape')}
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
                      ↗
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
                      ↗
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
            {att !== undefined && !embedded && (
              <div className="ref-attach">
                {attPresent ? (
                  <button className="primary small" onClick={() => onOpenReader?.(ref.id)}>
                    ⧉ {t('read.openInReader')}
                  </button>
                ) : storageLinked ? (
                  <div className="muted small">
                    {t('read.pdfNotFound')} <span className="mono">{att.file}</span>
                  </div>
                ) : (
                  <div className="muted small">
                    {t('read.pdfLinkHint')} <span className="mono">{att.file}</span>
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
                <Markdown text={ref.bodyMarkdown ?? ''} />
                <button className="link-btn" onClick={() => setEditingBody(true)}>
                  {t('read.editBody')}
                </button>
              </>
            )}
          </div>
        )}

        {tab === 'notes' && (
          <textarea
            className="editor-pane"
            value={ref.notes}
            onChange={(e) => update(ref.id, { notes: e.target.value })}
            placeholder={t('read.notesPlaceholder')}
          />
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

        {tab === 'meta' && <RefMeta ref={ref} scraping={scraping} msg={scrapeMsg} onScrape={() => void runScrape()} />}
      </div>
    </div>
  );
}

// ---- Reader ----------------------------------------------------------------

// A dedicated reading view (one open PDF tab). The PDF is the main pane; a
// resizable side column reuses the Inspector (Info / Read / Notes / Cite) so
// notes are written next to the document. Multiple of these live behind the tab
// strip; switching tabs swaps which one renders (director: "the PDF viewer can be
// opened in several tabs at the same time").
function ReaderView({ refId, onGone }: { refId: string; onGone: () => void }): JSX.Element {
  const t = useT();
  const ref = useLibrary((s) => s.references.find((r) => r.id === refId));
  const openLink = useOpenLink();
  const [sideW, setSideW] = useState(() => loadWidth('termipod.read.readerSideW', 420));

  useEffect(() => {
    if (ref === undefined) onGone(); // deleted while open — drop the tab
  }, [ref, onGone]);
  if (ref === undefined) return <></>;

  const att = ref.zoteroStorage;
  const url = ref.url;
  return (
    <div className="reader-view">
      <div className="reader-topbar">
        <div className="reader-title" title={ref.title}>
          {ref.title !== '' ? ref.title : t('read.untitled')}
        </div>
        <span className="spacer" />
        {url !== undefined && url !== '' && (
          <button className="link-btn" title={t('read.openUrl')} onClick={() => openLink(url)}>
            {t('read.openUrl')} ↗
          </button>
        )}
      </div>
      <div className="reader-body">
        <div className="reader-doc">
          {att !== undefined ? (
            <PdfView att={att} />
          ) : (
            <div className="muted region-pad">{t('read.noPdf')}</div>
          )}
        </div>
        <ResizeHandle
          onResize={(dx) =>
            setSideW((w) => {
              const n = clamp(w - dx, 300, 760);
              saveWidth('termipod.read.readerSideW', n);
              return n;
            })
          }
        />
        <aside className="reader-side" style={{ width: sideW }}>
          <Inspector refId={refId} embedded />
        </aside>
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
          <input
            className="discover-key-input"
            type="password"
            value={key}
            placeholder={t('read.apiKeyPlaceholder')}
            onChange={(e) => {
              setKey(e.target.value);
              if (source.keyKey !== undefined) lsSet(source.keyKey, e.target.value);
            }}
          />
          {source.keyUrl !== undefined && (
            <button className="link-btn" onClick={() => openLink(source.keyUrl ?? '')}>
              {t('read.getApiKey')} ↗
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
  const references = useLibrary((s) => s.references);
  const collections = useLibrary((s) => s.collections);
  const addReference = useLibrary((s) => s.addReference);
  const addCollection = useLibrary((s) => s.addCollection);
  const removeCollection = useLibrary((s) => s.removeCollection);
  const importReferences = useLibrary((s) => s.importReferences);
  const linkFolder = useZoteroStorage((s) => s.linkFolder);
  const linkNative = useZoteroStorage((s) => s.linkNative);
  const reindex = useZoteroStorage((s) => s.reindex);
  const storageCount = useZoteroStorage((s) => s.count);

  const [mode, setMode] = useState<Mode>('library');
  const [collection, setCollection] = useState<string>(ALL);
  const [tag, setTag] = useState<string | null>(null);
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
  const [railW, setRailW] = useState(() => loadWidth('termipod.read.railW', 220));
  const [inspW, setInspW] = useState(() => loadWidth('termipod.read.inspW', 380));
  const [railCollapsed, setRailCollapsed] = useState(() => localStorage.getItem('termipod.read.railFold') === '1');
  const [inspCollapsed, setInspCollapsed] = useState(() => localStorage.getItem('termipod.read.inspFold') === '1');
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

  function openPdfTab(refId: string): void {
    const existing = tabs.find((tb) => tb.kind === 'pdf' && tb.refId === refId);
    if (existing !== undefined) {
      setActiveTab(existing.id);
      return;
    }
    const r = useLibrary.getState().references.find((x) => x.id === refId);
    const id = nextTabId();
    const title = r !== undefined && r.title !== '' ? r.title : t('read.untitled');
    setTabs((ts) => [...ts, { id, kind: 'pdf', refId, title }]);
    setActiveTab(id);
  }

  function openWebTab(url: string): void {
    if (url === '') return;
    const id = nextTabId();
    setTabs((ts) => [...ts, { id, kind: 'web', url, title: hostOf(url) }]);
    setActiveTab(id);
  }

  function closeTab(id: string): void {
    setTabs((ts) => ts.filter((tb) => tb.id !== id));
    setActiveTab((a) => (a === id ? null : a));
  }

  // Re-index the persisted storage-folder path on mount (Tauri) so the link
  // survives a restart instead of being lost (director report).
  useEffect(() => {
    if (isTauri()) void reindex();
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
      const err = await linkNative();
      if (err !== null) setImportMsg(err);
    } else {
      dirRef.current?.click();
    }
  }

  // Attachments exist but no folder is linked — prompt a (re-)link.
  const needsRelink = useMemo(
    () => storageCount === 0 && references.some((r) => r.zoteroStorage !== undefined),
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
    references.forEach((r) => r.tags.forEach((tg) => s.add(tg)));
    return [...s].sort();
  }, [references]);

  const items = useMemo(() => {
    const ql = query.trim().toLowerCase();
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
  }, [references, collection, tag, query, sortKey, sortDir]);

  function toggleSort(key: SortKey): void {
    if (key === sortKey) setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
    else {
      setSortKey(key);
      setSortDir('asc');
    }
  }

  const activeTabObj = activeTab !== null ? tabs.find((tb) => tb.id === activeTab) : undefined;

  function newCollection(): void {
    const name = window.prompt(t('read.newCollectionPrompt'));
    if (name !== null && name.trim() !== '') setCollection(addCollection(name.trim()));
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
          <div className="seg">
            <button className={mode === 'library' ? 'seg-btn active' : 'seg-btn'} onClick={() => setMode('library')}>
              {t('read.modeLibrary')}
            </button>
            <button className={mode === 'discover' ? 'seg-btn active' : 'seg-btn'} onClick={() => setMode('discover')}>
              {t('read.modeDiscover')}
            </button>
          </div>
        </>
      }
    >
      <OpenLinkContext.Provider value={openWebTab}>
      {importMsg !== null && (
        <div className="read-import-msg">
          <span>{importMsg}</span>
          <span className="spacer" />
          <button className="link-btn" onClick={() => setImportMsg(null)}>
            ×
          </button>
        </div>
      )}
      {tabs.length > 0 && (
        <div className="read-tabstrip">
          <button
            className={`read-tabitem${activeTab === null ? ' active' : ''}`}
            onClick={() => setActiveTab(null)}
          >
            {t('read.tabLibrary')}
          </button>
          {tabs.map((tb) => (
            <span key={tb.id} className={`read-tabitem${activeTab === tb.id ? ' active' : ''}`}>
              <button className="read-tabitem-label" title={tb.title} onClick={() => setActiveTab(tb.id)}>
                <span className="read-tabitem-kind">{tb.kind === 'web' ? '🌐' : '📄'}</span>
                {tb.title}
              </button>
              <button className="read-tabitem-x" title={t('read.closeTab')} onClick={() => closeTab(tb.id)}>
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      {activeTabObj !== undefined ? (
        activeTabObj.kind === 'pdf' && activeTabObj.refId !== undefined ? (
          <ReaderView refId={activeTabObj.refId} onGone={() => closeTab(activeTabObj.id)} />
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
            ›
          </button>
        ) : (
          <>
        <aside className="read-rail" style={{ width: railW }}>
          <div className="read-rail-head">
            <button className="read-fold" title={t('read.collapse')} onClick={() => foldRail(true)}>
              ‹
            </button>
          </div>
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
                  ×
                </span>
              </button>
            ))}
            <button className="read-col add" onClick={newCollection}>
              + {t('read.newCollection')}
            </button>
          </div>
          {allTags.length > 0 && (
            <div className="read-rail-group">
              <div className="read-rail-label">{t('read.tags')}</div>
              <div className="read-tags">
                {allTags.map((tg) => (
                  <button
                    key={tg}
                    className={`read-tag${tag === tg ? ' active' : ''}`}
                    onClick={() => setTag(tag === tg ? null : tg)}
                  >
                    {tg}
                  </button>
                ))}
              </div>
            </div>
          )}
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
                  <table className="read-table">
                    <colgroup>
                      <col style={{ width: '44%' }} />
                      <col style={{ width: '20%' }} />
                      <col style={{ width: '3.5rem' }} />
                      <col style={{ width: '22%' }} />
                      <col style={{ width: '6rem' }} />
                    </colgroup>
                    <thead>
                      <tr>
                        {SORT_COLS.map((c) => (
                          <th
                            key={c.key}
                            className={`read-th${sortKey === c.key ? ' sorted' : ''}`}
                            onClick={() => toggleSort(c.key)}
                          >
                            {t(c.labelKey)}
                            <span className="read-th-arrow">
                              {sortKey === c.key ? (sortDir === 'asc' ? ' ▲' : ' ▼') : ''}
                            </span>
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {items.map((r) => {
                        const hasPdf = r.zoteroStorage !== undefined;
                        return (
                          <tr
                            key={r.id}
                            className={selected === r.id ? 'active' : ''}
                            onClick={() => setSelected(r.id)}
                            onDoubleClick={() => {
                              if (hasPdf) openPdfTab(r.id);
                            }}
                          >
                            <td className="read-td-title">
                              {hasPdf && (
                                <span
                                  className="read-pdf-dot"
                                  title={t('read.openInReader')}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    openPdfTab(r.id);
                                  }}
                                >
                                  ⧉
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
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                )}
              </div>
            </div>
          )}
        </div>

        {inspCollapsed ? (
          <button className="read-pane-expand" title={t('read.showDetails')} onClick={() => foldInsp(false)}>
            ‹
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
            <Inspector refId={selected} onOpenReader={openPdfTab} onCollapse={() => foldInsp(true)} />
          ) : (
            <div className="ref-inspector-empty-wrap">
              <div className="ref-tabs">
                <span className="spacer" />
                <button className="read-fold" title={t('read.collapse')} onClick={() => foldInsp(true)}>
                  ›
                </button>
              </div>
              <div className="muted region-pad ref-inspector-empty">{t('read.pickItem')}</div>
            </div>
          )}
        </aside>
          </>
        )}
          </div>
        </>
      )}
      </OpenLinkContext.Provider>
    </WorkbenchSurface>
  );
}
