import { useT } from '../i18n';
import { useFocus, type FocusScope, type Selection } from '../state/focus';
import { Icon } from '../ui/Icon';
import { AgentTranscript } from './AgentTranscript';
import { AuditConsole } from './AuditConsole';
import { HostBoard } from './HostBoard';
import { ProjectBoard } from './ProjectBoard';

/// The centre (Focus) region. What it shows is driven by its tab's `useFocus`
/// scope — an agent transcript, a project board, a host board, or (nothing
/// selected) the activity console — so a drill-down from the tab's left nav lands
/// here. The scope is per-tab (Fleet vs Projects) so the two tabs don't share one
/// selection; see `FocusScope`.
///
/// When a drill-down happened (e.g. opening an agent from a project board), the
/// header shows a **back** control that returns to the prior selection (the
/// project), so the transcript isn't a dead end.
export function FocusRegion({ scope }: { scope: FocusScope }): JSX.Element {
  const t = useT();
  const selection = useFocus((s) => s[scope].selection);
  const prev = useFocus((s) => s[scope].prev);
  const back = useFocus((s) => s.back);

  const kindLabel = (s: Selection): string =>
    s?.type === 'agent'
      ? t('region.agent')
      : s?.type === 'project'
        ? t('region.project')
        : s?.type === 'host'
          ? t('region.host')
          : t('region.activity');

  // Offer "back" only when there is a distinct prior selection to return to.
  const canBack =
    prev !== null && (prev.type !== selection?.type || prev.id !== (selection?.type ? selection.id : ''));

  return (
    <div className="region focus">
      <div className="region-header focus-header">
        {canBack && (
          <button
            className="focus-back"
            onClick={() => back(scope)}
            title={t('focus.backTo').replace('{what}', kindLabel(prev))}
          >
            <Icon name="chevron-left" size={13} />
            {kindLabel(prev)}
          </button>
        )}
        <span className="focus-header-title">
          {selection !== null ? `${kindLabel(selection)} · ${selection.id}` : t('region.activity')}
        </span>
      </div>
      {selection?.type === 'agent' ? (
        <AgentTranscript agentId={selection.id} />
      ) : selection?.type === 'project' ? (
        <ProjectBoard projectId={selection.id} />
      ) : selection?.type === 'host' ? (
        <HostBoard hostId={selection.id} />
      ) : (
        <AuditConsole />
      )}
    </div>
  );
}
