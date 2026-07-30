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

/// What the target row offers, in display order: **"Attach to kimi web"
/// first when the kimi panel is open**, then the companion's "Send to
/// <agent>" row; both null/false → the `annotate.noTarget` hint (plan §3.4
/// step 4).
export interface AnnotationTargets {
  kimi: boolean;
  companion: CompanionTarget | null;
}

/// Resolve the target row for the current arm.
///
/// - A companion arm offers ONLY that companion, and only when bound (D2
///   semantics, unchanged): one companion's gesture never leaks into another
///   mount's compose box.
/// - A global arm (no storageKey — D2.1) offers the first registered bound
///   companion; registration order is mount order, so the longest-lived bound
///   companion wins. Unbound registrations are skipped.
export function resolveTargets(opts: {
  kimiOpen: boolean;
  origin: AnnotationOrigin | null;
  companions: readonly CompanionTarget[];
}): AnnotationTargets {
  const { kimiOpen, origin, companions } = opts;
  if (origin !== null && origin.storageKey !== undefined) {
    const bound = (origin.agentId ?? '') !== '';
    return {
      kimi: kimiOpen,
      companion: bound
        ? {
            storageKey: origin.storageKey,
            agentId: origin.agentId ?? '',
            agentLabel: origin.agentLabel ?? '',
          }
        : null,
    };
  }
  return { kimi: kimiOpen, companion: companions.find((c) => c.agentId !== '') ?? null };
}

/// Registry reducers (the store keeps the array; these keep it honest).
/// `upsertCompanion` replaces IN PLACE by storageKey — a companion
/// re-registers when its binding or the agents list changes, so the label
/// stays fresh without duplicates and without disturbing mount order (which
/// is what `resolveTargets`' first-bound pick reads). `removeCompanion` is
/// the unmount / unbind path.
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
