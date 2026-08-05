import { useT } from '../i18n';
import type { ContextFill } from '../state/transcriptStats';

/// Context-window fill, drawn as a ring beside the composer (R2).
///
/// A ring rather than a number because the question it answers is "how much
/// room is left before the next response spills" — an at-a-glance quantity the
/// user checks WHILE typing, not a figure they read. The exact tokens stay in
/// the title, and the same numbers render as text in the transcript's status
/// strip for anyone who wants them.
///
/// Bands come from `contextFill` so this component decides nothing: it draws
/// what the fold measured. When the fold has no honest answer it returns
/// undefined and the caller renders nothing at all — an empty ring would read
/// as "0% full" rather than "not reported" (D-4).
///
/// Above the high band, and only where the engine has a compaction command the
/// hub recognizes, the ring becomes a button that drops that command into the
/// draft. It never sends: the same rule the slash picker and the annotation
/// crop follow — the client stages, the user commits.

const SIZE = 22;
const STROKE = 3;
const R = (SIZE - STROKE) / 2;
const CIRC = 2 * Math.PI * R;

export function ContextRing({
  fill,
  compactCommand,
  onCompact,
}: {
  fill: ContextFill;
  /// The engine's compaction command (`compactCommandFor`), when it has one.
  compactCommand?: string;
  /// Stage `compactCommand` in the composer draft. Required for the button
  /// form; without it the ring stays a plain indicator.
  onCompact?: (command: string) => void;
}): JSX.Element {
  const t = useT();
  const pctText = `${Math.round(fill.pct * 100)}%`;
  const detail = t('ctx.title')
    .replace('{used}', String(fill.used))
    .replace('{total}', String(fill.window))
    .replace('{pct}', pctText);
  const offerCompact = fill.band === 'high' && compactCommand !== undefined && onCompact !== undefined;
  const title = offerCompact ? `${detail}\n${t('ctx.compactHint').replace('{cmd}', compactCommand)}` : detail;

  const ring = (
    <>
      <svg className="ctx-ring-svg" width={SIZE} height={SIZE} viewBox={`0 0 ${SIZE} ${SIZE}`} aria-hidden="true">
        <circle className="ctx-ring-track" cx={SIZE / 2} cy={SIZE / 2} r={R} strokeWidth={STROKE} fill="none" />
        <circle
          className="ctx-ring-fill"
          cx={SIZE / 2}
          cy={SIZE / 2}
          r={R}
          strokeWidth={STROKE}
          fill="none"
          strokeDasharray={`${CIRC * fill.pct} ${CIRC}`}
          // Start the arc at 12 o'clock rather than 3 — a gauge that fills
          // from the top reads as a dial; from the right it reads as noise.
          transform={`rotate(-90 ${SIZE / 2} ${SIZE / 2})`}
        />
      </svg>
      <span className="ctx-ring-pct">{pctText}</span>
    </>
  );

  if (offerCompact) {
    return (
      <button
        className={`ctx-ring ctx-${fill.band} ctx-ring-btn`}
        title={title}
        aria-label={title}
        onClick={() => onCompact(compactCommand)}
      >
        {ring}
      </button>
    );
  }
  return (
    <span className={`ctx-ring ctx-${fill.band}`} title={title} aria-label={title}>
      {ring}
    </span>
  );
}
