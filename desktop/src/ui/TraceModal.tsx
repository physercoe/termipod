import { useMemo, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { listConnections, type Connection } from '../state/connections';
import { detectInterpreter, getInterp, getLastForm, runTrace, setInterp, setLastForm } from '../state/trace';
import { detectTorch, runTraceExport } from '../state/traceExport';
import type { GraphCollection } from '../state/modelGraph';
import type { InspectTab } from '../state/inspect';
import { useInspectRoots, innermostLocalRoot } from '../state/inspectRoots';

/// Tier 1 = torchview weightless **architecture** graph → DOT viewer. Tier 2 =
/// `torch.export` **traced** ATen graph → the interactive Model Explorer element.
type TraceTier = 'arch' | 'traced';

function dirOf(p: string): string {
  const i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
  return i >= 0 ? p.slice(0, i) : '';
}
function baseOf(p: string): string {
  const i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
  return i >= 0 ? p.slice(i + 1) : p;
}

/// The **Trace model graph** form (plan §5, W4 Tier 1). Collects the entry
/// expression, input shape, depth and a **venue** (local interpreter or a saved
/// SSH host + interpreter preset), then runs the weightless meta-device torchview
/// trace and hands the resulting DOT back to open as a graph tab. The interpreter
/// preset persists per venue; the entry/shape/depth persist across opens.
export function TraceModal({
  tab,
  onClose,
  onGraph,
  onModelGraph,
}: {
  tab: InspectTab;
  onClose: () => void;
  onGraph: (dot: string, title: string) => void;
  onModelGraph: (graph: GraphCollection, title: string) => void;
}): JSX.Element {
  const t = useT();
  const conns = useMemo(() => listConnections(), []);
  const last = useMemo(() => getLastForm(), []);
  const roots = useInspectRoots((s) => s.roots);

  const [tier, setTier] = useState<TraceTier>('arch');
  // Default the venue to the tab's own SFTP host, else local.
  const [venue, setVenue] = useState<string>(tab.source === 'remote' && tab.hostId ? tab.hostId : 'local');
  const [command, setCommand] = useState<string>(() => getInterp(venue));
  // The repo-root default is the innermost pinned local root that contains the
  // file (import-locality is repo-shaped, not file-shaped), falling back to the
  // file's own directory as before (plan §3 item 6).
  const [repoRoot, setRepoRoot] = useState<string>(tab.path ? (innermostLocalRoot(roots, tab.path) ?? dirOf(tab.path)) : '');
  const [filePath, setFilePath] = useState<string>(tab.path ? baseOf(tab.path) : '');
  const [entry, setEntry] = useState<string>(last.entry);
  const [shape, setShape] = useState<string>(last.shape);
  const [depth, setDepth] = useState<number>(last.depth);
  const [busy, setBusy] = useState<null | 'detect' | 'trace'>(null);
  const [detected, setDetected] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const connection: Connection | undefined = venue === 'local' ? undefined : conns.find((c) => c.id === venue);

  function switchVenue(v: string): void {
    setVenue(v);
    setCommand(getInterp(v));
    setDetected(null);
    setErr(null);
  }

  function switchTier(v: TraceTier): void {
    setTier(v);
    setDetected(null);
    setErr(null);
  }

  async function detect(): Promise<void> {
    setBusy('detect');
    setDetected(null);
    setErr(null);
    try {
      setInterp(venue, command);
      const msg = tier === 'traced' ? await detectTorch(command, connection) : await detectInterpreter(command, connection);
      setDetected(msg);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function trace(): Promise<void> {
    setBusy('trace');
    setErr(null);
    try {
      setInterp(venue, command);
      setLastForm({ entry, shape, depth });
      const title = `graph: ${entry || baseOf(filePath) || 'model'}`;
      if (tier === 'traced') {
        onModelGraph(await runTraceExport({ entry, shape, depth, command, repoRoot, filePath, connection }), title);
      } else {
        onGraph(await runTrace({ entry, shape, depth, command, repoRoot, filePath, connection }), title);
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="inspect-modal-backdrop" onClick={onClose}>
      <div className="inspect-modal trace-modal" onClick={(e) => e.stopPropagation()}>
        <div className="inspect-modal-head">
          <Icon name="diagram" size={15} />
          <span>{t('trace.title')}</span>
          <span className="spacer" />
          <button className="icon-btn" onClick={onClose} aria-label={t('inspect.cancel')}>
            <Icon name="eye-off" size={14} />
          </button>
        </div>
        <div className="trace-body">
          <label className="trace-field">
            <span className="small muted">{t('trace.tier')}</span>
            <select className="surface-select" value={tier} onChange={(e) => switchTier(e.target.value as TraceTier)}>
              <option value="arch">{t('trace.tierArch')}</option>
              <option value="traced">{t('trace.tierTraced')}</option>
            </select>
          </label>
          <p className="small muted">{tier === 'traced' ? t('trace.blurbTraced') : t('trace.blurb')}</p>

          <label className="trace-field">
            <span className="small muted">{t('trace.venue')}</span>
            <select className="surface-select" value={venue} onChange={(e) => switchVenue(e.target.value)}>
              <option value="local">{t('trace.local')}</option>
              {conns.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>

          <label className="trace-field">
            <span className="small muted">{t('trace.interpreter')}</span>
            <span className="trace-inline">
              <input className="trace-input mono" value={command} placeholder="python3" onChange={(e) => setCommand(e.target.value)} />
              <button className="import-btn" disabled={busy !== null} onClick={() => void detect()}>
                {busy === 'detect' ? t('trace.detecting') : t('trace.detect')}
              </button>
            </span>
          </label>
          {detected !== null && <div className="trace-ok small mono">✓ {detected}</div>}

          <label className="trace-field">
            <span className="small muted">{t('trace.repoRoot')}</span>
            <input className="trace-input mono" value={repoRoot} placeholder="/path/to/repo" onChange={(e) => setRepoRoot(e.target.value)} />
          </label>
          <label className="trace-field">
            <span className="small muted">{t('trace.file')}</span>
            <input className="trace-input mono" value={filePath} placeholder="model.py" onChange={(e) => setFilePath(e.target.value)} />
          </label>
          <label className="trace-field">
            <span className="small muted">{t('trace.entry')}</span>
            <input className="trace-input mono" value={entry} placeholder="Model(dim=512)" onChange={(e) => setEntry(e.target.value)} />
          </label>
          <div className="trace-row">
            <label className="trace-field grow">
              <span className="small muted">{t('trace.shape')}</span>
              <input className="trace-input mono" value={shape} placeholder="1, 3, 224, 224" onChange={(e) => setShape(e.target.value)} />
            </label>
            {tier === 'arch' && (
              <label className="trace-field">
                <span className="small muted">{t('trace.depth')}</span>
                <input
                  className="trace-input trace-depth"
                  type="number"
                  min={1}
                  max={12}
                  value={depth}
                  onChange={(e) => setDepth(Math.max(1, Math.min(12, Number(e.target.value) || 1)))}
                />
              </label>
            )}
          </div>

          {err !== null && (
            <pre className="trace-err mono small">
              <Icon name="alert" size={13} /> {err}
            </pre>
          )}

          <div className="trace-actions">
            <span className="small muted">{t('trace.note')}</span>
            <span className="spacer" />
            <button className="import-btn" onClick={onClose}>
              {t('inspect.cancel')}
            </button>
            <button className="import-btn primary" disabled={busy !== null || entry.trim() === ''} onClick={() => void trace()}>
              <Icon name="diagram" size={14} /> {busy === 'trace' ? t('trace.tracing') : t('trace.run')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
