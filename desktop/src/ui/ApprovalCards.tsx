import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { obj, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useAgentSource } from '../state/useAgentSource';
import { useWorkbench } from '../state/workbench';
import { Icon } from './Icon';
import {
  approvalWire,
  inlineDecidable,
  parseApprovalRequest,
  parseAttentionRequest,
  type ApprovalOption,
  type CompactionSpec,
  type PermissionSpec,
  type QuestionSpec,
} from './approvalRequest';

/// The two full interactive cards in the Companion feed (vision-parity R1,
/// plan D-3). Everything else in the feed is a row, a group or a footer;
/// questions and approvals get a card because they are the only events the
/// agent is *blocked on* — the user's answer is the thing that unblocks it.
///
/// Before this, `approval_request` and `attention_request` had no case in
/// EventCard at all and fell to the generic `<details>` payload dump. That was
/// worse than cosmetic: `toolGroups.ts` HIDES the gate `tool_call` on the
/// stated assumption that an inline card exists to replace it, so the desktop
/// transcript showed neither the gate nor a way to answer it, and the agent
/// sat blocked until it timed out.
///
/// Classification and wire rules live in the pure `approvalRequest.ts` (which
/// `node --test` covers); this file is the rendering and the POST.

function msg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/// Shared shell: a heading strip, the body, an option row, and the settled
/// state once the user has answered. `onPick` returns a promise; the card
/// disables itself while it is in flight and keeps the chosen label afterwards
/// so the transcript still reads as a record of what was decided.
function CardShell({
  kindLabel,
  children,
  options,
  onPick,
  disabledNote,
}: {
  kindLabel: string;
  children?: React.ReactNode;
  options: ApprovalOption[];
  onPick?: (o: ApprovalOption) => Promise<void>;
  /// Why there are no buttons, when there are none to give.
  disabledNote?: string;
}): JSX.Element {
  const t = useT();
  const [busy, setBusy] = useState(false);
  const [picked, setPicked] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function pick(o: ApprovalOption): Promise<void> {
    if (onPick === undefined || busy) return;
    setBusy(true);
    setError(null);
    try {
      await onPick(o);
      setPicked(o.label);
    } catch (e) {
      // Surface it: a silently-failed approval leaves the agent blocked and
      // the user believing they answered.
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="ev-approval">
      <div className="ev-approval-head">
        <Icon name="lock" size={13} />
        <span className="ev-approval-kind">{kindLabel}</span>
      </div>
      {children}
      {picked !== null ? (
        <div className="ev-approval-settled">
          <Icon name="check" size={13} /> {t('approval.answered').replace('{choice}', picked)}
        </div>
      ) : onPick !== undefined ? (
        <div className="ev-approval-opts">
          {options.map((o) => (
            <button
              key={o.id}
              type="button"
              className="import-btn"
              disabled={busy}
              title={o.description}
              onClick={() => void pick(o)}
            >
              {o.label}
            </button>
          ))}
        </div>
      ) : null}
      {disabledNote !== undefined && picked === null && (
        <div className="ev-approval-note muted small">{disabledNote}</div>
      )}
      {error !== null && <div className="ev-approval-err">{t('approval.failed').replace('{err}', error)}</div>}
    </div>
  );
}

function PermissionCard({ spec, agentId }: { spec: PermissionSpec; agentId?: string }): JSX.Element {
  const t = useT();
  const source = useAgentSource();
  const ready = source !== null && agentId !== undefined && agentId !== '';
  return (
    <CardShell
      kindLabel={t('approval.permission')}
      options={spec.options}
      onPick={
        ready
          ? async (o) => {
              const wire = approvalWire(spec, o);
              await source.approve(agentId, spec.requestId, wire.decision, wire.optionId);
            }
          : undefined
      }
      // A read-only mount (a replayed transcript, no bound agent) still shows
      // WHAT was asked — it just can't answer it.
      disabledNote={ready ? undefined : t('approval.readOnly')}
    >
      {spec.toolSummary !== undefined && (
        <div className="ev-approval-subject">
          <span className="muted small">{t('approval.tool')}</span> <code>{spec.toolSummary}</code>
        </div>
      )}
    </CardShell>
  );
}

function QuestionCard({ spec, agentId }: { spec: QuestionSpec; agentId?: string }): JSX.Element {
  const t = useT();
  const source = useAgentSource();
  const ready = source !== null && agentId !== undefined && agentId !== '';
  return (
    <CardShell
      kindLabel={spec.header ?? t('approval.question')}
      options={spec.options}
      onPick={
        ready
          ? async (o) => {
              // The body is the option LABEL: the hub carved `answer` off from
              // `approval` precisely so the agent receives the option text
              // rather than "allow: Red".
              await source.answer(agentId, spec.requestId, o.label);
            }
          : undefined
      }
      disabledNote={
        !ready
          ? t('approval.readOnly')
          : spec.options.length === 0
            ? t('approval.noOptions')
            : undefined
      }
    >
      {spec.question !== '' && <div className="ev-approval-q">{spec.question}</div>}
      {spec.moreQuestions > 0 && (
        <div className="ev-approval-note muted small">
          {t('approval.moreQuestions').replace('{n}', String(spec.moreQuestions))}
        </div>
      )}
    </CardShell>
  );
}

function CompactionCard({ spec }: { spec: CompactionSpec }): JSX.Element {
  const t = useT();
  const setJob = useWorkbench((s) => s.setJob);
  return (
    <div className="ev-approval">
      <div className="ev-approval-head">
        <Icon name="lock" size={13} />
        <span className="ev-approval-kind">{t('approval.compaction')}</span>
      </div>
      <div className="ev-approval-q">{t('approval.compactionAsk')}</div>
      {spec.trigger !== undefined && (
        <div className="ev-approval-subject">
          <span className="muted small">{t('approval.trigger')}</span> <code>{spec.trigger}</code>
        </div>
      )}
      {spec.customInstructions !== undefined && (
        <div className="ev-approval-note muted small">{spec.customInstructions}</div>
      )}
      {/* No buttons by design: the PreCompact hook parks a real attention item
          and blocks on THAT, and this event carries no attention id — an inline
          button would resolve nothing. Send the user where the decision lives
          instead of implying one exists here (D-4). */}
      <div className="ev-approval-opts">
        <button type="button" className="import-btn" onClick={() => setJob('fleet')}>
          <Icon name="eye" size={13} /> {t('approval.openAttention')}
        </button>
      </div>
    </div>
  );
}

/// `approval_request` — the agent is blocked and the user's answer unblocks it.
/// An unrecognised payload returns null so EventCard falls through to its
/// generic dump: showing the bytes beats showing a card that misrepresents
/// them.
export function ApprovalRequestBody({ p, agentId }: { p: Entity; agentId?: string }): JSX.Element | null {
  const spec = parseApprovalRequest(p);
  switch (spec.form) {
    case 'permission':
      return <PermissionCard spec={spec} agentId={agentId} />;
    case 'question':
      return <QuestionCard spec={spec} agentId={agentId} />;
    case 'compaction':
      return <CompactionCard spec={spec} />;
    default:
      return null;
  }
}

/// The pending attention items this agent raised, rendered inline at the tail
/// of its own feed (R1). They belong here because `toolGroups.ts` hides the
/// gate `tool_call` that produced them — on the stated grounds that an inline
/// card already represents the gesture, which was true on mobile and false
/// here until now. Pending items are by definition the newest thing needing
/// the user, so the tail is their honest position.
///
/// The AttentionDock stays the cross-agent aggregator; this is the same row,
/// resolved through the same `POST /attention/{id}/decide`. Invalidating the
/// shared `['attention']` key is what keeps a decision made in one surface
/// from leaving a live button in the other.
///
/// The attention TABLE is a hub capability, so these cards render only when
/// the bound source has one (L1 / D-4). A local driver parks nothing: its
/// approvals arrive as `approval_request` events and are answered by the two
/// cards above (plan L4). Gating on the capability rather than on
/// `kind === 'hub'` is deliberate — any future source that keeps a queue gets
/// these rows without a second condition here.
export function InlineAttentionCards({
  items,
}: {
  items: readonly Entity[];
}): JSX.Element | null {
  const t = useT();
  const attention = useAgentSource()?.attention;
  const qc = useQueryClient();
  const setJob = useWorkbench((s) => s.setJob);
  if (items.length === 0 || attention === undefined) return null;
  return (
    <>
      {items.map((item) => {
        const id = str(item, 'id') ?? '';
        const kind = str(item, 'kind') ?? 'attention';
        const summary = str(item, 'summary') ?? '';
        const payload = obj(item, 'pending_payload');
        const toolName = payload === undefined ? undefined : str(payload, 'tool_name') ?? str(payload, 'tool');
        // `t` echoes the key on a miss, so compare rather than `||`-chain —
        // an undeclared kind must fall back to its own name, not to the
        // literal string "approval.attn.<kind>".
        const labelKey = `approval.attn.${kind}`;
        const translated = t(labelKey);
        const kindLabel = translated === labelKey ? kind : translated;
        const detail = (
          <>
            {summary !== '' && <div className="ev-approval-q">{summary}</div>}
            {toolName !== undefined && (
              <div className="ev-approval-subject">
                <span className="muted small">{t('approval.tool')}</span> <code>{toolName}</code>
              </div>
            )}
          </>
        );
        // A kind whose decision needs more than approve/reject (select's
        // option, help_request's reply body) gets the pointer, not a pair of
        // buttons that would resolve it wrong — see `inlineDecidable`. Same
        // shape as the compaction card: show the ask, send the user where the
        // decision actually lives (D-4).
        if (!inlineDecidable(kind)) {
          return (
            <div className="ev-approval" key={id}>
              <div className="ev-approval-head">
                <Icon name="lock" size={13} />
                <span className="ev-approval-kind">{kindLabel}</span>
              </div>
              {detail}
              <div className="ev-approval-opts">
                <button type="button" className="import-btn" onClick={() => setJob('fleet')}>
                  <Icon name="eye" size={13} /> {t('approval.openAttention')}
                </button>
              </div>
            </div>
          );
        }
        const approveLabel =
          kind === 'browser_action' || kind === 'desktop_action' ? t('att.allowOnce') : t('att.approve');
        return (
          <CardShell
            key={id}
            kindLabel={kindLabel}
            options={[
              { id: 'approve', label: approveLabel },
              { id: 'reject', label: t('att.reject') },
            ]}
            onPick={
              id !== ''
                ? async (o) => {
                    await attention.resolve(id, o.id);
                    await qc.invalidateQueries({ queryKey: ['attention'] });
                  }
                : undefined
            }
          >
            {detail}
          </CardShell>
        );
      })}
    </>
  );
}

/// `attention_request` — today always `auth_required` from the ACP driver.
/// Deliberately NOT interactive (D-4): the remediation is a command on the
/// HOST (`gemini auth`, `kimi login`), so a button here would promise a fix
/// the desktop cannot perform. Show the reason and the exact remediation the
/// hub already computed.
export function AttentionRequestBody({ p }: { p: Entity }): JSX.Element {
  const t = useT();
  const qc = useQueryClient();
  const spec = parseAttentionRequest(p);
  return (
    <div className="ev-approval">
      <div className="ev-approval-head">
        <Icon name="alert" size={13} />
        <span className="ev-approval-kind">{t(`approval.attn.${spec.kind}`) || spec.kind}</span>
      </div>
      {spec.reason !== undefined && <div className="ev-approval-q">{spec.reason}</div>}
      {spec.methods.length > 0 && (
        <div className="ev-approval-subject">
          <span className="muted small">{t('approval.methods')}</span>{' '}
          {spec.methods.map((m) => (
            <code key={m.id}>{m.label}</code>
          ))}
        </div>
      )}
      {spec.remediation !== undefined && (
        <div className="ev-approval-note">
          <span className="muted small">{t('approval.remediation')}</span>
          <pre className="ev-mono">{spec.remediation}</pre>
        </div>
      )}
      <div className="ev-approval-opts">
        <button
          type="button"
          className="import-btn"
          onClick={() => void qc.invalidateQueries({ queryKey: ['agents'] })}
        >
          <Icon name="refresh" size={13} /> {t('approval.recheck')}
        </button>
      </div>
    </div>
  );
}
