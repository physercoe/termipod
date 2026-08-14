import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { invoke } from '../bridge';
import { Icon, type IconName } from '../ui/Icon';
import { InspectFileIcon } from '../ui/InspectFileIcon';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';
import type { CodeViewHandle } from '../ui/CodeView';
import { kindForInspectFile, useInspect, type InspectKind, type InspectRef, type InspectTab } from '../state/inspect';
import { classifyArch, parseHfConfig, parsePolicyConfig } from '../state/checkpoint';
import { buildArchSchematic } from '../state/archSchematic';
import { useInspectRoots } from '../state/inspectRoots';
import { looksLikeDot } from '../state/dotGraph';
import type { GraphCollection } from '../state/modelGraph';
import { TraceModal } from '../ui/TraceModal';
import { CallGraphModal } from '../ui/CallGraphModal';
import { useWorkspace } from '../state/workspace';
import { readRef, readSource, readSourceBytes, sftpSessionFor } from '../state/inspectSources';
import { canStreamMedia, isNotTextError, mediaFileUrl, mediaSftpUrl, PDF_PREVIEW_CAP } from '../state/inspectMedia';
import { useSession } from '../state/session';
import { runScript, type ScriptResult } from '../state/scriptRun';
import { parseTrace, type ParsedTrace, type TraceFrame } from '../state/stackTrace';
import { looksLikePatch } from '../state/patch';
import { looksLikeLog } from '../state/ansi';
import { extractSymbols, SUPPORTED_LANGS, type CodeSymbol } from '../state/treeSitter';
import { CodeOutline } from '../ui/CodeOutline';
import { usePanelWidth, ResizeHandle } from '../ui/ResizeHandle';
import type { LogSource } from '../ui/LogView';
import { InspectOpenDialog, type OpenMode, type PickResult, type PinRoot } from './InspectOpen';
import { InspectTree } from './InspectTree';
import { PaneStateCard } from './PaneStateCard';
import { PaneStatePickDialog } from './PaneStatePick';
import { InspectRepoAddDialog } from './InspectRepoAdd';
import { RepoPickDialog } from './InspectForgePick';
import { PopoverMenu } from '../ui/PopoverMenu';

// CodeMirror 6 + its search/language-data deps ride a lazy chunk (never the boot
// bundle — plan §7 bundle discipline), loaded the first time a code tab renders.
const CodeView = lazy(() => import('../ui/CodeView').then((m) => ({ default: m.CodeView })));
// Rich text/table readers remain off the boot path, just like every specialized
// Inspect viewer. Markdown shares Read's document renderer; CSV owns a lean grid.
const MarkdownReader = lazy(() => import('../ui/MarkdownReader').then((m) => ({ default: m.MarkdownReader })));
const DelimitedPreview = lazy(() => import('../ui/DelimitedPreview').then((m) => ({ default: m.DelimitedPreview })));
// W2 diff viewers — each its own lazy chunk (git-diff-view + @codemirror/merge
// never touch the boot bundle), loaded the first time a diff / compare tab shows.
const PatchDiffView = lazy(() => import('../ui/PatchDiffView').then((m) => ({ default: m.PatchDiffView })));
const TwoBlobCompare = lazy(() => import('../ui/TwoBlobCompare').then((m) => ({ default: m.TwoBlobCompare })));
// W3 — the virtualized log viewer (react-virtuoso + anser) rides its own lazy
// chunk, loaded the first time a log tab renders.
const LogView = lazy(() => import('../ui/LogView').then((m) => ({ default: m.LogView })));
// W4 — the checkpoint inspector (@huggingface/gguf runs main-side; this chunk is
// the UI) is loaded the first time a model tab renders.
const ModelView = lazy(() => import('../ui/ModelView').then((m) => ({ default: m.ModelView })));
// §5a — the config-only architecture view (ArchCard from a parsed config alone,
// any source), its own lazy chunk shared with ModelView.
const ConfigArchView = lazy(() => import('../ui/ModelView').then((m) => ({ default: m.ConfigArchView })));
// LeRobot policy configs (VLA/robot policies) — a different config schema, its
// own honest card view; shares the ModelView lazy chunk.
const PolicyConfigView = lazy(() => import('../ui/ModelView').then((m) => ({ default: m.PolicyConfigView })));
// The Graphviz DOT viewer — the wasm engine loads on first render (its own chunk).
const DotGraphView = lazy(() => import('../ui/DotGraphView').then((m) => ({ default: m.DotGraphView })));
// The interactive Model Explorer WebGL graph — the 2.5 MB element + worker load on
// first render (its own chunk; the runtime is self-hosted, never in the boot bundle).
const ModelExplorerView = lazy(() => import('../ui/ModelExplorerView').then((m) => ({ default: m.ModelExplorerView })));
// W4b — the interactive class-composition graph (React Flow + elkjs), its own lazy
// chunk (neither heavy dep touches the boot bundle), loaded when a modgraph tab opens.
const ModuleGraphView = lazy(() => import('../ui/ModuleGraphView').then((m) => ({ default: m.ModuleGraphView })));
// §5a follow-on — the config-only architecture **schematic** (React Flow + the
// pure block-diagram spec), its own lazy chunk (React Flow shared with the module
// graph), loaded when an archgraph tab opens.
const ArchSchematicView = lazy(() => import('../ui/ArchSchematicView').then((m) => ({ default: m.ArchSchematicView })));
// The PDF preview shares Read's pdf.js viewer — same pipeline, so the two
// surfaces agree on what a PDF looks like. Lazy for the usual reason: pdf.js is
// a multi-MB dependency and a static import would drag it into the boot bundle
// for every user who never opens one.
const PdfCanvas = lazy(() => import('../ui/PdfCanvas').then((m) => ({ default: m.PdfCanvas })));

/// J3 — the **Inspect** surface (label-only rename of "Debug"; the `debug` JobId
/// stays, see the round-2 plan §0a). The round-1 paste textarea becomes a tabbed
/// inspector: each tab is a viewer over one source. W1 ships the shell + the
/// **code** viewer (CodeMirror 6 + a stack-trace lens + run-scratch); the diff /
/// log / model kinds open as tabs but render an honest "coming next" placard
/// until W2 / W3 / W4 land.
///
/// Sources in W1: `paste` (a device-local scratch) and `local` (a file picked via
/// the native dialog). The workspace-tree / SFTP / hub-doc bridges are the W1
/// follow-on (the store already models those sources).

// ── path helpers (renderer-side, no node:path in the browser build) ──────────
function baseName(p: string): string {
  const s = p.replace(/[\\/]+$/, '');
  const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'));
  return i >= 0 ? s.slice(i + 1) : s;
}
function dirName(p: string): string {
  const i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
  return i >= 0 ? p.slice(0, i) : '';
}
function extOf(p: string): string {
  const b = baseName(p);
  const i = b.lastIndexOf('.');
  return i >= 0 ? b.slice(i + 1) : '';
}
function isAbs(p: string): boolean {
  return p.startsWith('/') || /^[A-Za-z]:[\\/]/.test(p);
}
function joinPath(dir: string, rel: string): string {
  if (dir === '') return rel;
  const sep = dir.includes('\\') && !dir.includes('/') ? '\\' : '/';
  return `${dir.replace(/[\\/]+$/, '')}${sep}${rel}`;
}

// Language ids offered for a paste scratch, and their run interpreter (when
// runnable via the existing `script_run`).
const LANGS = ['text', 'python', 'javascript', 'typescript', 'go', 'rust', 'bash', 'json', 'yaml', 'markdown', 'c', 'c++', 'html', 'css', 'sql'];
const RUN_INTERP: Record<string, string> = { python: 'python3', bash: 'bash', shell: 'bash', javascript: 'node' };

// Sources code2flow can build a static call graph for (plan §5 W4). `langId` covers
// py/js (mapped by langFromPath); ruby/php have no highlight mode so we also sniff
// the extension directly.
const CALLGRAPH_LANGS = new Set(['python', 'javascript']);
const CALLGRAPH_EXTS = new Set(['py', 'js', 'mjs', 'cjs', 'rb', 'php']);

// File extension → a coarse language id (for run-scratch + mode hinting on a
// file tab, where CodeView also self-detects from the filename).
function langFromPath(path: string | undefined): string | undefined {
  if (path === undefined) return undefined;
  const e = extOf(path).toLowerCase();
  const map: Record<string, string> = {
    py: 'python', sh: 'bash', bash: 'bash', zsh: 'bash', js: 'javascript', mjs: 'javascript', cjs: 'javascript',
    ts: 'typescript', tsx: 'typescript', go: 'go', rs: 'rust', json: 'json', yaml: 'yaml', yml: 'yaml',
    md: 'markdown', c: 'c', h: 'c', cpp: 'c++', cc: 'c++', css: 'css', html: 'html', sql: 'sql',
  };
  return map[e];
}

function kindIcon(kind: InspectKind): IconName {
  switch (kind) {
    case 'markdown':
      return 'note';
    case 'table':
      return 'table';
    case 'diff':
      return 'split-h';
    case 'log':
      return 'list-ordered';
    case 'model':
      return 'sliders';
    case 'graph':
      return 'diagram';
    case 'megraph':
      return 'canvas';
    case 'modgraph':
      return 'git-branch';
    case 'archgraph':
      return 'sitemap';
    case 'image':
      return 'image';
    case 'video':
      return 'film';
    case 'audio':
      return 'music';
    case 'pdf':
      return 'file-text';
    case 'panestate':
      return 'terminal';
    default:
      return 'code';
  }
}

// The failure a file-backed tab shows when its source could not be read.
//
// "This file is not text" is separated out because it is not an error in the
// sense the others are: nothing is broken, no retry helps, and the user's next
// move is different — they opened something Inspect renders as bytes or not at
// all. Left as a raw message it arrives as the decoder's own
// `TypeError: The encoded data was not valid for encoding utf-8`, wrapped in an
// IPC frame, which reads as a crash. Say what the file is instead, and name the
// kinds that DO preview so the boundary is discoverable rather than guessed at.
function ReadError({ message }: { message: string }): JSX.Element {
  const t = useT();
  if (isNotTextError(message))
    return (
      <div className="surface-placeholder region-pad">
        <div className="surface-posture">{t('inspect.notText')}</div>
        <p className="small muted">{t('inspect.notTextHint')}</p>
      </div>
    );
  return (
    <div className="inspect-error region-pad">
      <Icon name="alert" size={16} /> {message}
    </div>
  );
}

// ── trace lens ───────────────────────────────────────────────────────────────
function TraceLens({ trace, onOpen }: { trace: ParsedTrace; onOpen: (f: TraceFrame) => void }): JSX.Element {
  const t = useT();
  const [showLib, setShowLib] = useState(false);
  const hasLib = trace.frames.some((f) => f.lib);
  const visible = showLib ? trace.frames : trace.frames.filter((f) => !f.lib);
  const shown = visible.length > 0 ? visible : trace.frames;
  return (
    <div className="inspect-trace">
      <div className="inspect-trace-head">
        <Icon name="alert" size={14} />
        <span className="small">
          {t('inspect.trace')} · {trace.kind}
        </span>
        <span className="spacer" />
        {hasLib && (
          <button className="link-btn small" onClick={() => setShowLib((s) => !s)}>
            {showLib ? t('inspect.hideLib') : t('inspect.showLib')}
          </button>
        )}
      </div>
      <ol className="inspect-frames">
        {shown.map((f, i) => (
          <li key={i} className={f.lib ? 'lib' : ''}>
            <button className="inspect-frame" onClick={() => onOpen(f)} title={`${f.file}:${f.line}`}>
              <span className="frame-file">{baseName(f.file)}</span>
              <span className="frame-line">:{f.line}</span>
              {f.func !== undefined && f.func !== '' && <span className="frame-func muted">{f.func}</span>}
            </button>
          </li>
        ))}
      </ol>
    </div>
  );
}

function RunOutput({ res }: { res: ScriptResult }): JSX.Element {
  const t = useT();
  return (
    <div className="inspect-runout">
      <div className="inspect-runout-head small muted">
        {t('inspect.exit')}: {res.code ?? '—'}
        {res.timedOut ? ` · ${t('inspect.timedOut')}` : ''}
      </div>
      {res.stdout !== '' && <pre className="inspect-out mono">{res.stdout}</pre>}
      {res.stderr !== '' && <pre className="inspect-out mono err">{res.stderr}</pre>}
    </div>
  );
}

// ── code tab ─────────────────────────────────────────────────────────────────
function CodeTab({
  tab,
  reveal,
  onRevealed,
  onOpenFrame,
}: {
  tab: InspectTab;
  reveal: number | undefined;
  onRevealed: () => void;
  onOpenFrame: (f: TraceFrame, from: InspectTab) => void;
}): JSX.Element {
  const t = useT();
  const content = useInspect((s) => s.content[tab.id]);
  const loading = useInspect((s) => s.loading[tab.id]);
  const error = useInspect((s) => s.error[tab.id]);
  const setContent = useInspect((s) => s.setContent);
  const setLoading = useInspect((s) => s.setLoading);
  const setError = useInspect((s) => s.setError);
  const setLang = useInspect((s) => s.setLang);
  const setKind = useInspect((s) => s.setKind);
  const setSelection = useInspect((s) => s.setSelection);
  const openTab = useInspect((s) => s.open);
  const folder = useWorkspace((s) => s.folder);
  const codeRef = useRef<CodeViewHandle>(null);
  const [runOut, setRunOut] = useState<ScriptResult | null>(null);
  const [running, setRunning] = useState(false);
  const [traceOpen, setTraceOpen] = useState(false);
  const [cgOpen, setCgOpen] = useState(false);
  const [symbols, setSymbols] = useState<CodeSymbol[]>([]);

  // Lazily read a file-backed tab's content the first time it is shown.
  useEffect(() => {
    if (tab.source === 'paste' || content !== undefined || loading === true) return;
    let cancelled = false;
    setLoading(tab.id, true);
    setError(tab.id, undefined);
    void (async () => {
      try {
        const body = await readSource(tab);
        if (!cancelled) setContent(tab.id, body);
      } catch (e) {
        if (!cancelled) setError(tab.id, e instanceof Error ? e.message : String(e));
      } finally {
        if (!cancelled) setLoading(tab.id, false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  // A trace chip opened THIS tab at a line — reveal it once content is present.
  useEffect(() => {
    if (reveal === undefined || content === undefined) return;
    const id = window.setTimeout(() => {
      codeRef.current?.revealLine(reveal);
      onRevealed();
    }, 30);
    return () => window.clearTimeout(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reveal, content]);

  const body = content ?? '';
  const langId = tab.lang ?? langFromPath(tab.path);
  const interp = langId !== undefined ? (RUN_INTERP[langId] ?? null) : null;
  const trace = useMemo<ParsedTrace | null>(() => {
    const fromErr = runOut?.stderr !== undefined && runOut.stderr !== '' ? parseTrace(runOut.stderr) : null;
    return fromErr ?? parseTrace(body);
  }, [body, runOut]);

  // Extract the tree-sitter symbol outline for supported languages, debounced so
  // a paste tab isn't re-parsed on every keystroke. Unsupported language → no
  // parse (the WASM grammar never loads) and the rail hides.
  useEffect(() => {
    if (langId === undefined || !SUPPORTED_LANGS.has(langId)) {
      setSymbols([]);
      return;
    }
    let cancelled = false;
    const id = window.setTimeout(() => {
      void extractSymbols(langId, body).then((s) => !cancelled && setSymbols(s));
    }, 350);
    return () => {
      cancelled = true;
      window.clearTimeout(id);
    };
  }, [body, langId]);

  async function run(): Promise<void> {
    if (interp === null) return;
    setRunning(true);
    try {
      const cwd = tab.path !== undefined ? dirName(tab.path) : (folder ?? undefined);
      setRunOut(await runScript(interp, body, cwd));
    } catch (e) {
      setRunOut({ code: null, stdout: '', stderr: e instanceof Error ? e.message : String(e), timedOut: false });
    } finally {
      setRunning(false);
    }
  }

  if (loading === true) return <div className="muted region-pad">{t('inspect.loading')}</div>;
  if (error !== undefined)
    return (
      <ReadError message={error} />
    );

  const isPatch = looksLikePatch(body);
  const isDot = !isPatch && looksLikeDot(body);
  const isLog = !isPatch && !isDot && looksLikeLog(body);
  // A Python tab can be traced into a model graph (needs a local/SSH venue).
  const isPython = isShell() && (langId === 'python' || (tab.path?.toLowerCase().endsWith('.py') ?? false));
  // A py/js/ruby/php source can be turned into a static call graph via code2flow.
  const isCallable = isShell() && ((langId !== undefined && CALLGRAPH_LANGS.has(langId)) || CALLGRAPH_EXTS.has(extOf(tab.path ?? '').toLowerCase()));
  // A file-backed Python module can be read into a class-composition graph (W4b).
  const isModeling = isPython && tab.path !== undefined;
  // §5a — a transformers config.json (any source) can flip to the architecture card.
  const isHfConfig = !isPatch && !isDot && !isLog && parseHfConfig(body) !== null;
  // A LeRobot policy config (VLA/pi0/smolvla/act/…) flips to the policy card.
  const isPolicyConfig = !isPatch && !isDot && !isLog && !isHfConfig && parsePolicyConfig(body) !== null;
  const showRunBar =
    tab.source === 'paste' || interp !== null || isPatch || isLog || isDot || isPython || isCallable || isHfConfig || isPolicyConfig;
  return (
    <div className="inspect-tabbody">
      {showRunBar && (
        <div className="inspect-runbar">
          {tab.source === 'paste' && (
            <select
              className="surface-select"
              value={langId ?? 'text'}
              onChange={(e) => setLang(tab.id, e.target.value === 'text' ? undefined : e.target.value)}
            >
              {LANGS.map((l) => (
                <option key={l} value={l}>
                  {l}
                </option>
              ))}
            </select>
          )}
          <span className="spacer" />
          {isPatch && (
            <button className="import-btn" onClick={() => setKind(tab.id, 'diff')}>
              <Icon name="git-compare" size={14} /> {t('inspect.viewAsDiff')}
            </button>
          )}
          {isLog && (
            <button className="import-btn" onClick={() => setKind(tab.id, 'log')}>
              <Icon name="list-ordered" size={14} /> {t('inspect.viewAsLog')}
            </button>
          )}
          {isDot && (
            <button className="import-btn" onClick={() => setKind(tab.id, 'graph')}>
              <Icon name="diagram" size={14} /> {t('inspect.viewAsGraph')}
            </button>
          )}
          {(isHfConfig || isPolicyConfig) && (
            <button className="import-btn" onClick={() => setKind(tab.id, 'model')}>
              <Icon name="sliders" size={14} /> {t('model.analyze')}
            </button>
          )}
          {/* Supplied-screen mode (P4): run the pane-state manifests over a
              screen someone pasted out of a bug report. Offered on any paste
              tab because a captured screen has no extension to sniff. */}
          {tab.source === 'paste' && body.trim() !== '' && (
            <button className="import-btn" onClick={() => setKind(tab.id, 'panestate')}>
              <Icon name="terminal" size={14} /> {t('panestate.viewAsPaneState')}
            </button>
          )}
          {isPython && (
            <button className="import-btn" onClick={() => setTraceOpen(true)}>
              <Icon name="diagram" size={14} /> {t('trace.action')}
            </button>
          )}
          {isCallable && (
            <button className="import-btn" onClick={() => setCgOpen(true)}>
              <Icon name="git-branch" size={14} /> {t('callgraph.action')}
            </button>
          )}
          {isModeling && (
            <button
              className="import-btn"
              onClick={() =>
                openTab({ kind: 'modgraph', source: tab.source, title: `modules: ${tab.path !== undefined ? baseName(tab.path) : ''}`, path: tab.path, hostId: tab.hostId, projectId: tab.projectId })
              }
            >
              <Icon name="sitemap" size={14} /> {t('modgraph.action')}
            </button>
          )}
          {interp !== null && (
            <button className="import-btn" disabled={running} onClick={() => void run()}>
              <Icon name="play" size={14} /> {running ? t('inspect.running') : t('inspect.run')}
            </button>
          )}
        </div>
      )}
      {traceOpen && (
        <TraceModal
          tab={tab}
          onClose={() => setTraceOpen(false)}
          onGraph={(dot, title) => {
            openTab({ kind: 'graph', source: 'paste', title }, dot);
            setTraceOpen(false);
          }}
          onModelGraph={(gc, title) => {
            openTab({ kind: 'megraph', source: 'paste', title }, JSON.stringify(gc));
            setTraceOpen(false);
          }}
        />
      )}
      {cgOpen && (
        <CallGraphModal
          tab={tab}
          onClose={() => setCgOpen(false)}
          onGraph={(dot, title) => {
            openTab({ kind: 'graph', source: 'paste', title }, dot);
            setCgOpen(false);
          }}
        />
      )}
      <div className="inspect-codewrap">
        <div className="inspect-code">
          <Suspense fallback={<div className="muted region-pad">{t('inspect.loading')}</div>}>
            <CodeView
              ref={codeRef}
              value={body}
              onChange={(v) => setContent(tab.id, v)}
              // G2: the selected lines are what makes `inspect.selection`
              // truthful — an agent asked about "this bit" needs to know which
              // bit. The store dedupes; the focus sender throttles.
              onSelection={(sel) => setSelection(tab.id, sel)}
              filename={tab.path !== undefined ? baseName(tab.path) : undefined}
              lang={langId}
              editable={tab.source === 'paste'}
            />
          </Suspense>
        </div>
        <CodeOutline symbols={symbols} onJump={(line) => codeRef.current?.revealLine(line)} />
      </div>
      {trace !== null && <TraceLens trace={trace} onOpen={(f) => onOpenFrame(f, tab)} />}
      {runOut !== null && <RunOutput res={runOut} />}
    </div>
  );
}

// ── rendered text tabs (Markdown + CSV/TSV) ──────────────────────────────────
// Both use the same source-loading contract as CodeTab, but default to a useful
// semantic preview. Source is always one click away: Inspect must never hide or
// reinterpret the bytes without giving the user the literal representation.
function RichTextTab({ tab }: { tab: InspectTab }): JSX.Element {
  const t = useT();
  const content = useInspect((s) => s.content[tab.id]);
  const loading = useInspect((s) => s.loading[tab.id]);
  const error = useInspect((s) => s.error[tab.id]);
  const setContent = useInspect((s) => s.setContent);
  const setLoading = useInspect((s) => s.setLoading);
  const setError = useInspect((s) => s.setError);
  const setSelection = useInspect((s) => s.setSelection);
  const [mode, setMode] = useState<'preview' | 'source'>('preview');
  const [softWrap, setSoftWrap] = useState(false);

  useEffect(() => {
    if (tab.source === 'paste' || content !== undefined || loading === true) return;
    let cancelled = false;
    setLoading(tab.id, true);
    setError(tab.id, undefined);
    void (async () => {
      try {
        const body = await readSource(tab);
        if (!cancelled) setContent(tab.id, body);
      } catch (e) {
        if (!cancelled) setError(tab.id, e instanceof Error ? e.message : String(e));
      } finally {
        if (!cancelled) setLoading(tab.id, false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  useEffect(() => () => setSelection(tab.id, null), [setSelection, tab.id]);

  if (loading === true) return <div className="muted region-pad">{t('inspect.loading')}</div>;
  if (error !== undefined) return <ReadError message={error} />;

  const body = content ?? '';
  const filename = tab.path !== undefined ? baseName(tab.path) : tab.title;
  const delimiter = extOf(filename).toLowerCase() === 'tsv' ? '\t' : ',';
  return (
    <div className="inspect-tabbody">
      <div className="inspect-runbar inspect-previewbar">
        <span className="muted small">{tab.kind === 'markdown' ? t('inspect.markdownPreview') : t('inspect.tablePreview')}</span>
        <span className="spacer" />
        {mode === 'preview' && (
          <button
            type="button"
            className={`icon-btn${softWrap ? ' active' : ''}`}
            title={t('inspect.wrap')}
            aria-label={t('inspect.wrap')}
            aria-pressed={softWrap}
            onClick={() => setSoftWrap((current) => !current)}
          >
            <Icon name="wrap" size={15} />
          </button>
        )}
        <div className="patch-modes" role="group" aria-label={t('inspect.previewMode')}>
          <button
            type="button"
            className={`seg-btn${mode === 'preview' ? ' active' : ''}`}
            onClick={() => {
              setSelection(tab.id, null);
              setMode('preview');
            }}
          >
            {t('inspect.preview')}
          </button>
          <button
            type="button"
            className={`seg-btn${mode === 'source' ? ' active' : ''}`}
            onClick={() => setMode('source')}
          >
            {t('inspect.source')}
          </button>
        </div>
      </div>
      <div className="inspect-rich-preview">
        <Suspense fallback={<div className="muted region-pad">{t('inspect.loading')}</div>}>
          {mode === 'source' ? (
            <CodeView value={body} filename={filename} onSelection={(selection) => setSelection(tab.id, selection)} />
          ) : tab.kind === 'markdown' ? (
            <MarkdownReader text={body} softWrap={softWrap} />
          ) : (
            <DelimitedPreview text={body} delimiter={delimiter} softWrap={softWrap} />
          )}
        </Suspense>
      </div>
    </div>
  );
}

// ── diff tab (W2) ─────────────────────────────────────────────────────────────
// Two shapes share the `diff` kind: a **patch** tab (a `.patch`/`.diff` file or
// pasted patch → GitHub-style multi-file render) and a **compare** tab (two
// sources → editor-grade side-by-side merge). `tab.left`/`tab.right` select the
// second shape.
function DiffTab({ tab }: { tab: InspectTab }): JSX.Element {
  const t = useT();
  const content = useInspect((s) => s.content[tab.id]);
  const loading = useInspect((s) => s.loading[tab.id]);
  const error = useInspect((s) => s.error[tab.id]);
  const setContent = useInspect((s) => s.setContent);
  const setLoading = useInspect((s) => s.setLoading);
  const setError = useInspect((s) => s.setError);
  const setKind = useInspect((s) => s.setKind);
  const isCompare = tab.left !== undefined && tab.right !== undefined;
  const [sides, setSides] = useState<{ a: string; b: string } | null>(null);

  // Compare tab: read both sides once.
  useEffect(() => {
    if (!isCompare) return;
    let cancelled = false;
    setLoading(tab.id, true);
    setError(tab.id, undefined);
    void (async () => {
      try {
        const [a, b] = await Promise.all([readRef(tab.left!, `insp-${tab.id}-a`), readRef(tab.right!, `insp-${tab.id}-b`)]);
        if (!cancelled) setSides({ a, b });
      } catch (e) {
        if (!cancelled) setError(tab.id, e instanceof Error ? e.message : String(e));
      } finally {
        if (!cancelled) setLoading(tab.id, false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  // Patch tab (file-backed): lazily read its content once (paste patches keep
  // their body authoritative in the store, mirroring the code tab).
  useEffect(() => {
    if (isCompare || tab.source === 'paste' || content !== undefined || loading === true) return;
    let cancelled = false;
    setLoading(tab.id, true);
    setError(tab.id, undefined);
    void (async () => {
      try {
        const body = await readSource(tab);
        if (!cancelled) setContent(tab.id, body);
      } catch (e) {
        if (!cancelled) setError(tab.id, e instanceof Error ? e.message : String(e));
      } finally {
        if (!cancelled) setLoading(tab.id, false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  if (loading === true) return <div className="muted region-pad">{t('inspect.loading')}</div>;
  if (error !== undefined)
    return (
      <ReadError message={error} />
    );

  if (isCompare) {
    if (sides === null) return <div className="muted region-pad">{t('inspect.loading')}</div>;
    const fname = tab.left?.path !== undefined ? baseName(tab.left.path) : tab.right?.path !== undefined ? baseName(tab.right.path) : undefined;
    const lang = tab.lang ?? tab.left?.lang ?? tab.right?.lang;
    return (
      <Suspense fallback={<div className="muted region-pad">{t('inspect.loading')}</div>}>
        <TwoBlobCompare a={sides.a} b={sides.b} aTitle={tab.left?.title} bTitle={tab.right?.title} filename={fname} lang={lang} />
      </Suspense>
    );
  }
  return (
    <Suspense fallback={<div className="muted region-pad">{t('inspect.loading')}</div>}>
      <PatchDiffView patch={content ?? ''} onViewSource={tab.source === 'paste' ? () => setKind(tab.id, 'code') : undefined} />
    </Suspense>
  );
}

// ── log tab (W3) ──────────────────────────────────────────────────────────────
// A **local** file is read through the main-process line index (never slurped
// into the store — the plan's IPC discipline); every other source (paste /
// workspace / remote / hub) renders from an in-memory string via `readSource`,
// mirroring the code tab's lazy read.
function LogTab({ tab }: { tab: InspectTab }): JSX.Element {
  const t = useT();
  const content = useInspect((s) => s.content[tab.id]);
  const loading = useInspect((s) => s.loading[tab.id]);
  const error = useInspect((s) => s.error[tab.id]);
  const setContent = useInspect((s) => s.setContent);
  const setLoading = useInspect((s) => s.setLoading);
  const setError = useInspect((s) => s.setError);
  const setKind = useInspect((s) => s.setKind);
  const indexMode = isShell() && tab.source === 'local' && tab.path !== undefined;

  useEffect(() => {
    if (indexMode || tab.source === 'paste' || content !== undefined || loading === true) return;
    let cancelled = false;
    setLoading(tab.id, true);
    setError(tab.id, undefined);
    void (async () => {
      try {
        const body = await readSource(tab);
        if (!cancelled) setContent(tab.id, body);
      } catch (e) {
        if (!cancelled) setError(tab.id, e instanceof Error ? e.message : String(e));
      } finally {
        if (!cancelled) setLoading(tab.id, false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  if (error !== undefined)
    return (
      <ReadError message={error} />
    );
  if (!indexMode && content === undefined && tab.source !== 'paste') return <div className="muted region-pad">{t('inspect.loading')}</div>;

  const source: LogSource = indexMode ? { kind: 'index', path: tab.path! } : { kind: 'memory', text: content ?? '' };
  return (
    <div className="inspect-tabbody">
      {tab.source === 'paste' && (
        <div className="inspect-runbar">
          <span className="spacer" />
          <button className="import-btn" onClick={() => setKind(tab.id, 'code')}>
            <Icon name="code" size={14} /> {t('inspect.viewSource')}
          </button>
        </div>
      )}
      <div className="inspect-logwrap">
        <Suspense fallback={<div className="muted region-pad">{t('inspect.loading')}</div>}>
          <LogView source={source} />
        </Suspense>
      </div>
    </div>
  );
}

// ── media tab (image / video / audio / pdf) ───────────────────────────────────
// The bytes are STREAMED, never read into the store. A media file is routinely
// orders of magnitude larger than any text Inspect holds — the store persists
// to localStorage (~5 MB whole-app quota) and a slurped video would blow it —
// and `<video>` needs range requests to seek at all, which only the main
// process's media scheme provides. So this tab holds a URL, not a body.
//
// Sources split on whether their bytes can be streamed. `local`/`workspace` are
// absolute paths on this machine; `remote` rides the same live SFTP session the
// text reads use, resolved here because the scheme addresses an ssh session id
// rather than a saved-connection id. `hub`/`github`/`hf` reach their bytes
// through HTTP transports that decode to text before the renderer sees them, so
// they say that plainly instead of mounting a player that will never load.
function MediaTab({ tab }: { tab: InspectTab }): JSX.Element {
  const t = useT();
  const [url, setUrl] = useState<string | null>(null);
  const [pdfBuf, setPdfBuf] = useState<ArrayBuffer | null>(null);
  const [failed, setFailed] = useState<string | null>(null);
  const streamable = isShell() && canStreamMedia(tab.source);

  // PDF is the one kind that cannot stream. Chromium's built-in viewer is off
  // (`plugins` is false on this window, deliberately — it is a plugin surface
  // we do not otherwise want), so an <iframe> would render nothing; pdf.js
  // parses a whole document, so the bytes have to be in hand. Read reaches for
  // the same viewer for the same reason, which is also what makes the two
  // surfaces agree on what a PDF looks like.
  useEffect(() => {
    if (!streamable || tab.kind !== 'pdf') return;
    let cancelled = false;
    setPdfBuf(null);
    setFailed(null);
    void readSourceBytes(tab, PDF_PREVIEW_CAP)
      .then((bytes) => {
        if (cancelled) return;
        // Copy into a standalone ArrayBuffer: the IPC buffer may be a view over
        // a larger pooled allocation, and pdf.js takes ownership of what it is
        // given.
        setPdfBuf(bytes.slice().buffer);
      })
      .catch((e: unknown) => {
        if (!cancelled) setFailed(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  useEffect(() => {
    if (!streamable || tab.kind === 'pdf') return;
    let cancelled = false;
    setUrl(null);
    setFailed(null);
    if (tab.source !== 'remote') {
      setUrl(mediaFileUrl(tab.path));
      return;
    }
    // A remote tab names a saved connection; the scheme wants the ephemeral ssh
    // session id. Reuse the cached session so opening a preview does not add a
    // second connection alongside the one the tree is already browsing on.
    void sftpSessionFor(tab.hostId ?? '')
      .then((sid) => {
        if (!cancelled) setUrl(mediaSftpUrl(sid, tab.path ?? ''));
      })
      .catch((e: unknown) => {
        if (!cancelled) setFailed(e instanceof Error ? e.message : t('inspect.mediaFailed'));
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  if (!isShell())
    return (
      <div className="surface-placeholder region-pad">
        <div className="surface-posture">{t('inspect.mediaDesktopOnly')}</div>
      </div>
    );
  if (!streamable)
    return (
      <div className="surface-placeholder region-pad">
        <div className="surface-posture">{t('inspect.mediaSourceUnsupported')}</div>
      </div>
    );
  if (failed !== null)
    return (
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {failed}
      </div>
    );

  if (tab.kind === 'pdf') {
    if (pdfBuf === null) return <div className="muted region-pad">{t('inspect.loading')}</div>;
    return (
      <Suspense fallback={<div className="muted region-pad">{t('inspect.loading')}</div>}>
        <PdfCanvas data={pdfBuf} fileName={tab.title} />
      </Suspense>
    );
  }
  if (url === null) return <div className="muted region-pad">{t('inspect.loading')}</div>;

  // `key={url}` so a re-resolved remote session swaps the element rather than
  // leaving a player pinned to a session that has since closed.
  return (
    <div className="inspect-media">
      {tab.kind === 'image' ? (
        <img className="inspect-media-el" src={url} alt={tab.title} onError={() => setFailed(t('inspect.mediaFailed'))} />
      ) : tab.kind === 'video' ? (
        <video key={url} className="inspect-media-el" src={url} controls onError={() => setFailed(t('inspect.mediaFailed'))} />
      ) : (
        <audio key={url} className="inspect-media-audio" src={url} controls onError={() => setFailed(t('inspect.mediaFailed'))} />
      )}
    </div>
  );
}

// ── model tab (W4) ────────────────────────────────────────────────────────────
// A checkpoint (`.safetensors`/`.gguf`) is parsed **header-only in the main
// process** (`checkpoint_inspect` by path — the bytes never leave disk, plan §5).
// W4 core reads a **local** file (the native picker); remote/hub checkpoints are a
// follow-on (they'd need an SFTP header-fetch), so those show an honest note.
function ModelTab({ tab }: { tab: InspectTab }): JSX.Element {
  const t = useT();
  const content = useInspect((s) => s.content[tab.id]);
  // §5a — a model tab flipped from a JSON config code tab (any source) renders
  // the config-only architecture view from the text already in the store; no
  // `checkpoint_inspect`, no local path required.
  const cfg = useMemo(() => parseHfConfig(content), [content]);
  // A LeRobot policy config (no model_type/architectures) — a different schema.
  const policy = useMemo(() => parsePolicyConfig(content), [content]);
  if (cfg !== null) {
    return (
      <Suspense fallback={<div className="muted region-pad">{t('inspect.loading')}</div>}>
        <ConfigArchView tab={tab} config={cfg} />
      </Suspense>
    );
  }
  if (policy !== null) {
    return (
      <Suspense fallback={<div className="muted region-pad">{t('inspect.loading')}</div>}>
        <PolicyConfigView tab={tab} card={policy} />
      </Suspense>
    );
  }
  // `checkpoint_inspect` is by local absolute path — a `workspace` tab has one
  // too (the walk returns absolute paths), so it inspects just like `local`. The
  // gate reads "the path is local", not "picked via the native dialog".
  if (!isShell() || (tab.source !== 'local' && tab.source !== 'workspace') || tab.path === undefined) {
    return (
      <div className="surface-placeholder region-pad">
        <div className="surface-posture">{t('model.localOnly')}</div>
      </div>
    );
  }
  return (
    <Suspense fallback={<div className="muted region-pad">{t('inspect.loading')}</div>}>
      <ModelView path={tab.path} />
    </Suspense>
  );
}

// ── graph tab (Graphviz DOT) ──────────────────────────────────────────────────
// Renders a `.dot`/`.gv` file (or a pasted `digraph {…}` scratch that sniffs as
// DOT) as a pan/zoomable SVG via the wasm-graphviz engine (plan §5). This is the
// render path the code2flow call-graph and torchview tracer will emit into; those
// producers (which need a Python venue) are later slices.
function GraphTab({ tab }: { tab: InspectTab }): JSX.Element {
  const t = useT();
  const content = useInspect((s) => s.content[tab.id]);
  const loading = useInspect((s) => s.loading[tab.id]);
  const error = useInspect((s) => s.error[tab.id]);
  const setContent = useInspect((s) => s.setContent);
  const setLoading = useInspect((s) => s.setLoading);
  const setError = useInspect((s) => s.setError);
  const setKind = useInspect((s) => s.setKind);

  useEffect(() => {
    if (tab.source === 'paste' || content !== undefined || loading === true) return;
    let cancelled = false;
    setLoading(tab.id, true);
    setError(tab.id, undefined);
    void (async () => {
      try {
        const body = await readSource(tab);
        if (!cancelled) setContent(tab.id, body);
      } catch (e) {
        if (!cancelled) setError(tab.id, e instanceof Error ? e.message : String(e));
      } finally {
        if (!cancelled) setLoading(tab.id, false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  if (error !== undefined)
    return (
      <ReadError message={error} />
    );
  if (content === undefined && tab.source !== 'paste') return <div className="muted region-pad">{t('inspect.loading')}</div>;

  return (
    <div className="inspect-tabbody">
      {tab.source === 'paste' && (
        <div className="inspect-runbar">
          <span className="spacer" />
          <button className="import-btn" onClick={() => setKind(tab.id, 'code')}>
            <Icon name="code" size={14} /> {t('inspect.viewSource')}
          </button>
        </div>
      )}
      <div className="inspect-graphwrap">
        <Suspense fallback={<div className="muted region-pad">{t('graph.rendering')}</div>}>
          <DotGraphView dot={content ?? ''} />
        </Suspense>
      </div>
    </div>
  );
}

// ── interactive model-explorer graph tab (Model Explorer WebGL) ───────────────
// A `megraph` tab renders the interactive Model Explorer graph from either a `local`
// checkpoint (re-inspected header-only, like a model tab) or a `paste` body carrying
// a pre-built GraphCollection (the tracer Tier-2 torch.export traced graph).
function MEGraphTab({ tab }: { tab: InspectTab }): JSX.Element {
  const t = useT();
  const content = useInspect((s) => s.content[tab.id]);
  if (tab.source === 'paste') {
    let gc: GraphCollection | null = null;
    try {
      gc = content !== undefined && content !== '' ? (JSON.parse(content) as GraphCollection) : null;
    } catch {
      gc = null;
    }
    if (gc === null)
      return (
        <div className="inspect-error region-pad">
          <Icon name="alert" size={16} /> {t('graph.rendering')}
        </div>
      );
    return (
      <Suspense fallback={<div className="muted region-pad">{t('graph.rendering')}</div>}>
        <ModelExplorerView collection={gc} />
      </Suspense>
    );
  }
  if (!isShell() || (tab.source !== 'local' && tab.source !== 'workspace') || tab.path === undefined) {
    return (
      <div className="surface-placeholder region-pad">
        <div className="surface-posture">{t('model.localOnly')}</div>
      </div>
    );
  }
  return (
    <Suspense fallback={<div className="muted region-pad">{t('graph.rendering')}</div>}>
      <ModelExplorerView path={tab.path} />
    </Suspense>
  );
}

// ── config-only architecture schematic tab (§5a follow-on) ────────────────────
// An `archgraph` tab carries a parsed HF `config.json` (paste body) and renders
// the paper-style block diagram synthesized from it — no weights, no compute
// graph (a config can't yield either), just the canonical transformer stack the
// classifier already describes. Available from every source the config-only view
// is (local/workspace/remote/hub/github/hf), since it only needs the config text.
function ArchGraphTab({ tab }: { tab: InspectTab }): JSX.Element {
  const t = useT();
  const content = useInspect((s) => s.content[tab.id]);
  const built = useMemo(() => {
    const config = parseHfConfig(content);
    if (config === null) return null;
    const card = classifyArch({ config, tensorNames: [] });
    const schematic = card !== null ? buildArchSchematic(card, config) : null;
    return schematic !== null ? { schematic, config, card } : null;
  }, [content]);
  if (built === null)
    return (
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {t('archgraph.cannotDerive')}
      </div>
    );
  return (
    <Suspense fallback={<div className="muted region-pad">{t('graph.rendering')}</div>}>
      <ArchSchematicView schematic={built.schematic} config={built.config} card={built.card} />
    </Suspense>
  );
}

// ── interactive module (class-composition) graph tab (W4b) ────────────────────
// A `modgraph` tab reads a file-backed Python module's class hierarchy on its venue
// (stdlib `ast`) and renders the React-Flow/elkjs class graph. `onReveal` scrolls the
// modeling file's code tab to a clicked class (the code-sync).
function ModGraphTab({ tab, onReveal }: { tab: InspectTab; onReveal: (lineno: number) => void }): JSX.Element {
  const t = useT();
  if (!isShell() || tab.path === undefined) {
    return (
      <div className="surface-placeholder region-pad">
        <div className="surface-posture">{t('modgraph.needsFile')}</div>
      </div>
    );
  }
  return (
    <Suspense fallback={<div className="muted region-pad">{t('modgraph.reading')}</div>}>
      <ModuleGraphView tab={tab} onReveal={onReveal} />
    </Suspense>
  );
}

export function DebugSurface(): JSX.Element {
  const t = useT();
  const tabs = useInspect((s) => s.tabs);
  const activeId = useInspect((s) => s.activeId);
  const activeHasContent = useInspect((s) => s.activeId !== null && s.content[s.activeId] !== undefined);
  const openTab = useInspect((s) => s.open);
  const closeTab = useInspect((s) => s.close);
  const setActive = useInspect((s) => s.setActive);
  const setContent = useInspect((s) => s.setContent);
  const folder = useWorkspace((s) => s.folder);
  const client = useSession((s) => s.client);
  const roots = useInspectRoots((s) => s.roots);
  const addRoot = useInspectRoots((s) => s.addRoot);
  const [treeW, onResizeTree] = usePanelWidth('termipod.inspect.treeW', 260, 200, 420);
  const [treeOpen, setTreeOpen] = useState<boolean>(() => localStorage.getItem('termipod.inspect.treeOpen') !== '0');
  const [reveal, setReveal] = useState<Record<string, number>>({});
  const [notFound, setNotFound] = useState<string | null>(null);
  const [menu, setMenu] = useState(false);
  const [cmpMenu, setCmpMenu] = useState(false);
  const openMenuAnchorRef = useRef<HTMLDivElement>(null);
  const compareMenuAnchorRef = useRef<HTMLDivElement>(null);
  const [dialog, setDialog] = useState<OpenMode | null>(null);
  const [repoDialog, setRepoDialog] = useState(false);
  // The Compare menu's repo picker (#460) — resolve + browse a GitHub/HF repo
  // for side B WITHOUT pinning it as a tree root (that's `repoDialog`).
  const [repoPick, setRepoPick] = useState(false);
  const [panePick, setPanePick] = useState(false);
  // When set, the next file/tab the user picks becomes side B of a compare tab
  // whose side A is this base tab (W2 tier 2).
  const [cmpBase, setCmpBase] = useState<InspectTab | null>(null);
  const active = tabs.find((tb) => tb.id === activeId);

  // File-backed Inspect tabs used to keep their first read forever. Re-read the
  // active local text source in the background and swap the cached body only
  // when bytes actually changed. The interval pauses while the window is hidden
  // and an immediate check runs when the user returns from another app/surface.
  useEffect(() => {
    if (
      active === undefined ||
      (active.source !== 'local' && active.source !== 'workspace') ||
      active.path === undefined ||
      !activeHasContent
    ) {
      return;
    }
    const watched = active;
    let stopped = false;
    let running = false;
    async function refresh(): Promise<void> {
      if (running || document.visibilityState === 'hidden') return;
      running = true;
      try {
        const body = await readSource(watched);
        if (!stopped && body !== useInspect.getState().content[watched.id]) setContent(watched.id, body);
      } catch {
        // Preserve the last readable snapshot during an atomic-save rename or a
        // temporarily unavailable mount. A later poll retries.
      } finally {
        running = false;
      }
    }
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2000);
    const onVisible = (): void => {
      if (document.visibilityState === 'visible') void refresh();
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      stopped = true;
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [active?.id, active?.path, active?.source, activeHasContent, setContent]);
  // A tab is comparable if we can read its content: any tab except an existing
  // compare tab (comparing a compare makes no sense).
  const canCompare = active !== undefined && !(active.left !== undefined && active.right !== undefined);
  const otherTabs = tabs.filter((tb) => tb.id !== active?.id && !(tb.left !== undefined && tb.right !== undefined));

  function newScratch(): void {
    openTab({ kind: 'code', source: 'paste', title: t('inspect.scratch') }, '');
  }

  // Live mode: explain the pane of a running agent (pane-state-manifests P4).
  // `ephemeral` because the record is a snapshot of somebody's terminal — it
  // is re-fetched on open and never written to localStorage.
  function openPaneState(p: { agentId: string; title: string }): void {
    openTab({
      kind: 'panestate',
      source: 'paste',
      title: t('panestate.tabTitle').replace('{agent}', p.title),
      agentId: p.agentId,
      ephemeral: true,
    });
    setPanePick(false);
  }

  function setTree(open: boolean): void {
    setTreeOpen(open);
    try {
      localStorage.setItem('termipod.inspect.treeOpen', open ? '1' : '0');
    } catch {
      /* ignore */
    }
  }

  // Pin a local folder as a tree root (Open menu "Open folder…" + the pane's +).
  async function openFolder(): Promise<void> {
    if (!isShell()) return;
    const p = await invoke<string | null>('workspace_pick_folder', {});
    if (p === null || p === '') return;
    addRoot({ source: 'local', label: baseName(p), path: p });
    setTree(true);
  }

  // Pin the Author workspace folder as a root (a one-click suggestion, not
  // auto-pinned — plan §8 open question 1).
  function pinWorkspaceRoot(): void {
    if (folder === null || folder === '') return;
    addRoot({ source: 'local', label: baseName(folder), path: folder });
    setTree(true);
  }

  // Pin a remote directory / hub project browsed in the open dialog (T2).
  function pinRoot(r: PinRoot): void {
    addRoot(r);
    setTree(true);
    setDialog(null);
    setCmpBase(null);
  }

  // Snapshot a tab as a compare side. A paste/scratch side carries its current
  // body inline (nothing to re-read); a file-backed side re-reads on activate.
  function refOfTab(tb: InspectTab): InspectRef {
    return {
      source: tb.source,
      title: tb.title,
      path: tb.path,
      hostId: tb.hostId,
      projectId: tb.projectId,
      repo: tb.repo,
      lang: tb.lang ?? langFromPath(tb.path),
      body: tb.source === 'paste' ? (useInspect.getState().content[tb.id] ?? '') : undefined,
    };
  }

  function makeCompare(right: InspectRef): void {
    if (cmpBase === null) return;
    const left = refOfTab(cmpBase);
    openTab({
      kind: 'diff',
      source: 'paste', // nominal — a compare tab reads its two refs, not its own source
      title: `${left.title} ↔ ${right.title}`,
      lang: left.lang ?? right.lang,
      left,
      right,
    });
    setCmpBase(null);
    setCmpMenu(false);
    setDialog(null);
  }

  function beginCompare(): void {
    if (active === undefined || !canCompare) return;
    setCmpBase(active);
    setMenu(false);
    setCmpMenu(true);
  }

  async function openLocal(): Promise<void> {
    if (!isShell()) return;
    const r = await invoke<{ path: string; content: string } | null>('debug_open', {});
    if (r === null) return;
    if (cmpBase !== null) {
      makeCompare({ source: 'local', title: baseName(r.path), path: r.path, lang: langFromPath(r.path) });
      return;
    }
    const kind = kindForInspectFile(extOf(r.path), r.content);
    openTab({ kind, source: 'local', title: baseName(r.path), path: r.path }, kind === 'model' ? undefined : r.content);
  }

  // A picker (workspace / remote / hub) chose a file — either open it as a
  // metadata-only tab (read lazily on activate) or, in compare mode, make it
  // side B of a compare against the base tab.
  function pick(r: PickResult): void {
    if (cmpBase !== null) {
      makeCompare({ source: r.source, title: r.title, path: r.path, hostId: r.hostId, projectId: r.projectId, repo: r.repo, lang: langFromPath(r.path) });
      return;
    }
    const id = openTab({ kind: r.kind, source: r.source, title: r.title, path: r.path, hostId: r.hostId, projectId: r.projectId, repo: r.repo });
    if (r.revealLine !== undefined) setReveal((m) => ({ ...m, [id]: r.revealLine! }));
    setDialog(null);
  }

  // Code-sync for the module graph: open/focus the modeling file's code tab and
  // scroll it to a clicked class's line. Reuses the trace-lens reveal mechanism.
  function revealModeling(from: InspectTab, lineno: number): void {
    if (from.path === undefined) return;
    const id = openTab({ kind: 'code', source: from.source, title: baseName(from.path), path: from.path, hostId: from.hostId, projectId: from.projectId });
    setReveal((m) => ({ ...m, [id]: lineno }));
  }

  async function resolveFrame(frame: TraceFrame, from: InspectTab): Promise<void> {
    if (!isShell()) return;
    const cands: string[] = [];
    if (isAbs(frame.file)) cands.push(frame.file);
    if (folder !== null && folder !== '') cands.push(joinPath(folder, frame.file));
    // Every pinned local root — a traceback from an inspected checkout resolves
    // against it (plan §3 item 6), after the workspace folder, before the origin
    // tab's own directory.
    for (const r of roots) if (r.source === 'local' && r.path !== undefined && r.path !== '') cands.push(joinPath(r.path, frame.file));
    if (from.path !== undefined) cands.push(joinPath(dirName(from.path), frame.file));
    if (!isAbs(frame.file)) cands.push(frame.file);
    for (const c of cands) {
      try {
        const res = await invoke<{ path: string; content: string }>('doc_read', { path: c });
        const kind = kindForInspectFile(extOf(res.path), res.content);
        const id = openTab({ kind, source: 'local', title: baseName(res.path), path: res.path }, res.content);
        setReveal((m) => ({ ...m, [id]: frame.line }));
        return;
      } catch {
        /* try next candidate */
      }
    }
    setNotFound(frame.file);
    window.setTimeout(() => setNotFound(null), 2600);
  }

  return (
    <WorkbenchSurface
      job="debug"
      actions={
        <>
          {roots.length > 0 && (
            <button
              className={`import-btn${treeOpen ? ' active' : ''}`}
              title={treeOpen ? t('inspect.hideTree') : t('inspect.showTree')}
              aria-pressed={treeOpen}
              onClick={() => setTree(!treeOpen)}
            >
              <Icon name="sidebar" size={14} /> {t('inspect.tree')}
            </button>
          )}
          <button className="import-btn primary" onClick={newScratch}>
            <Icon name="plus" size={14} /> {t('inspect.newScratch')}
          </button>
          <div ref={openMenuAnchorRef} className="inspect-openwrap">
            <button className="import-btn" aria-haspopup="menu" aria-expanded={menu} onClick={() => setMenu((m) => !m)}>
              <Icon name="folder" size={14} /> {t('inspect.open')} <Icon name="chevron-down" size={12} />
            </button>
            <PopoverMenu
              anchorRef={openMenuAnchorRef}
              open={menu}
              onClose={() => setMenu(false)}
              className="inspect-menu"
              ariaLabel={t('inspect.open')}
            >
                  {isShell() && (
                    <button className="inspect-menu-item" role="menuitem" onClick={() => (setMenu(false), void openLocal())}>
                      <Icon name="file-text" size={14} /> {t('inspect.openFile')}
                    </button>
                  )}
                  {isShell() && (
                    <button className="inspect-menu-item" role="menuitem" onClick={() => (setMenu(false), void openFolder())}>
                      <Icon name="folder" size={14} /> {t('inspect.openFolder')}
                    </button>
                  )}
                  {isShell() && folder !== null && folder !== '' && (
                    <button className="inspect-menu-item" role="menuitem" onClick={() => (setMenu(false), pinWorkspaceRoot())}>
                      <Icon name="sidebar" size={14} /> {t('inspect.pinWorkspaceFolder')}
                    </button>
                  )}
                  {isShell() && (
                    <button className="inspect-menu-item" role="menuitem" onClick={() => (setMenu(false), setDialog('workspace'))}>
                      <Icon name="sidebar" size={14} /> {t('inspect.fromWorkspace')}
                    </button>
                  )}
                  {isShell() && (
                    <button className="inspect-menu-item" role="menuitem" onClick={() => (setMenu(false), setDialog('remote'))}>
                      <Icon name="terminal" size={14} /> {t('inspect.fromRemote')}
                    </button>
                  )}
                  {client !== null && (
                    <button className="inspect-menu-item" role="menuitem" onClick={() => (setMenu(false), setDialog('hub'))}>
                      <Icon name="cloud" size={14} /> {t('inspect.fromHub')}
                    </button>
                  )}
                  {client !== null && (
                    <button className="inspect-menu-item" role="menuitem" onClick={() => (setMenu(false), setPanePick(true))}>
                      <Icon name="terminal" size={14} /> {t('panestate.openLive')}
                    </button>
                  )}
                  <button className="inspect-menu-item" role="menuitem" onClick={() => (setMenu(false), setRepoDialog(true))}>
                    <Icon name="git-branch" size={14} /> {t('inspect.fromRepo')}
                  </button>
            </PopoverMenu>
          </div>
          {canCompare && (
            <div ref={compareMenuAnchorRef} className="inspect-openwrap">
              <button className="import-btn" aria-haspopup="menu" aria-expanded={cmpMenu} onClick={beginCompare}>
                <Icon name="git-compare" size={14} /> {t('inspect.compare')} <Icon name="chevron-down" size={12} />
              </button>
              <PopoverMenu
                anchorRef={compareMenuAnchorRef}
                open={cmpMenu}
                onClose={() => {
                  setCmpMenu(false);
                  setCmpBase(null);
                }}
                className="inspect-menu"
                ariaLabel={t('inspect.compare')}
              >
                    {otherTabs.length > 0 && <div className="inspect-menu-label small muted">{t('inspect.compareWithTab')}</div>}
                    {otherTabs.map((tb) => (
                      <button key={tb.id} className="inspect-menu-item" role="menuitem" onClick={() => makeCompare(refOfTab(tb))}>
                        <Icon name={kindIcon(tb.kind)} size={14} /> {tb.title}
                      </button>
                    ))}
                    <div className="inspect-menu-label small muted">{t('inspect.compareWithFile')}</div>
                    {isShell() && (
                      <button className="inspect-menu-item" role="menuitem" onClick={() => (setCmpMenu(false), void openLocal())}>
                        <Icon name="file-text" size={14} /> {t('inspect.openFile')}
                      </button>
                    )}
                    {isShell() && (
                      <button className="inspect-menu-item" role="menuitem" onClick={() => (setCmpMenu(false), setDialog('workspace'))}>
                        <Icon name="sidebar" size={14} /> {t('inspect.fromWorkspace')}
                      </button>
                    )}
                    {isShell() && (
                      <button className="inspect-menu-item" role="menuitem" onClick={() => (setCmpMenu(false), setDialog('remote'))}>
                        <Icon name="terminal" size={14} /> {t('inspect.fromRemote')}
                      </button>
                    )}
                    {client !== null && (
                      <button className="inspect-menu-item" role="menuitem" onClick={() => (setCmpMenu(false), setDialog('hub'))}>
                        <Icon name="cloud" size={14} /> {t('inspect.fromHub')}
                      </button>
                    )}
                    {roots.length > 0 && (
                      <button className="inspect-menu-item" role="menuitem" onClick={() => (setCmpMenu(false), setDialog('roots'))}>
                        <Icon name="sidebar" size={14} /> {t('inspect.fromRoots')}
                      </button>
                    )}
                    <button className="inspect-menu-item" role="menuitem" onClick={() => (setCmpMenu(false), setRepoPick(true))}>
                      <Icon name="git-branch" size={14} /> {t('inspect.fromRepo')}
                    </button>
              </PopoverMenu>
            </div>
          )}
        </>
      }
    >
      <div className="inspect-shell">
        {treeOpen && roots.length > 0 && (
          <>
            <InspectTree
              width={treeW}
              onPick={pick}
              onAddFolder={() => void openFolder()}
              onClose={() => setTree(false)}
              onOpenPatch={(title, patch) => openTab({ kind: 'diff', source: 'paste', title, ephemeral: true }, patch)}
            />
            <ResizeHandle onResize={onResizeTree} />
          </>
        )}
        <div className="inspect-main">
        {tabs.length > 0 && (
          <div className="inspect-tabs" role="tablist">
            {tabs.map((tb) => (
              <div
                key={tb.id}
                className={`inspect-tab${tb.id === activeId ? ' active' : ''}`}
                role="tab"
                aria-selected={tb.id === activeId}
                tabIndex={0}
                onClick={() => setActive(tb.id)}
                onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && setActive(tb.id)}
              >
                {tb.kind === 'code' ? <InspectFileIcon filename={tb.title} size={13} /> : <Icon name={kindIcon(tb.kind)} size={13} />}
                <span className="inspect-tab-title">{tb.title}</span>
                <button
                  className="inspect-tab-close"
                  title={t('inspect.close')}
                  onClick={(e) => {
                    e.stopPropagation();
                    closeTab(tb.id);
                  }}
                >
                  <Icon name="close" size={12} />
                </button>
              </div>
            ))}
          </div>
        )}
        <div className="inspect-active">
          {active === undefined ? (
            <div className="inspect-empty region-pad muted">
              <Icon name="code" size={24} />
              <p>{t('inspect.emptyTitle')}</p>
              <p className="small">{t('inspect.emptyHint')}</p>
            </div>
          ) : active.kind === 'code' ? (
            <CodeTab
              key={active.id}
              tab={active}
              reveal={reveal[active.id]}
              onRevealed={() =>
                setReveal((m) => {
                  const n = { ...m };
                  delete n[active.id];
                  return n;
                })
              }
              onOpenFrame={(f, from) => void resolveFrame(f, from)}
            />
          ) : active.kind === 'markdown' ? (
            <RichTextTab key={active.id} tab={active} />
          ) : active.kind === 'table' ? (
            <RichTextTab key={active.id} tab={active} />
          ) : active.kind === 'diff' ? (
            <DiffTab key={active.id} tab={active} />
          ) : active.kind === 'log' ? (
            <LogTab key={active.id} tab={active} />
          ) : active.kind === 'graph' ? (
            <GraphTab key={active.id} tab={active} />
          ) : active.kind === 'megraph' ? (
            <MEGraphTab key={active.id} tab={active} />
          ) : active.kind === 'modgraph' ? (
            <ModGraphTab key={active.id} tab={active} onReveal={(l) => revealModeling(active, l)} />
          ) : active.kind === 'archgraph' ? (
            <ArchGraphTab key={active.id} tab={active} />
          ) : active.kind === 'image' || active.kind === 'video' || active.kind === 'audio' || active.kind === 'pdf' ? (
            <MediaTab key={active.id} tab={active} />
          ) : active.kind === 'panestate' ? (
            <PaneStateCard key={active.id} tab={active} />
          ) : (
            <ModelTab key={active.id} tab={active} />
          )}
        </div>
        </div>
        {cmpBase !== null && (
          <div className="inspect-toast cmp">
            {t('inspect.comparing').replace('{name}', cmpBase.title)}
            <button className="link-btn small" onClick={() => (setCmpBase(null), setCmpMenu(false), setDialog(null), setRepoPick(false))}>
              {t('inspect.cancel')}
            </button>
          </div>
        )}
        {notFound !== null && (
          <div className="inspect-toast">
            {t('inspect.notFound')}: {baseName(notFound)}
          </div>
        )}
        {dialog !== null && <InspectOpenDialog mode={dialog} onClose={() => (setDialog(null), setCmpBase(null))} onPick={pick} onPinRoot={pinRoot} />}
        {repoDialog && <InspectRepoAddDialog onClose={() => setRepoDialog(false)} onAdd={(r) => (pinRoot(r), setRepoDialog(false))} />}
        {repoPick && <RepoPickDialog onClose={() => (setRepoPick(false), setCmpBase(null))} onPick={(r) => (setRepoPick(false), pick(r))} />}
        {panePick && <PaneStatePickDialog onClose={() => setPanePick(false)} onPick={openPaneState} />}
      </div>
    </WorkbenchSurface>
  );
}
