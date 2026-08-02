/// Target resolution for the annotation overlay (D2.1 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.4 step 2): pure logic
/// shared by the zustand store (annotation.ts) and the target row
/// (AnnotationOverlay.tsx), kept free of the shell bridge so `node --test`
/// can run the contract directly (the store itself imports the bridge, which
/// node ESM cannot resolve).

import { DOCK_COMPANION_KEY } from './companionBinding.ts';

/// The overlay's lifecycle. The store (annotation.ts) holds the state; the
/// reducers + decisions below keep it honest.
export type AnnotationPhase = 'idle' | 'selecting' | 'target';

/// The annotating-hide flag the assistant dock reads (D2.2): while the overlay
/// is active (selecting OR target) the dock steps aside via a CSS hide class —
/// WITHOUT its `open` state flipping — so it stays out of the captured pixels
/// and the drag gets an unobstructed surface. Idle (cancel / discard / target
/// pick) restores the dock exactly as it was.
export function dockHiddenForPhase(phase: AnnotationPhase): boolean {
  return phase !== 'idle';
}

/// Who armed the overlay. A COMPANION arm carries that mount's `storageKey`
/// so the handoff routes back to it, plus its bound agent — `agentId` ''
/// means the companion is unbound: the kimi-web path stays reachable, the
/// companion target doesn't (plan §3.4 step 2). A GLOBAL arm (D2.1 — the
/// status-bar chip / command palette, `GLOBAL_ORIGIN`) has NO storageKey: no
/// companion mount is implied, and the target row offers any bound companion
/// from the registry (`resolveTargets`).
export interface AnnotationOrigin {
  storageKey?: string;
  agentId?: string;
  agentLabel?: string;
}

/// The D2.1 global arm — no companion mount scopes the handoff. Arming with
/// this origin is how the status-bar chip and the palette entry start the
/// gesture from anywhere (e.g. while chatting in the embedded kimi panel).
export const GLOBAL_ORIGIN: AnnotationOrigin = {};

/// A mounted AgentCompanion that can take a handoff, as reported by the
/// companion itself (annotation.ts's registry). The label is pre-resolved
/// (the companion owns the agents list) so the target row just renders it.
export interface CompanionTarget {
  storageKey: string;
  agentId: string;
  agentLabel: string;
}

/// One row in the target list. The kimi-web injection target and each bound
/// Companion mount are the same kind of thing — a place this crop can go — so
/// they are one union, not a boolean beside an object (vision-parity F2). The
/// previous `{kimi: boolean, companion: CompanionTarget | null}` could express
/// at most one companion and gave the two targets different shapes, so the
/// target row had to special-case each; N companion mounts, or a third target
/// kind, had nowhere to live.
export type AnnotationTarget = { kind: 'kimi' } | ({ kind: 'companion' } & CompanionTarget);

/// Resolve the target row for the current arm, **in display order**: the kimi
/// row leads when its panel is open (D2.2), then the companions. An empty list
/// is the `annotate.noTarget` hint (plan §3.4 step 4).
///
/// - A companion arm offers ONLY that companion, and only when bound (D2
///   semantics, unchanged): one companion's gesture never leaks into another
///   mount's compose box.
/// - A global arm (no storageKey — D2.1) offers every bound companion in
///   registration order, which is mount order — so the longest-lived bound
///   companion leads. Unbound registrations are skipped. Exactly one companion
///   mount is registered today (the dock's; the per-surface mounts are
///   retired), so this renders identically to the old first-bound pick.
export function resolveTargets(opts: {
  kimiOpen: boolean;
  origin: AnnotationOrigin | null;
  companions: readonly CompanionTarget[];
}): AnnotationTarget[] {
  const { kimiOpen, origin, companions } = opts;
  const rows: AnnotationTarget[] = kimiOpen ? [{ kind: 'kimi' }] : [];
  if (origin !== null && origin.storageKey !== undefined) {
    if ((origin.agentId ?? '') !== '') {
      rows.push({
        kind: 'companion',
        storageKey: origin.storageKey,
        agentId: origin.agentId ?? '',
        agentLabel: origin.agentLabel ?? '',
      });
    }
    return rows;
  }
  for (const c of companions) {
    if (c.agentId !== '') rows.push({ kind: 'companion', ...c });
  }
  return rows;
}

/// Registry reducers (the store keeps the array; these keep it honest).
/// `upsertCompanion` replaces IN PLACE by storageKey — a companion
/// re-registers when its binding or the agents list changes, so the label
/// stays fresh without duplicates and without disturbing mount order — which
/// is the order `resolveTargets` renders the companion rows in.
/// `removeCompanion` is the unmount / unbind path.
export function upsertCompanion(list: readonly CompanionTarget[], c: CompanionTarget): CompanionTarget[] {
  const i = list.findIndex((x) => x.storageKey === c.storageKey);
  if (i === -1) return [...list, c];
  const next = list.slice();
  next[i] = c;
  return next;
}

export function removeCompanion(list: readonly CompanionTarget[], storageKey: string): CompanionTarget[] {
  return list.filter((x) => x.storageKey !== storageKey);
}

/// Where a "Send to <agent>" handoff routes: the explicitly passed key (a
/// global arm's resolved companion) else the arming origin's mount. null = no
/// route — the store no-ops.
export function handoffKey(origin: AnnotationOrigin | null, storageKey?: string): string | null {
  return storageKey ?? origin?.storageKey ?? null;
}

/// Whether a handoff to `key` should reveal the assistant dock on its
/// Companion tab (D2.2): true only for the dock companion — the one companion
/// mount now that the per-surface mounts are retired. The reveal makes the
/// user watch the crop chip land in the compose box.
export function handoffRevealsDock(key: string): boolean {
  return key === DOCK_COMPANION_KEY;
}
