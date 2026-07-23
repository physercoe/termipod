import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { invoke } from '../bridge';
import { Icon, type IconName } from '../ui/Icon';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';
import type { CodeViewHandle } from '../ui/CodeView';
import { kindForInspectFile, useInspect, type InspectKind, type InspectRef, type InspectTab } from '../state/inspect';
import { looksLikeDot } from '../state/dotGraph';
import { TraceModal } from '../ui/TraceModal';
import { useWorkspace } from '../state/workspace';
import { readRef, readSource } from '../state/inspectSources';
import { useSession } from '../state/session';
import { runScript, type ScriptResult } from '../state/scriptRun';
import { parseTrace, type ParsedTrace, type TraceFrame } from '../state/stackTrace';
import { looksLikePatch } from '../state/patch';
import { looksLikeLog } from '../state/ansi';
import { extractSymbols, SUPPORTED_LANGS, type CodeSymbol } from '../state/treeSitter';
import { CodeOutline } from '../ui/CodeOutline';
import type { LogSource } from '../ui/LogView';
import { InspectOpenDialog, type OpenMode, type PickResult } from './InspectOpen';

// CodeMirror 6 + its search/language-data deps ride a lazy chunk (never the boot
// bundle — plan §7 bundle discipline), loaded the first time a code tab renders.
const CodeView = lazy(() => import('../ui/CodeView').then((m) => ({ default: m.CodeView })));
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
// The Graphviz DOT viewer — the wasm engine loads on first render (its own chunk).
const DotGraphView = lazy(() => import('../ui/DotGraphView').then((m) => ({ default: m.DotGraphView })));

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
    case 'diff':
      return 'split-h';
    case 'log':
      return 'list-ordered';
    case 'model':
      return 'sliders';
    default:
      return 'code';
  }
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
  const openTab = useInspect((s) => s.open);
  const folder = useWorkspace((s) => s.folder);
  const codeRef = useRef<CodeViewHandle>(null);
  const [runOut, setRunOut] = useState<ScriptResult | null>(null);
  const [running, setRunning] = useState(false);
  const [traceOpen, setTraceOpen] = useState(false);
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
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {error}
      </div>
    );

  const isPatch = looksLikePatch(body);
  const isDot = !isPatch && looksLikeDot(body);
  const isLog = !isPatch && !isDot && looksLikeLog(body);
  // A Python tab can be traced into a model graph (needs a local/SSH venue).
  const isPython = isShell() && (langId === 'python' || (tab.path?.toLowerCase().endsWith('.py') ?? false));
  const showRunBar = tab.source === 'paste' || interp !== null || isPatch || isLog || isDot || isPython;
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
          {isPython && (
            <button className="import-btn" onClick={() => setTraceOpen(true)}>
              <Icon name="diagram" size={14} /> {t('trace.action')}
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
        />
      )}
      <div className="inspect-codewrap">
        <div className="inspect-code">
          <Suspense fallback={<div className="muted region-pad">{t('inspect.loading')}</div>}>
            <CodeView
              ref={codeRef}
              value={body}
              onChange={(v) => setContent(tab.id, v)}
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
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {error}
      </div>
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
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {error}
      </div>
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

// ── model tab (W4) ────────────────────────────────────────────────────────────
// A checkpoint (`.safetensors`/`.gguf`) is parsed **header-only in the main
// process** (`checkpoint_inspect` by path — the bytes never leave disk, plan §5).
// W4 core reads a **local** file (the native picker); remote/hub checkpoints are a
// follow-on (they'd need an SFTP header-fetch), so those show an honest note.
function ModelTab({ tab }: { tab: InspectTab }): JSX.Element {
  const t = useT();
  if (!isShell() || tab.source !== 'local' || tab.path === undefined) {
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
      <div className="inspect-error region-pad">
        <Icon name="alert" size={16} /> {error}
      </div>
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

export function DebugSurface(): JSX.Element {
  const t = useT();
  const tabs = useInspect((s) => s.tabs);
  const activeId = useInspect((s) => s.activeId);
  const openTab = useInspect((s) => s.open);
  const closeTab = useInspect((s) => s.close);
  const setActive = useInspect((s) => s.setActive);
  const folder = useWorkspace((s) => s.folder);
  const client = useSession((s) => s.client);
  const [reveal, setReveal] = useState<Record<string, number>>({});
  const [notFound, setNotFound] = useState<string | null>(null);
  const [menu, setMenu] = useState(false);
  const [cmpMenu, setCmpMenu] = useState(false);
  const [dialog, setDialog] = useState<OpenMode | null>(null);
  // When set, the next file/tab the user picks becomes side B of a compare tab
  // whose side A is this base tab (W2 tier 2).
  const [cmpBase, setCmpBase] = useState<InspectTab | null>(null);
  const active = tabs.find((tb) => tb.id === activeId);
  // A tab is comparable if we can read its content: any tab except an existing
  // compare tab (comparing a compare makes no sense).
  const canCompare = active !== undefined && !(active.left !== undefined && active.right !== undefined);
  const otherTabs = tabs.filter((tb) => tb.id !== active?.id && !(tb.left !== undefined && tb.right !== undefined));

  function newScratch(): void {
    openTab({ kind: 'code', source: 'paste', title: t('inspect.scratch') }, '');
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
      makeCompare({ source: r.source, title: r.title, path: r.path, hostId: r.hostId, projectId: r.projectId, lang: langFromPath(r.path) });
      return;
    }
    openTab({ kind: r.kind, source: r.source, title: r.title, path: r.path, hostId: r.hostId, projectId: r.projectId });
    setDialog(null);
  }

  async function resolveFrame(frame: TraceFrame, from: InspectTab): Promise<void> {
    if (!isShell()) return;
    const cands: string[] = [];
    if (isAbs(frame.file)) cands.push(frame.file);
    if (folder !== null && folder !== '') cands.push(joinPath(folder, frame.file));
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
          <button className="import-btn" onClick={newScratch}>
            <Icon name="plus" size={14} /> {t('inspect.newScratch')}
          </button>
          <div className="inspect-openwrap">
            <button className="import-btn" aria-haspopup="menu" aria-expanded={menu} onClick={() => setMenu((m) => !m)}>
              <Icon name="folder" size={14} /> {t('inspect.open')} <Icon name="chevron-down" size={12} />
            </button>
            {menu && (
              <>
                <div className="inspect-menu-scrim" onClick={() => setMenu(false)} />
                <div className="inspect-menu" role="menu">
                  {isShell() && (
                    <button className="inspect-menu-item" role="menuitem" onClick={() => (setMenu(false), void openLocal())}>
                      <Icon name="file-text" size={14} /> {t('inspect.openFile')}
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
                </div>
              </>
            )}
          </div>
          {canCompare && (
            <div className="inspect-openwrap">
              <button className="import-btn" aria-haspopup="menu" aria-expanded={cmpMenu} onClick={beginCompare}>
                <Icon name="git-compare" size={14} /> {t('inspect.compare')} <Icon name="chevron-down" size={12} />
              </button>
              {cmpMenu && (
                <>
                  <div className="inspect-menu-scrim" onClick={() => (setCmpMenu(false), setCmpBase(null))} />
                  <div className="inspect-menu" role="menu">
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
                  </div>
                </>
              )}
            </div>
          )}
        </>
      }
    >
      <div className="inspect-shell">
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
                <Icon name={kindIcon(tb.kind)} size={13} />
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
          ) : active.kind === 'diff' ? (
            <DiffTab key={active.id} tab={active} />
          ) : active.kind === 'log' ? (
            <LogTab key={active.id} tab={active} />
          ) : active.kind === 'graph' ? (
            <GraphTab key={active.id} tab={active} />
          ) : (
            <ModelTab key={active.id} tab={active} />
          )}
        </div>
        {cmpBase !== null && (
          <div className="inspect-toast cmp">
            {t('inspect.comparing').replace('{name}', cmpBase.title)}
            <button className="link-btn small" onClick={() => (setCmpBase(null), setCmpMenu(false), setDialog(null))}>
              {t('inspect.cancel')}
            </button>
          </div>
        )}
        {notFound !== null && (
          <div className="inspect-toast">
            {t('inspect.notFound')}: {baseName(notFound)}
          </div>
        )}
        {dialog !== null && <InspectOpenDialog mode={dialog} onClose={() => (setDialog(null), setCmpBase(null))} onPick={pick} />}
      </div>
    </WorkbenchSurface>
  );
}
