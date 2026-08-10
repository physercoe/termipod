import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Icon } from '../ui/Icon';
import {
  matcherSummary,
  orderedRules,
  readPaneExplain,
  readPaneExplainError,
  type PaneExplainView,
  type PaneRule,
  type PaneState,
} from '../state/paneExplain';
import { useInspect, type InspectTab } from '../state/inspect';

/// The pane-state rule debugger (pane-state-manifests P4).
///
/// It answers one question — *why does the fleet think this agent is
/// idle / working / blocked* — and the answer is only useful if it shows the
/// rules that did NOT fire alongside the one that did. A card that printed
/// just the verdict would repeat the claim the status pill already makes, with
/// more pixels.
///
/// Two modes, and the card never blurs them: `live` captured a real pane a
/// moment ago; `supplied` evaluated text someone pasted. Presenting a
/// hypothetical as a fact about a running agent is the one error this surface
/// must not make, so the mode is stated in the header rather than inferred.
///
/// All parsing lives in `state/paneExplain.ts` and is unit-tested; this file is
/// layout only.

function stateTone(s: PaneState): string {
  switch (s) {
    case 'blocked':
      return 'ps-blocked';
    case 'working':
      return 'ps-working';
    case 'idle':
      return 'ps-idle';
    default:
      return 'ps-unknown';
  }
}

function RuleRow({ rule, winner }: { rule: PaneRule; winner: boolean }): JSX.Element {
  const t = useT();
  // The winner opens by default; the rest are one click away. A page that
  // expanded 14 rules would bury the answer it exists to give.
  const [open, setOpen] = useState(winner);
  const matchers = matcherSummary(rule.evidence);

  return (
    <div className={`ps-rule${rule.matched ? ' matched' : ''}${winner ? ' winner' : ''}`}>
      <button type="button" className="ps-rule-head" onClick={() => setOpen((v) => !v)} aria-expanded={open}>
        <Icon name={open ? 'minus' : 'plus'} size={11} />
        {/* The verdict is what a reader scans for, so it leads — and it is a
            glyph, not only a colour, so the distinction survives a theme. */}
        <span className={`ps-verdict${rule.matched ? ' hit' : ''}`}>
          <Icon name={rule.matched ? 'check' : 'close'} size={11} />
        </span>
        <code className="ps-rule-id">{rule.id}</code>
        <span className={`ps-chip ${stateTone(rule.state)}`}>{rule.state}</span>
        <span className="muted small">p{rule.priority}</span>
        {winner && <span className="ps-chip ps-win">{t('panestate.winner')}</span>}
        <span className="muted small ps-rule-region">{rule.region}</span>
      </button>
      {open && (
        <div className="ps-rule-body">
          <div className="ps-rule-line">
            <span className="muted small">{t('panestate.wanted')}</span>{' '}
            {matchers === '' ? (
              <span className="muted small">{t('panestate.nestedOnly')}</span>
            ) : (
              <code>{matchers}</code>
            )}
          </div>
          <div className="ps-rule-line muted small">
            {t('panestate.readFrom')
              .replace('{region}', rule.region)
              .replace('{n}', String(rule.evidence.regionBytes))}
          </div>
          {/* Bounded host-side. This is what turns "did not match" from an
              assertion into something the reader can check. */}
          <pre className="ps-preview">{rule.evidence.regionPreview}</pre>
        </div>
      )}
    </div>
  );
}

function RecordView({ view, onRecheck }: { view: PaneExplainView; onRecheck: () => void }): JSX.Element {
  const t = useT();
  const rules = orderedRules(view);
  const matchedCount = rules.filter((r) => r.matched).length;

  return (
    <>
      <div className="ps-head">
        <span className={`ps-chip big ${stateTone(view.state)}`}>{view.state}</span>
        <span className={`ps-mode ${view.mode}`}>
          {view.mode === 'live' ? t('panestate.modeLive') : t('panestate.modeSupplied')}
        </span>
        <button type="button" className="link-btn small" onClick={onRecheck}>
          <Icon name="refresh" size={12} /> {t('panestate.recheck')}
        </button>
      </div>

      <dl className="ps-facts">
        <dt>{t('panestate.family')}</dt>
        <dd>
          <code>{view.family}</code>
          {' → '}
          <code>{view.manifestId}</code>{' '}
          <span className="muted small">
            v{view.manifestVersion} · {view.source}
          </span>
        </dd>
        {view.mode === 'live' && (
          <>
            <dt>{t('panestate.pane')}</dt>
            <dd>
              <code>{view.paneId}</code> <span className="muted small">{view.hostId}</span>
            </dd>
          </>
        )}
        <dt>{t('panestate.screen')}</dt>
        <dd className="muted small">
          {t('panestate.screenSize')
            .replace('{lines}', String(view.screenLines))
            .replace('{bytes}', String(view.screenBytes))}
        </dd>
        <dt>{t('panestate.oscTitle')}</dt>
        <dd>
          {view.oscTitle === '' ? (
            // Not cosmetic: an empty title means every `osc_title` rule read
            // nothing and could not have fired. Saying so saves a reader from
            // concluding those rules are broken.
            <span className="muted small">{t('panestate.oscEmpty')}</span>
          ) : (
            <code>{view.oscTitle}</code>
          )}
        </dd>
        <dt>{t('panestate.verdict')}</dt>
        <dd>
          {view.matchedRuleId !== '' ? (
            <code>{view.matchedRuleId}</code>
          ) : (
            <span className="muted small">
              {view.fallbackReason !== '' ? view.fallbackReason : t('panestate.noMatch')}
            </span>
          )}
        </dd>
      </dl>

      {(view.visibleBlocker || view.visibleIdle || view.visibleWorking || view.skipStateUpdate) && (
        <div className="ps-hints">
          {view.visibleBlocker && <span className="ps-chip ps-blocked">{t('panestate.visibleBlocker')}</span>}
          {view.visibleIdle && <span className="ps-chip ps-idle">{t('panestate.visibleIdle')}</span>}
          {view.visibleWorking && <span className="ps-chip ps-working">{t('panestate.visibleWorking')}</span>}
          {view.skipStateUpdate && (
            <span className="ps-chip ps-frozen">
              {t('panestate.frozen').replace('{rule}', view.skippedUpdateReason)}
            </span>
          )}
        </div>
      )}

      <h4 className="ps-rules-title">
        {t('panestate.rulesTitle')
          .replace('{matched}', String(matchedCount))
          .replace('{total}', String(rules.length))}
      </h4>
      <div className="ps-rules">
        {rules.map((r) => (
          <RuleRow key={r.id} rule={r} winner={r.id === view.matchedRuleId && view.matchedRuleId !== ''} />
        ))}
      </div>
    </>
  );
}

export function PaneStateCard({ tab }: { tab: InspectTab }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const setFamily = useInspect((s) => s.setFamily);
  /// The supplied mode's text is the tab's paste body — the same store slot
  /// every other pasted tab uses, rather than a second place a screen can
  /// live. Live mode ignores it: the host reads the pane.
  const screen = useInspect((s) => s.content[tab.id]) ?? '';

  const live = tab.agentId !== undefined && tab.agentId !== '';
  const family = tab.family ?? '';
  const ready = live || (family !== '' && screen !== '');

  const q = useQuery({
    // `screen` is in the key so re-pasting re-evaluates: the hub call is
    // stateless, and edited text is simply a different question.
    queryKey: ['pane-explain', tab.id, tab.agentId ?? '', family, live ? '' : screen],
    enabled: client !== null && ready,
    // Never on a timer. This captures a live terminal, and a debugger that
    // re-read a pane every few seconds unasked would be a much more invasive
    // thing than one that answers when you press re-check.
    refetchOnWindowFocus: false,
    staleTime: Infinity,
    queryFn: () =>
      live ? client!.explainPane({ agentId: tab.agentId! }) : client!.explainPane({ family, screen }),
  });

  // Supplied mode needs a family before it can ask anything: manifests are
  // per-engine, and guessing one would answer confidently with another
  // engine's rules — the exact failure D-3 exists to prevent.
  const coverageQ = useQuery({
    queryKey: ['pane-coverage'],
    enabled: client !== null && !live,
    staleTime: 5 * 60_000,
    queryFn: () => client!.listPaneCoverage(),
  });

  if (client === null) {
    return <div className="region-pad muted">{t('panestate.noHub')}</div>;
  }

  const picker = live ? null : (
    <div className="ps-picker">
      <label className="small muted" htmlFor={`ps-fam-${tab.id}`}>
        {t('panestate.family')}
      </label>
      <select id={`ps-fam-${tab.id}`} value={family} onChange={(e) => setFamily(tab.id, e.target.value)}>
        <option value="">{t('panestate.pickFamily')}</option>
        {(coverageQ.data ?? [])
          .map((f) => (typeof f['family'] === 'string' ? f['family'] : ''))
          .filter((f) => f !== '')
          .map((f) => (
            <option key={f} value={f}>
              {f}
            </option>
          ))}
      </select>
    </div>
  );

  function inner(): JSX.Element {
    if (!ready) {
      return <p className="muted small">{t('panestate.suppliedHint')}</p>;
    }
    if (q.isLoading) {
      return <p className="muted">{t('panestate.loading')}</p>;
    }
    if (q.isError) {
      return (
        <div>
          <p className="muted">{q.error instanceof Error ? q.error.message : String(q.error)}</p>
          <button type="button" className="import-btn" onClick={() => void q.refetch()}>
            <Icon name="refresh" size={13} /> {t('panestate.retry')}
          </button>
        </div>
      );
    }
    const refusal = readPaneExplainError(q.data);
    if (refusal !== null) {
      return (
        <p>
          {refusal.code === 'unmapped_family'
            ? t('panestate.unmapped').replace('{family}', refusal.family)
            : refusal.detail !== ''
              ? refusal.detail
              : refusal.code}
        </p>
      );
    }
    const view = readPaneExplain(q.data);
    if (view === null) {
      return <p className="muted">{t('panestate.noRecord')}</p>;
    }
    return <RecordView view={view} onRecheck={() => void q.refetch()} />;
  }

  return (
    <div className="ps-card region-pad">
      {picker}
      {inner()}
    </div>
  );
}
