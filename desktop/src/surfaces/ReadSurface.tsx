import { useEffect, useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import {
  REF_TYPES,
  useLibrary,
  type Reference,
  type RefType,
} from '../state/library';
import { resolveAttachment, useZoteroStorage } from '../state/zoteroStorage';
import { searchPapers, type DiscoveryPaper } from '../discovery/semanticScholar';
import { Markdown } from '../ui/Markdown';
import { ResizeHandle } from '../ui/ResizeHandle';
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
type Tab = 'info' | 'read' | 'notes' | 'cite';
const ALL = '__all__';

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

// Inline viewer for a local attachment — a blob URL into an <iframe>, which the
// WebView renders natively for PDF/HTML. The object URL is revoked on unmount so
// bytes aren't retained after the reader closes it.
function PdfView({ file }: { file: File }): JSX.Element {
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    const u = URL.createObjectURL(file);
    setUrl(u);
    return () => URL.revokeObjectURL(u);
  }, [file]);
  if (url === null) return <></>;
  return <iframe className="pdf-frame" title={file.name} src={url} />;
}

// ---- Inspector -------------------------------------------------------------

function Inspector({ refId }: { refId: string }): JSX.Element {
  const t = useT();
  const ref = useLibrary((s) => s.references.find((r) => r.id === refId));
  const collections = useLibrary((s) => s.collections);
  const update = useLibrary((s) => s.updateReference);
  const remove = useLibrary((s) => s.removeReference);
  const files = useZoteroStorage((s) => s.files);
  const storageLinked = useZoteroStorage((s) => s.count > 0);
  const [tab, setTab] = useState<Tab>('info');
  const [pdfOpen, setPdfOpen] = useState(false);
  useEffect(() => setPdfOpen(false), [refId]);

  if (ref === undefined) return <div className="muted region-pad">{t('read.pickItem')}</div>;

  const att = ref.zoteroStorage;
  const attFile = resolveAttachment(files, att);

  const tabs: { id: Tab; label: string }[] = [
    { id: 'info', label: t('read.tabInfo') },
    { id: 'read', label: t('read.tabRead') },
    { id: 'notes', label: t('read.tabNotes') },
    { id: 'cite', label: t('read.tabCite') },
  ];

  return (
    <div className="ref-inspector">
      <div className="ref-tabs">
        {tabs.map((tb) => (
          <button key={tb.id} className={tab === tb.id ? 'ref-tab active' : 'ref-tab'} onClick={() => setTab(tb.id)}>
            {tb.label}
          </button>
        ))}
        <span className="spacer" />
        <button className="link-btn danger" onClick={() => remove(ref.id)}>
          {t('read.delete')}
        </button>
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
                {t('read.fDoi')}
                <input value={ref.doi ?? ''} onChange={(e) => update(ref.id, { doi: e.target.value || undefined })} />
              </label>
              <label className="grow">
                {t('read.fUrl')}
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
            {att !== undefined && (
              <div className="ref-attach">
                {attFile !== undefined ? (
                  <>
                    <button className="primary small" onClick={() => setPdfOpen((o) => !o)}>
                      {pdfOpen ? t('read.hidePdf') : t('read.openPdf')}
                    </button>
                    {pdfOpen && <PdfView file={attFile} />}
                  </>
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
            {ref.bodyMarkdown !== undefined && ref.bodyMarkdown !== '' ? (
              <Markdown text={ref.bodyMarkdown} />
            ) : (
              <textarea
                className="editor-pane ref-body-edit"
                value={ref.bodyMarkdown ?? ''}
                onChange={(e) => update(ref.id, { bodyMarkdown: e.target.value })}
                placeholder={t('read.bodyPlaceholder')}
              />
            )}
            {ref.bodyMarkdown !== undefined && ref.bodyMarkdown !== '' && (
              <button className="link-btn" onClick={() => update(ref.id, { bodyMarkdown: '' })}>
                {t('read.editBody')}
              </button>
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
      </div>
    </div>
  );
}

// ---- Discover --------------------------------------------------------------

function DiscoverPanel({ onSelect }: { onSelect: (id: string) => void }): JSX.Element {
  const t = useT();
  const add = useLibrary((s) => s.addReference);
  const references = useLibrary((s) => s.references);
  const [q, setQ] = useState('');
  const [results, setResults] = useState<DiscoveryPaper[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const importedIds = useMemo(
    () => new Set(references.map((r) => r.externalId).filter((x): x is string => x !== undefined)),
    [references],
  );

  async function run(): Promise<void> {
    if (q.trim() === '') return;
    setBusy(true);
    setErr(null);
    try {
      setResults(await searchPapers(q, 25));
    } catch (e) {
      setErr(e instanceof Error && e.message === 'rate-limited' ? t('read.rateLimited') : t('read.searchFailed'));
      setResults([]);
    } finally {
      setBusy(false);
    }
  }

  function importPaper(p: DiscoveryPaper): void {
    const id = add(paperToRef(p));
    onSelect(id);
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
      </div>
      <div className="discover-source muted small">{t('read.discoverSource')}</div>
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
  const storageCount = useZoteroStorage((s) => s.count);

  const [mode, setMode] = useState<Mode>('library');
  const [collection, setCollection] = useState<string>(ALL);
  const [tag, setTag] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState<string | null>(null);
  const [importing, setImporting] = useState(false);
  const [importMsg, setImportMsg] = useState<string | null>(null);
  const [railW, setRailW] = useState(() => loadWidth('termipod.read.railW', 220));
  const [inspW, setInspW] = useState(() => loadWidth('termipod.read.inspW', 380));
  const fileRef = useRef<HTMLInputElement>(null);
  const dirRef = useRef<HTMLInputElement>(null);

  // `webkitdirectory` isn't in the input TS types — set it (plus the vendor
  // aliases) imperatively so the picker selects a folder, not a single file.
  useEffect(() => {
    const el = dirRef.current;
    if (el === null) return;
    el.setAttribute('webkitdirectory', '');
    el.setAttribute('directory', '');
    el.setAttribute('mozdirectory', '');
  }, []);

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
          .replace('{s}', String(res.skipped))
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
    return references.filter((r) => {
      if (collection !== ALL && !r.collectionIds.includes(collection)) return false;
      if (tag !== null && !r.tags.includes(tag)) return false;
      if (ql !== '' && !`${r.title} ${r.authors.join(' ')} ${r.venue ?? ''}`.toLowerCase().includes(ql)) return false;
      return true;
    });
  }, [references, collection, tag, query]);

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
            className="import-btn"
            title={t('read.linkStorageHint')}
            onClick={() => dirRef.current?.click()}
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
      {importMsg !== null && (
        <div className="read-import-msg">
          <span>{importMsg}</span>
          <span className="spacer" />
          <button className="link-btn" onClick={() => setImportMsg(null)}>
            ×
          </button>
        </div>
      )}
      <div className="read-layout">
        <aside className="read-rail" style={{ width: railW }}>
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
              <div className="read-list scroll">
                {items.length === 0 && (
                  <div className="muted region-pad">
                    {references.length === 0 ? t('read.emptyLibrary') : t('read.noMatch')}
                  </div>
                )}
                {items.map((r) => (
                  <button
                    key={r.id}
                    className={`read-item${selected === r.id ? ' active' : ''}`}
                    onClick={() => setSelected(r.id)}
                  >
                    <div className="read-item-title">{r.title !== '' ? r.title : t('read.untitled')}</div>
                    <div className="read-item-meta muted small">
                      {r.authors.slice(0, 3).join(', ')}
                      {r.authors.length > 3 ? ' et al.' : ''}
                      {r.year !== undefined ? ` · ${r.year}` : ''}
                    </div>
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>

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
            <Inspector refId={selected} />
          ) : (
            <div className="muted region-pad ref-inspector-empty">{t('read.pickItem')}</div>
          )}
        </aside>
      </div>
    </WorkbenchSurface>
  );
}
