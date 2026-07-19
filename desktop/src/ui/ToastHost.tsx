import { useToasts } from '../state/toast';
import { Icon } from './Icon';

/// Renders the toast stack once at the app root. The region is an `aria-live`
/// polite announcer (errors assertive) so screen readers hear transient results
/// too (#315/#316). Click a toast (or its ✕) to dismiss early.
export function ToastHost(): JSX.Element {
  const toasts = useToasts((s) => s.toasts);
  const remove = useToasts((s) => s.remove);
  return (
    <div className="toast-host" aria-live="polite" aria-relevant="additions">
      {toasts.map((t) => (
        <div
          key={t.id}
          className={`toast toast-${t.kind}`}
          role={t.kind === 'error' ? 'alert' : 'status'}
          onClick={() => remove(t.id)}
        >
          <span className="toast-msg">{t.message}</span>
          <button
            className="toast-close"
            aria-label="Dismiss"
            onClick={(e) => {
              e.stopPropagation();
              remove(t.id);
            }}
          >
            <Icon name="close" size={13} />
          </button>
        </div>
      ))}
    </div>
  );
}
