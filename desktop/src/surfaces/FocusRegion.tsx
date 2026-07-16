import { useT } from '../i18n';
import { useFocus } from '../state/focus';
import { AgentTranscript } from './AgentTranscript';
import { AuditConsole } from './AuditConsole';
import { HostBoard } from './HostBoard';
import { ProjectBoard } from './ProjectBoard';

/// The centre (Focus) region, shared by the Fleet and Projects tabs. What it
/// shows is driven by the global `useFocus` selection — an agent transcript, a
/// project board, a host board, or (nothing selected) the activity console — so
/// a drill-down from either tab's left nav lands in the same place.
export function FocusRegion(): JSX.Element {
  const t = useT();
  const selection = useFocus((s) => s.selection);
  return (
    <div className="region focus">
      <div className="region-header">
        {selection?.type === 'agent'
          ? `${t('region.agent')} · ${selection.id}`
          : selection?.type === 'project'
            ? `${t('region.project')} · ${selection.id}`
            : selection?.type === 'host'
              ? `${t('region.host')} · ${selection.id}`
              : t('region.activity')}
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
