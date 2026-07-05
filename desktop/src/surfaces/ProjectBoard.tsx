import { useQuery } from '@tanstack/react-query';
import { str, type Entity } from '../hub/types';
import { useSession } from '../state/session';

const COLUMNS: { status: string; label: string }[] = [
  { status: 'todo', label: 'To do' },
  { status: 'in_progress', label: 'In progress' },
  { status: 'blocked', label: 'Blocked' },
  { status: 'done', label: 'Done' },
  { status: 'cancelled', label: 'Cancelled' },
];

/// Focus region for a selected project (WS6, first slice): a tasks kanban
/// (ADR-029 statuses). Overview / runs / plans / deliverables panes follow.
export function ProjectBoard({ projectId }: { projectId: string }): JSX.Element {
  const client = useSession((s) => s.client);
  const tasksQ = useQuery({
    queryKey: ['tasks', projectId],
    enabled: client !== null,
    refetchInterval: 8000,
    queryFn: () => client!.listTasks(projectId),
  });

  if (tasksQ.isLoading) return <div className="region-pad muted">Loading tasks…</div>;
  if (tasksQ.isError) return <div className="region-pad error">{(tasksQ.error as Error).message}</div>;

  const tasks = tasksQ.data ?? [];
  const inColumn = (status: string): Entity[] =>
    tasks.filter((t) => (str(t, 'status') ?? 'todo') === status);

  return (
    <div className="scroll">
      <div className="kanban">
        {COLUMNS.map((col) => {
          const items = inColumn(col.status);
          return (
            <div key={col.status} className="kanban-col">
              <div className="kanban-head">
                {col.label} <span className="pill">{items.length}</span>
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
