import { useState } from 'react';
import { num, obj, str, type Entity } from '../hub/types';
import { type TLookup } from '../i18n';
import { Icon } from './Icon';

/// Agent config + runtime inspector (parity — mobile `agent_config_sheet.dart`
/// AND `session_details_sheet.dart`, consolidated into one Info tab).
///
/// Two data sources, two halves:
///   - SESSION config — the merged `session.init` frame (engine, model,
///     permission, workdir, tools, mcp, slash, …) plus the latest
///     `status_line` frame (live effort / thinking / fast-mode). These are
///     feed-derived, so the transcript computes them and passes them in.
///   - AGENT record — the single-agent `GET /agents/{id}` map: persona,
///     runtime, and the exact spawn spec.
///
/// Read-only throughout: lifecycle actions live in the transcript bar and
/// reconfig is delegated to the steward, so this surface only reads (+ copies
/// the spec).

function relTime(iso: string | undefined): string | undefined {
  if (iso === undefined || iso === '') return undefined;
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return undefined;
  const secs = Math.max(0, (Date.now() - ms) / 1000);
  if (secs < 60) return 'now';
  const m = Math.floor(secs / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

/// Derived operating role (mobile `_operationRole`): a steward-kind agent is a
/// steward, everything else a worker. (Mobile also promotes on a `team.` default
/// role parsed from the spec; kind is the load-bearing signal we mirror here.)
function opRole(kind: string): 'steward' | 'worker' {
  return kind.startsWith('steward.') ? 'steward' : 'worker';
}

/// Merge every `session.init` frame in the feed (later frames overwrite fields,
/// earlier-only fields persist), mirroring mobile `latestSessionInitPayload`.
/// Most engines emit it once; antigravity emits two partials (the second carries
/// only `{model}`) that must merge rather than shadow, or the engine pill drops.
export function mergeSessionInit(events: Entity[]): Entity | undefined {
  let merged: Entity | undefined;
  for (const e of events) {
    if (str(e, 'kind') !== 'session.init') continue;
    const p = obj(e, 'payload');
    if (p === undefined) continue;
    merged = merged === undefined ? { ...p } : { ...merged, ...p };
  }
  return merged;
}

/// The newest `status_line` frame's payload (mobile `latestStatusLinePayload`).
/// claude resends the full snapshot every ~10s, so the last frame is always the
/// authoritative live state (effort / thinking / fast-mode / output_style).
export function latestStatusLine(events: Entity[]): Entity | undefined {
  for (let i = events.length - 1; i >= 0; i--) {
    if (str(events[i], 'kind') !== 'status_line') continue;
    const p = obj(events[i], 'payload');
    if (p !== undefined) return p;
  }
  return undefined;
}

/// `status_line` ships some fields nested (`{level}`, `{name}`, `{enabled}`) or
/// as a bare scalar depending on engine/version — accept both shapes, mirroring
/// mobile's defensive `statusLine*` reducers.
function nestedStr(p: Entity | undefined, key: string, inner: string): string | undefined {
  if (p === undefined) return undefined;
  const v = p[key];
  if (typeof v === 'string') return v === '' ? undefined : v;
  if (v !== null && typeof v === 'object') {
    const iv = (v as Entity)[inner];
    if (typeof iv === 'string') return iv === '' ? undefined : iv;
  }
  return undefined;
}

function nestedBool(p: Entity | undefined, key: string, inner: string): boolean | undefined {
  if (p === undefined) return undefined;
  const v = p[key];
  if (typeof v === 'boolean') return v;
  if (v !== null && typeof v === 'object') {
    const iv = (v as Entity)[inner];
    if (typeof iv === 'boolean') return iv;
  }
  return undefined;
}

/// Coerce a wire value into a list of strings (mobile `_payloadToList`).
function strList(v: unknown): string[] {
  if (!Array.isArray(v)) return [];
  return v.map((e) => String(e));
}

/// Coerce a wire value into a list of maps (mobile `_payloadToMapList`), for
/// `mcp_servers` rows that carry `{name, status}`.
function mapList(v: unknown): Entity[] {
  if (!Array.isArray(v)) return [];
  return v.filter((e): e is Entity => e !== null && typeof e === 'object' && !Array.isArray(e));
}

/// Permission-mode risk class — approval-gated policies read calm, auto-approve
/// policies read hot (mobile `_permModeColor`). Unknown/experimental → no tint.
function permClass(mode: string): string | undefined {
  switch (mode) {
    case 'default':
    case 'plan':
    case 'on-request':
    case 'untrusted':
    case 'interactive':
      return 'ai-ok';
    case 'acceptEdits':
    case 'on-failure':
      return 'ai-warn';
    case 'bypassPermissions':
    case 'never':
    case 'dangerously-skip-permissions':
      return 'ai-danger';
    default:
      return undefined;
  }
}

/// Effort-level tint — warmer as the depth-of-thought knob climbs (mobile
/// `_effortColor`), so an unusually expensive run stands out at a glance.
function effortClass(level: string): string | undefined {
  switch (level.toLowerCase()) {
    case 'medium':
      return 'ai-ok';
    case 'high':
      return 'ai-warn';
    case 'xhigh':
      return 'ai-danger';
    default:
      return undefined;
  }
}

/// The MCP status pill's color signal (mobile `_mcpStatusColor`).
function mcpClass(status: string): string | undefined {
  switch (status.toLowerCase()) {
    case 'connected':
    case 'ok':
      return 'ai-ok';
    case 'needs-auth':
    case 'pending-auth':
      return 'ai-warn';
    case 'failed':
    case 'error':
      return 'ai-danger';
    default:
      return undefined;
  }
}

function KV({ label, value, mono, cls }: { label: string; value: string; mono?: boolean; cls?: string }): JSX.Element {
  const valClass = ['ai-val', mono === true ? 'mono' : '', cls ?? ''].filter(Boolean).join(' ');
  return (
    <div className="ai-kv">
      <span className="ai-key">{label}</span>
      <span className={valClass}>{value}</span>
    </div>
  );
}

/// One section — rendered only when it has at least one populated row.
function Section({ title, rows }: { title: string; rows: (JSX.Element | null)[] }): JSX.Element | null {
  const shown = rows.filter((r): r is JSX.Element => r !== null);
  if (shown.length === 0) return null;
  return (
    <div className="ai-section">
      <div className="ai-section-head">{title}</div>
      {shown}
    </div>
  );
}

/// A KV row that self-hides when its value is empty (mobile `_kvLine` gate).
function row(label: string, value: string | undefined, opts?: { mono?: boolean; cls?: string }): JSX.Element | null {
  if (value === undefined || value === '') return null;
  return <KV key={label} label={label} value={value} mono={opts?.mono} cls={opts?.cls} />;
}

function money(cents: number | undefined): string | undefined {
  if (cents === undefined) return undefined;
  const usd = cents / 100;
  return usd >= 1 ? `$${usd.toFixed(2)}` : `$${usd.toFixed(4)}`;
}

/// Bordered chip cloud for the string-list frames (tools / slash / agents /
/// skills / plugins), mirroring mobile `_ChipWrap`.
function ChipWrap({ items }: { items: string[] }): JSX.Element {
  return (
    <div className="ai-chips">
      {items.map((it) => (
        <span className="ai-chip mono" key={it}>
          {it}
        </span>
      ))}
    </div>
  );
}

/// Trim the long claude model string down to family + version so the pill stays
/// readable (mobile `SessionInitChip._shortModel`). Unknown shapes pass through.
function shortModel(raw: string): string {
  if (raw.startsWith('claude-')) {
    const parts = raw.split('-');
    if (parts.length >= 4) return `${parts[1]} ${parts[2]}.${parts[3]}`;
  }
  return raw;
}

/// Feed-derived session config: the merged `session.init` frame plus the live
/// `status_line`. `engineKind` is the agent's real backend engine
/// (`backend.kind`) — session.init carries the model but not the engine hosting
/// it, so we thread it in from the agent record (#67).
function SessionConfig({
  init,
  status,
  engineKind,
  agentKind,
  t,
}: {
  init: Entity | undefined;
  status: Entity | undefined;
  engineKind: string | undefined;
  agentKind: string;
  t: TLookup;
}): JSX.Element | null {
  if (init === undefined && status === undefined) return null;

  const p: Entity = init ?? {};
  const model = str(p, 'model');
  const version = str(p, 'version');
  const permMode = str(p, 'permission_mode');
  const sessionId = str(p, 'session_id');
  const cwd = str(p, 'cwd');
  // output_style: statusLine's live value (a `/style` toggle) wins over the
  // spawn-time session.init value.
  const outputStyle = nestedStr(status, 'output_style', 'name') ?? str(p, 'output_style');

  // #67: show the real backend engine, falling back to the template kind so the
  // row never blanks. `version` is the engine/CLI build, so it rides here.
  const engineLabel = engineKind !== undefined && engineKind !== '' ? engineKind : agentKind;
  const engineLine = [engineLabel, version !== undefined ? `v${version}` : '']
    .filter((x) => x !== undefined && x !== '')
    .join(' · ');
  const showKindRow = agentKind !== '' && agentKind !== engineLabel;

  const effort = nestedStr(status, 'effort', 'level');
  const thinking = nestedBool(status, 'thinking', 'enabled');
  const fastMode = nestedBool(status, 'fast_mode', 'fast_mode');

  const tools = strList(p['tools']);
  const mcp = mapList(p['mcp_servers']);
  const slash = strList(p['slash_commands']);
  const subAgents = strList(p['agents']);
  const skills = strList(p['skills']);
  const plugins = strList(p['plugins']);

  const onOff = (v: boolean): string => (v ? t('cfg.on') : t('cfg.off'));

  return (
    <>
      <Section
        title={t('cfg.session')}
        rows={[
          row(t('cfg.engine'), engineLine === '' ? undefined : engineLine),
          showKindRow ? row(t('info.kind'), agentKind) : null,
          row(t('cfg.model'), model !== undefined ? shortModel(model) : undefined),
          row(t('cfg.permission'), permMode, { cls: permMode !== undefined ? permClass(permMode) : undefined }),
          row(t('cfg.style'), outputStyle),
          row(t('cfg.sessionId'), sessionId, { mono: true }),
        ]}
      />
      <Section
        title={t('cfg.state')}
        rows={[
          row(t('cfg.effort'), effort, { cls: effort !== undefined ? effortClass(effort) : undefined }),
          thinking !== undefined ? row(t('cfg.thinking'), onOff(thinking), { cls: thinking ? 'ai-ok' : undefined }) : null,
          fastMode !== undefined ? row(t('cfg.fastMode'), onOff(fastMode), { cls: fastMode ? 'ai-warn' : undefined }) : null,
        ]}
      />
      {cwd !== undefined && cwd !== '' && (
        <Section title={t('cfg.workdir')} rows={[row(t('cfg.cwd'), cwd, { mono: true })]} />
      )}
      {tools.length > 0 && (
        <div className="ai-section">
          <div className="ai-section-head">{`${t('cfg.tools')} · ${tools.length}`}</div>
          <ChipWrap items={tools} />
        </div>
      )}
      {mcp.length > 0 && (
        <div className="ai-section">
          <div className="ai-section-head">{`${t('cfg.mcp')} · ${mcp.length}`}</div>
          {mcp.map((s, i) => {
            const name = str(s, 'name') ?? '?';
            const st = str(s, 'status') ?? '';
            return (
              <div className="ai-mcp" key={`${name}:${i}`}>
                <span className="ai-mcp-name mono">{name}</span>
                {st !== '' && <span className={['ai-mcp-status', mcpClass(st) ?? ''].filter(Boolean).join(' ')}>{st}</span>}
              </div>
            );
          })}
        </div>
      )}
      {slash.length > 0 && (
        <div className="ai-section">
          <div className="ai-section-head">{`${t('cfg.slash')} · ${slash.length}`}</div>
          <ChipWrap items={slash} />
        </div>
      )}
      {subAgents.length > 0 && (
        <div className="ai-section">
          <div className="ai-section-head">{`${t('cfg.agents')} · ${subAgents.length}`}</div>
          <ChipWrap items={subAgents} />
        </div>
      )}
      {skills.length > 0 && (
        <div className="ai-section">
          <div className="ai-section-head">{`${t('cfg.skills')} · ${skills.length}`}</div>
          <ChipWrap items={skills} />
        </div>
      )}
      {plugins.length > 0 && (
        <div className="ai-section">
          <div className="ai-section-head">{`${t('cfg.plugins')} · ${plugins.length}`}</div>
          <ChipWrap items={plugins} />
        </div>
      )}
    </>
  );
}

export function AgentInfo({
  agent,
  init,
  status,
  t,
}: {
  agent: Entity;
  init?: Entity;
  status?: Entity;
  t: TLookup;
}): JSX.Element {
  const [copied, setCopied] = useState(false);
  const kind = str(agent, 'kind') ?? '';
  const mode = str(agent, 'mode') ?? str(agent, 'driving_mode');
  const spec = str(agent, 'spawn_spec_yaml');
  const budget = num(agent, 'budget_cents');
  const spent = num(agent, 'spent_cents');
  const created = str(agent, 'created_at');
  const lastEvent = str(agent, 'last_event_at');
  // The real backend engine (claude-code, codex, …) — nested `backend.kind` on
  // the agent record, distinct from the template `kind` for a steward (#67).
  const engineKind = str(obj(agent, 'backend') ?? {}, 'kind');

  async function copySpec(): Promise<void> {
    if (spec === undefined) return;
    try {
      await navigator.clipboard.writeText(spec);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      /* ignore */
    }
  }

  const role = opRole(kind);

  return (
    <div className="agent-info">
      <SessionConfig init={init} status={status} engineKind={engineKind} agentKind={kind} t={t} />
      <Section
        title={t('info.persona')}
        rows={[
          <div className="ai-kv" key="role">
            <span className="ai-key">{t('info.opRole')}</span>
            <span className={role === 'steward' ? 'ai-val ai-role-steward' : 'ai-val'}>{t(`info.role.${role}`)}</span>
          </div>,
          row(t('info.handle'), str(agent, 'handle')),
          row(t('info.kind'), kind),
          row(t('info.mode'), mode),
        ]}
      />
      <Section
        title={t('info.runtime')}
        rows={[
          row(t('info.status'), str(agent, 'status')),
          row(t('info.pauseState'), str(agent, 'pause_state')),
          row(t('info.host'), str(agent, 'host_id'), { mono: true }),
          row(t('info.parent'), str(agent, 'parent_agent_id'), { mono: true }),
          row(t('info.project'), str(agent, 'project_id'), { mono: true }),
          row(t('info.worktree'), str(agent, 'worktree_path'), { mono: true }),
          row(t('info.journal'), str(agent, 'journal_path'), { mono: true }),
          row(t('info.created'), relTime(created)),
          row(t('info.lastEvent'), relTime(lastEvent)),
          row(
            t('info.spend'),
            budget !== undefined || spent !== undefined
              ? `${money(spent) ?? '$0'}${budget !== undefined ? ` / ${money(budget)}` : ''}`
              : undefined,
          ),
        ]}
      />
      {spec !== undefined && spec !== '' && (
        <div className="ai-section">
          <div className="ai-section-head ai-spec-head">
            <span>{t('info.spawnSpec')}</span>
            <button className="ai-copy" onClick={() => void copySpec()} title={t('info.copyYaml')}>
              <Icon name={copied ? 'check' : 'copy'} size={13} />
              {copied ? t('tx.copied') : t('info.copyYaml')}
            </button>
          </div>
          <pre className="ai-yaml mono">{spec}</pre>
        </div>
      )}
    </div>
  );
}
