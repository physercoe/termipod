import { useState } from 'react';
import { num, str, type Entity } from '../hub/types';
import { type TLookup } from '../i18n';
import { Icon } from './Icon';

/// Agent config + runtime inspector (parity — mobile `agent_config_sheet.dart`).
/// Read-only view over the single-agent `GET /agents/{id}` map: what the agent is
/// (persona), where + how it runs (runtime), and the exact spawn spec. Lifecycle
/// actions live in the transcript bar; reconfig is delegated to the steward, so
/// this surface only reads (and copies the spec).

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

function KV({ label, value, mono }: { label: string; value: string; mono?: boolean }): JSX.Element {
  return (
    <div className="ai-kv">
      <span className="ai-key">{label}</span>
      <span className={mono === true ? 'ai-val mono' : 'ai-val'}>{value}</span>
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
function row(label: string, value: string | undefined, mono?: boolean): JSX.Element | null {
  if (value === undefined || value === '') return null;
  return <KV key={label} label={label} value={value} mono={mono} />;
}

function money(cents: number | undefined): string | undefined {
  if (cents === undefined) return undefined;
  const usd = cents / 100;
  return usd >= 1 ? `$${usd.toFixed(2)}` : `$${usd.toFixed(4)}`;
}

export function AgentInfo({ agent, t }: { agent: Entity; t: TLookup }): JSX.Element {
  const [copied, setCopied] = useState(false);
  const kind = str(agent, 'kind') ?? '';
  const mode = str(agent, 'mode') ?? str(agent, 'driving_mode');
  const spec = str(agent, 'spawn_spec_yaml');
  const budget = num(agent, 'budget_cents');
  const spent = num(agent, 'spent_cents');
  const created = str(agent, 'created_at');
  const lastEvent = str(agent, 'last_event_at');

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
          row(t('info.host'), str(agent, 'host_id'), true),
          row(t('info.parent'), str(agent, 'parent_agent_id'), true),
          row(t('info.project'), str(agent, 'project_id'), true),
          row(t('info.worktree'), str(agent, 'worktree_path'), true),
          row(t('info.journal'), str(agent, 'journal_path'), true),
          row(t('info.created'), relTime(created), false),
          row(t('info.lastEvent'), relTime(lastEvent), false),
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
