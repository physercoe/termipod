import { useT } from '../i18n';
import { useAgentNavigate } from '../state/agentNavigate';
import { uiRefLabel } from '../state/uiRef';
import { Icon } from './Icon';

/// Agent navigation, the visible half (coworking lane H — `desktop_open`).
///
/// The counterweight to the one capability in the desktop-UI set that moves the
/// user's screen for them. Three rules, and each is why this is a banner rather
/// than a toast:
///
///   - **It names the agent.** A tab that changes by itself reads as a bug; a
///     tab that changes with "kimi-1 opened …" on it reads as the thing the
///     user asked for. Attribution is the difference between a collaborator and
///     a glitch.
///   - **It does not expire.** `toast` auto-clears, and an undo that vanishes
///     while you are still working out what happened is an undo for whoever
///     happened to be watching. This stays until dismissed or used.
///   - **Undo comes before dismiss**, in that reading order, because the
///     interesting affordance for a jump you did not ask for is getting back.
///
/// Sits in the same tier as AgentHighlightOverlay — below the modal/menu layer,
/// so an agent can never cover the Attention dock or an approval dialog.
export function AgentNavigateBanner(): JSX.Element | null {
  const t = useT();
  const banner = useAgentNavigate((s) => s.banner);
  const dismiss = useAgentNavigate((s) => s.dismiss);
  const undo = useAgentNavigate((s) => s.undo);
  if (banner === null) return null;
  return (
    <div className="agent-navigate" role="status">
      <span className="agent-navigate-who">
        {/* Function replacement: `by` is agent-influenced, and a literal `$&`
            in a handle must render as itself, not as a replace() pattern. */}
        <Icon name="chevron-right" size={12} /> {t('navigate.opened').replace('{agent}', () => banner.by)}
      </span>
      <span className="agent-navigate-ref">{uiRefLabel(banner.ref)}</span>
      {banner.note !== '' && <span className="agent-navigate-note">{banner.note}</span>}
      <span className="spacer" />
      <button type="button" className="link-btn" onClick={undo}>
        {t('navigate.undo')}
      </button>
      <button type="button" className="link-btn" onClick={dismiss} aria-label={t('att.dismiss')}>
        <Icon name="close" size={12} />
      </button>
    </div>
  );
}
