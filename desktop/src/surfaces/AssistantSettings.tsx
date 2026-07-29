import { useEffect, useState } from 'react';
import { useT } from '../i18n';
import { Icon } from '../ui/Icon';
import { invoke } from '../bridge';
import { isShell } from '../platform';
import { useAssistant } from '../state/assistant';
import { useWorkbench } from '../state/workbench';
import { kindForInspectFile, useInspect } from '../state/inspect';
import { localHome, localList, localRead } from '../state/localfs';

/// Settings → Assistant: the config home for the embedded kimi-code assistant.
/// Shows the shared `kimi web` server status + dock control, then the assistant's
/// customization surfaces — config.toml, MCP servers, skills, plugins — resolved
/// from the kimi-code data root (`$KIMI_CODE_HOME`, asked of the main process
/// via `kimiweb_home`) plus the tool-shared `~/.agents/skills` dir. The listed
/// paths are verified against the kimi-code 0.28.1 binary (#451): there is NO
/// SYSTEM.md or agents-directory convention (don't re-add those rows), plugins
/// live in `plugins/` (installed.json + marketplace.json, not plugins/managed).
/// Read-and-open v1: each row shows what exists and opens the file in the
/// Inspect tab (J3) for viewing; editing stays with the tools that own these
/// files. All listing is via the existing localfs IPC — no new privileges.

interface ConfigRow {
  /// Absolute path (file or directory).
  path: string;
  /// Directory rows list their entries as children (one level, names only).
  isDir: boolean;
  exists: boolean;
  /// For a dir: entry names (files + dirs, dotfiles skipped). For mcp.json: server names.
  entries: string[];
}

interface Sections {
  home: string;
  config: ConfigRow;
  mcp: ConfigRow;
  skills: ConfigRow[];
  plugins: ConfigRow;
}

async function statFile(path: string): Promise<ConfigRow> {
  try {
    await localRead(path);
    return { path, isDir: false, exists: true, entries: [] };
  } catch {
    return { path, isDir: false, exists: false, entries: [] };
  }
}

async function statDir(path: string): Promise<ConfigRow> {
  try {
    const l = await localList(path);
    const entries = l.entries.filter((e) => !e.name.startsWith('.')).map((e) => e.name);
    return { path, isDir: true, exists: true, entries };
  } catch {
    return { path, isDir: true, exists: false, entries: [] };
  }
}

/// The server names out of an mcp.json (`{"mcpServers": {name: …}}`), best-effort.
async function statMcp(path: string): Promise<ConfigRow> {
  const row = await statFile(path);
  if (!row.exists) return row;
  try {
    const text = new TextDecoder().decode(await localRead(path));
    const parsed = JSON.parse(text) as unknown;
    if (parsed !== null && typeof parsed === 'object') {
      const servers = (parsed as Record<string, unknown>)['mcpServers'];
      if (servers !== null && typeof servers === 'object' && !Array.isArray(servers)) {
        return { ...row, entries: Object.keys(servers as Record<string, unknown>) };
      }
    }
  } catch {
    /* unparseable — still openable */
  }
  return row;
}

async function loadSections(): Promise<Sections> {
  const osHome = await localHome();
  const { home } = await invoke<{ home: string }>('kimiweb_home');
  const [config, mcp, plugins, kimiSkills, agentsSkills] = await Promise.all([
    statFile(`${home}/config.toml`),
    statMcp(`${home}/mcp.json`),
    // kimi-code 0.28.1's plugin root: installed.json + marketplace.json +
    // one dir per installed plugin (NOT plugins/managed — see #451).
    statDir(`${home}/plugins`),
    statDir(`${home}/skills`),
    statDir(`${osHome}/.agents/skills`),
  ]);
  return { home, config, mcp, plugins, skills: [kimiSkills, agentsSkills] };
}

export function AssistantSettings(): JSX.Element {
  const t = useT();
  const shell = isShell();
  const setJob = useWorkbench((s) => s.setJob);
  const { open: dockOpen, setOpen } = useAssistant();
  const [status, setStatus] = useState<{ running: boolean } | null>(null);
  const [sections, setSections] = useState<Sections | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!shell) return;
    let cancelled = false;
    invoke<{ running: boolean }>('kimiweb_status')
      .then((s) => !cancelled && setStatus(s))
      .catch(() => undefined);
    loadSections()
      .then((s) => !cancelled && setSections(s))
      .catch((e: unknown) => !cancelled && setError(e instanceof Error ? e.message : String(e)));
    return () => {
      cancelled = true;
    };
  }, [shell]);

  // Open a config file in the Inspect (J3) tab — view/verify posture; the
  // assistant's own tools stay the editors of record.
  async function openInInspect(path: string): Promise<void> {
    try {
      const content = new TextDecoder().decode(await localRead(path));
      const ext = path.split('.').pop() ?? '';
      const title = path.replace(/\\/g, '/').split('/').pop() ?? path;
      useInspect.getState().open({ kind: kindForInspectFile(ext, content), source: 'local', title, path }, content);
      setJob('debug');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  if (!shell) {
    return (
      <section className="setting-group">
        <h3>{t('assistant.title')}</h3>
        <p className="muted small">{t('assistant.desktopOnly')}</p>
      </section>
    );
  }

  const fileRow = (label: string, row: ConfigRow | undefined, hint?: string): JSX.Element | null => {
    if (row === undefined) return null;
    return (
      <div className="assistant-cfg-row" key={row.path}>
        <div className="assistant-cfg-main">
          <span className="assistant-cfg-label">{label}</span>
          <span className="mono small muted assistant-cfg-path" title={row.path}>
            {row.path}
          </span>
          {hint !== undefined && <span className="small muted">{hint}</span>}
        </div>
        {row.exists ? (
          <>
            {row.entries.length > 0 && (
              <div className="assistant-cfg-entries">
                {row.entries.map((e) => (
                  <span key={e} className="host-chip">
                    {e}
                  </span>
                ))}
              </div>
            )}
            {!row.isDir && (
              <button className="import-btn" onClick={() => void openInInspect(row.path)}>
                <Icon name="eye" size={13} /> {t('assistant.openInspect')}
              </button>
            )}
          </>
        ) : (
          <span className="small muted">{t('assistant.absent')}</span>
        )}
      </div>
    );
  };

  return (
    <section className="setting-group">
      <h3>{t('assistant.title')}</h3>
      <p className="muted small settings-lead">{t('assistant.settingsLead')}</p>

      <div className="assistant-cfg-row">
        <div className="assistant-cfg-main">
          <span className="assistant-cfg-label">{t('assistant.serverStatus')}</span>
          <span className={`pill small ${status?.running === true ? 'ok' : ''}`}>
            {status === null ? '…' : status.running ? t('assistant.running') : t('assistant.stopped')}
          </span>
        </div>
        <button className="import-btn" onClick={() => setOpen(!dockOpen)}>
          <Icon name="globe" size={13} /> {dockOpen ? t('assistant.hide') : t('assistant.openDock')}
        </button>
      </div>

      {error !== null && <div className="error small">{error}</div>}
      {sections === null ? (
        <div className="muted small">{t('common.loading')}</div>
      ) : (
        <>
          <h4 className="assistant-cfg-h">{t('assistant.secConfig')}</h4>
          {fileRow(t('assistant.rowConfig'), sections.config)}
          <h4 className="assistant-cfg-h">{t('assistant.secMcp')}</h4>
          {fileRow(t('assistant.rowMcp'), sections.mcp)}
          <h4 className="assistant-cfg-h">{t('assistant.secSkills')}</h4>
          {sections.skills.map((r) => fileRow(t('assistant.rowSkillsDir'), r))}
          <h4 className="assistant-cfg-h">{t('assistant.secPlugins')}</h4>
          {fileRow(t('assistant.rowPlugins'), sections.plugins)}
        </>
      )}
    </section>
  );
}
