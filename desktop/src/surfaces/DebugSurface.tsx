import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { invoke } from '../bridge';
import { Icon, type IconName } from '../ui/Icon';
import { WorkbenchSurface } from '../ui/WorkbenchSurface';
import type { CodeViewHandle } from '../ui/CodeView';
import { kindForInspectFile, useInspect, type InspectKind, type InspectTab } from '../state/inspect';
import { useWorkspace } from '../state/workspace';
import { readSource } from '../state/inspectSources';
import { useSession } from '../state/session';
import { runScript, type ScriptResult } from '../state/scriptRun';
import { parseTrace, type ParsedTrace, type TraceFrame } from '../state/stackTrace';
import { InspectOpenDialog, type OpenMode, type PickResult } from './InspectOpen';

// CodeMirror 6 + its search/language-data deps ride a lazy chunk (never the boot
// bundle — plan §7 bundle discipline), loaded the first time a code tab renders.
const CodeView = lazy(() => import('../ui/CodeView').then((m) => ({ default: m.CodeView })));

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
  const folder = useWorkspace((s) => s.folder);
  const codeRef = useRef<CodeViewHandle>(null);
  const [runOut, setRunOut] = useState<ScriptResult | null>(null);
  const [running, setRunning] = useState(false);

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

  const showRunBar = tab.source === 'paste' || interp !== null;
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
          {interp !== null && (
            <button className="import-btn" disabled={running} onClick={() => void run()}>
              <Icon name="play" size={14} /> {running ? t('inspect.running') : t('inspect.run')}
            </button>
          )}
        </div>
      )}
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
      {trace !== null && <TraceLens trace={trace} onOpen={(f) => onOpenFrame(f, tab)} />}
      {runOut !== null && <RunOutput res={runOut} />}
    </div>
  );
}

function WedgePlaceholder({ kind }: { kind: InspectKind }): JSX.Element {
  const t = useT();
  const key = kind === 'diff' ? 'inspect.wedgeDiff' : kind === 'log' ? 'inspect.wedgeLog' : 'inspect.wedgeModel';
  return (
    <div className="surface-placeholder region-pad">
      <div className="surface-posture">{t(key)}</div>
      <ul className="surface-todo">
        <li>{t('inspect.wedgeNote')}</li>
      </ul>
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
  const [dialog, setDialog] = useState<OpenMode | null>(null);
  const active = tabs.find((tb) => tb.id === activeId);

  function newScratch(): void {
    openTab({ kind: 'code', source: 'paste', title: t('inspect.scratch') }, '');
  }

  async function openLocal(): Promise<void> {
    if (!isShell()) return;
    const r = await invoke<{ path: string; content: string } | null>('debug_open', {});
    if (r === null) return;
    const kind = kindForInspectFile(extOf(r.path), r.content);
    openTab({ kind, source: 'local', title: baseName(r.path), path: r.path }, kind === 'model' ? undefined : r.content);
  }

  // A picker (workspace / remote / hub) chose a file — open it as a metadata-only
  // tab; its content is read lazily on activate via readSource.
  function pick(r: PickResult): void {
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
          ) : (
            <WedgePlaceholder kind={active.kind} />
          )}
        </div>
        {notFound !== null && (
          <div className="inspect-toast">
            {t('inspect.notFound')}: {baseName(notFound)}
          </div>
        )}
        {dialog !== null && <InspectOpenDialog mode={dialog} onClose={() => setDialog(null)} onPick={pick} />}
      </div>
    </WorkbenchSurface>
  );
}
