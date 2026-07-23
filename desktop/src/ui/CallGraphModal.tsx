import { useMemo, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { listConnections, type Connection } from '../state/connections';
import { detectCode2flow, getLastLang, runCallGraph, setLastLang, type CallGraphLang } from '../state/callGraph';
import { getInterp, setInterp } from '../state/trace';
import type { InspectTab } from '../state/inspect';

function dirOf(p: string): string {
  const i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
  return i >= 0 ? p.slice(0, i) : '';
}
function baseOf(p: string): string {
  const i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
  return i >= 0 ? p.slice(i + 1) : p;
}

const LANGS: { value: CallGraphLang; key: string }[] = [
  { value: '', key: 'callgraph.auto' },
  { value: 'py', key: 'callgraph.py' },
  { value: 'js', key: 'callgraph.js' },
  { value: 'rb', key: 'callgraph.rb' },
  { value: 'php', key: 'callgraph.php' },
];

/// The **Call graph** form (plan §5, W4) — the tracer's static-analysis sibling.
/// Collects one or more target files/dirs, a language (or auto-detect), and a
/// **venue** (local interpreter or a saved SSH host + interpreter preset), then runs
/// code2flow and hands the resulting DOT back to open as a graph tab. The
/// interpreter preset is shared with the tracer (same venue); the language persists.
export function CallGraphModal({ tab, onClose, onGraph }: { tab: InspectTab; onClose: () => void; onGraph: (dot: string, title: string) => void }): JSX.Element {
  const t = useT();
  const conns = useMemo(() => listConnections(), []);

  const [venue, setVenue] = useState<string>(tab.source === 'remote' && tab.hostId ? tab.hostId : 'local');
  const [command, setCommand] = useState<string>(() => getInterp(venue));
  const [repoRoot, setRepoRoot] = useState<string>(tab.path ? dirOf(tab.path) : '');
  const [targets, setTargets] = useState<string>(tab.path ? baseOf(tab.path) : '');
  const [lang, setLang] = useState<CallGraphLang>(() => getLastLang() as CallGraphLang);
  const [busy, setBusy] = useState<null | 'detect' | 'run'>(null);
  const [detected, setDetected] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const connection: Connection | undefined = venue === 'local' ? undefined : conns.find((c) => c.id === venue);

  function switchVenue(v: string): void {
    setVenue(v);
    setCommand(getInterp(v));
    setDetected(null);
    setErr(null);
  }

  async function detect(): Promise<void> {
    setBusy('detect');
    setDetected(null);
    setErr(null);
    try {
      setInterp(venue, command);
      setDetected(await detectCode2flow(command, connection));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function run(): Promise<void> {
    setBusy('run');
    setErr(null);
    try {
      setInterp(venue, command);
      setLastLang(lang);
      const dot = await runCallGraph({ targets, lang, command, repoRoot, connection });
      const first = targets.split('\n').map((s) => s.trim()).find((s) => s !== '') ?? 'code';
      onGraph(dot, `calls: ${baseOf(first)}`);
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
          <span>{t('callgraph.title')}</span>
          <span className="spacer" />
          <button className="icon-btn" onClick={onClose} aria-label={t('inspect.cancel')}>
            <Icon name="eye-off" size={14} />
          </button>
        </div>
        <div className="trace-body">
          <p className="small muted">{t('callgraph.blurb')}</p>

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
            <span className="small muted">{t('callgraph.targets')}</span>
            <textarea
              className="trace-input mono trace-targets"
              value={targets}
              placeholder={'model.py\npkg/'}
              rows={3}
              onChange={(e) => setTargets(e.target.value)}
            />
          </label>
          <label className="trace-field">
            <span className="small muted">{t('callgraph.lang')}</span>
            <select className="surface-select" value={lang} onChange={(e) => setLang(e.target.value as CallGraphLang)}>
              {LANGS.map((l) => (
                <option key={l.value} value={l.value}>
                  {t(l.key)}
                </option>
              ))}
            </select>
          </label>

          {err !== null && (
            <pre className="trace-err mono small">
              <Icon name="alert" size={13} /> {err}
            </pre>
          )}

          <div className="trace-actions">
            <span className="small muted">{t('callgraph.note')}</span>
            <span className="spacer" />
            <button className="import-btn" onClick={onClose}>
              {t('inspect.cancel')}
            </button>
            <button className="import-btn primary" disabled={busy !== null || targets.trim() === ''} onClick={() => void run()}>
              <Icon name="diagram" size={14} /> {busy === 'run' ? t('callgraph.running') : t('callgraph.run')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
