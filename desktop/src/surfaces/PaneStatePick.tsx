import { useQuery } from '@tanstack/react-query';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Icon } from '../ui/Icon';

/// Picks the agent whose pane to explain (pane-state-manifests P4).
///
/// Only running agents with a pane are offered, and an agent whose family has
/// no manifest is shown greyed with the reason rather than hidden: "this engine
/// has no rules" is the answer to "why is it never classified", and hiding the
/// row would leave the reader to guess.

export interface PaneAgentPick {
  agentId: string;
  title: string;
}

function str(o: Record<string, unknown>, k: string): string {
  const v = o[k];
  return typeof v === 'string' ? v : '';
}

export function PaneStatePickDialog({
  onClose,
  onPick,
}: {
  onClose: () => void;
  onPick: (p: PaneAgentPick) => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);

  const agentsQ = useQuery({
    queryKey: ['pane-pick-agents'],
    enabled: client !== null,
    queryFn: () => client!.listAgents({ status: 'running' }),
  });
  const coverageQ = useQuery({
    queryKey: ['pane-coverage'],
    enabled: client !== null,
    staleTime: 5 * 60_000,
    queryFn: () => client!.listPaneCoverage(),
  });

  const mapped = new Set(
    (coverageQ.data ?? []).map((f) => str(f as Record<string, unknown>, 'family')).filter((f) => f !== ''),
  );
  // A paneless agent (a driving mode with no tmux pane) has nothing to read,
  // and the hub would answer 409. Filtering here turns a refusal into an
  // absence, which is the honest rendering of "not applicable".
  const rows = (agentsQ.data ?? [])
    .map((a) => a as Record<string, unknown>)
    .filter((a) => str(a, 'pane_id') !== '');

  return (
    <div className="inspect-modal-backdrop" onClick={onClose}>
      <div
        className="inspect-modal"
        role="dialog"
        aria-label={t('panestate.pickTitle')}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="inspect-modal-head">
          <span className="inspect-modal-title">{t('panestate.pickTitle')}</span>
          <span className="spacer" />
          <button type="button" className="icon-btn" title={t('inspect.close')} onClick={onClose}>
            <Icon name="close" size={15} />
          </button>
        </div>
        <div className="ps-pick-body">
          {agentsQ.isLoading ? (
            <p className="muted">{t('panestate.loading')}</p>
          ) : rows.length === 0 ? (
            <p className="muted small">{t('panestate.noPanes')}</p>
          ) : (
            <ul className="ps-pick-list">
              {rows.map((a) => {
                const id = str(a, 'id');
                const handle = str(a, 'handle');
                const kind = str(a, 'kind');
                const covered = mapped.has(kind);
                return (
                  <li key={id}>
                    <button
                      type="button"
                      className="ps-pick-row"
                      disabled={!covered}
                      title={covered ? undefined : t('panestate.unmapped').replace('{family}', kind)}
                      onClick={() => onPick({ agentId: id, title: handle !== '' ? handle : id })}
                    >
                      <Icon name="terminal" size={13} />
                      <span className="ps-pick-handle">{handle !== '' ? handle : id}</span>
                      <code className="muted small">{kind}</code>
                      <span className="muted small">{str(a, 'pane_id')}</span>
                      {!covered && <span className="muted small">{t('panestate.noManifest')}</span>}
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}
