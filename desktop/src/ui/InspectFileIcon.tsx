import { Icon } from './Icon';
import { inspectFileVisual } from './inspectFileVisual';

export function InspectFileIcon({ filename, size = 14 }: { filename: string; size?: number }): JSX.Element {
  const visual = inspectFileVisual(filename);
  return (
    <span className={`inspect-file-icon tone-${visual.tone}`} aria-hidden="true">
      <Icon name={visual.icon} size={size} />
    </span>
  );
}
