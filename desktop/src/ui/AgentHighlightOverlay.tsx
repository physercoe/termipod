import { useEffect } from 'react';
import { useT } from '../i18n';
import { toast } from '../state/toast';
import { useAgentHighlight, type HighlightOrder } from '../state/agentHighlight';
import { canFocusUiRef, focusUiRef } from '../state/uiRefFocus';
import { uiRefLabel } from '../state/uiRef';
import { Icon } from './Icon';

/// Agent pointing, the visible half (D6 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.4b, ADR-062 D-5).
///
/// An agent said "look here". This paints an attributed marker that expires —
/// and does NOTHING else. It is deliberately the weakest thing in the plan:
///
///   - **Non-actuating.** Nothing here focuses, scrolls, clicks or types. The
///     marker offers the user a "Go there" button, and that button is the only
///     path to `focusUiRef` — the agent directs attention, the user actuates.
///   - **Attributed, always.** Every marker names the agent, so nothing an
///     agent draws can be mistaken for the app talking (the fake-UI risk).
///   - **Never over the consent UI.** It sits BELOW the modal/menu tier, so it
///     can never cover the Attention dock or an approval dialog — an agent
///     must not be able to obscure the thing that governs it.
///   - **Dismissible and self-expiring.** The TTL is main's, not the agent's.
///
/// Mounted once at the app shell, next to AnnotationOverlay (the user's half
/// of the same symmetry).
function HighlightCard({ order }: { order: HighlightOrder }): JSX.Element {
  const t = useT();
  const dismiss = useAgentHighlight((s) => s.dismiss);
  const focusable = canFocusUiRef(order.ref);

  useEffect(() => {
    const timer = setTimeout(() => dismiss(order.id), order.ttl_ms);
    return () => clearTimeout(timer);
  }, [order.id, order.ttl_ms, dismiss]);

  return (
    <div className="agent-highlight" role="status">
      <span className="agent-highlight-who">
        {/* Function replacement: `by` is agent-influenced, and a literal `$&`
            in a handle must render as itself, not as a replace() pattern. */}
        <Icon name="crosshair" size={12} /> {t('highlight.points').replace('{agent}', () => order.by)}
      </span>
      <span className="agent-highlight-ref">{uiRefLabel(order.ref)}</span>
      {order.note !== '' && <span className="agent-highlight-note">{order.note}</span>}
      {focusable && (
        <button
          type="button"
          className="link-btn"
          onClick={() => {
            if (focusUiRef(order.ref) === 'surface' && Object.keys(order.ref.params).length > 0) {
              toast.info(t('uiref.surfaceOnly'));
            }
          }}
        >
          {t('highlight.goThere')}
        </button>
      )}
      <button type="button" className="link-btn" onClick={() => dismiss(order.id)} aria-label={t('att.dismiss')}>
        <Icon name="close" size={12} />
      </button>
    </div>
  );
}

export function AgentHighlightOverlay(): JSX.Element | null {
  const orders = useAgentHighlight((s) => s.orders);
  if (orders.length === 0) return null;
  return (
    <div className="agent-highlight-layer">
      {orders.map((order) => (
        <HighlightCard key={order.id} order={order} />
      ))}
    </div>
  );
}
