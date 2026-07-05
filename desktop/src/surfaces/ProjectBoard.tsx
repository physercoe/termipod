import { useQuery } from '@tanstack/react-query';
import { str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';

const COLUMNS = ['todo', 'in_progress', 'blocked', 'done', 'cancelled'];

/// Focus region for a selected project (WS6, first slice): a tasks kanban
/// (ADR-029 statuses). Overview / runs / plans / deliverables panes follow.
export function ProjectBoard({ projectId }: { projectId: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const tasksQ = useQuery({
    queryKey: ['tasks', projectId],
    enabled: client !== null,
    refetchInterval: 8000,
    queryFn: () => client!.listTasks(projectId),
  });

  if (tasksQ.isLoading) return <div className="region-pad muted">{t('kanban.loading')}</div>;
  if (tasksQ.isError) return <div className="region-pad error">{(tasksQ.error as Error).message}</div>;

  const tasks = tasksQ.data ?? [];
  const inColumn = (status: string): Entity[] =>
    tasks.filter((t) => (str(t, 'status') ?? 'todo') === status);

  return (
    <div className="scroll">
      <div className="kanban">
        {COLUMNS.map((status) => {
          const items = inColumn(status);
          return (
            <div key={status} className="kanban-col">
              <div className="kanban-head">
                {t(`kanban.${status}`)} <span className="pill">{items.length}</span>
              </div>
              {items.map((t) => (
                <div key={str(t, 'id')} className="kanban-card">
                  <div className="kanban-card-title">
                    {str(t, 'title') ?? str(t, 'summary') ?? str(t, 'id')}
                  </div>
                  {str(t, 'assignee_handle') !== undefined && (
                    <div className="kanban-card-meta">{str(t, 'assignee_handle')}</div>
                  )}
                </div>
              ))}
              {items.length === 0 && <div className="kanban-empty">—</div>}
            </div>
          );
        })}
      </div>
    </div>
  );
}
