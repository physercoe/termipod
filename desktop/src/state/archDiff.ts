/// Pure architecture-card diff (archgraph plan §5 W5, decision D-5). Compares two
/// classified `ArchCard`s (+ their raw configs, for the KV-cache class) into a
/// row-wise table with changed-row marking, plus the chip add/remove sets. The
/// "compare against…" picker and the side-by-side render live in the Inspect
/// model view (W5b) — this is the state half, unit-tested without React, so the
/// diff semantics are pinned independently of the UI.
import { humanCount, TEMPLATE_LABEL, type ArchCard } from './checkpoint.ts';
import { deriveKvCacheClass } from './vram.ts';

/// One comparison row. `a` / `b` are the display strings for each side ('' when
/// that side has no value for the row); `changed` is true when they differ.
export interface ArchDiffRow {
  /// Stable key for the renderer (also the fallback English label).
  key: string;
  label: string;
  a: string;
  b: string;
  changed: boolean;
}

export interface ArchDiff {
  rows: ArchDiffRow[];
  /// Chips present on B but not A (added), and on A but not B (removed).
  chipsAdded: string[];
  chipsRemoved: string[];
  /// Convenience: any row changed, or any chip added/removed.
  anyChange: boolean;
}

/// A card paired with the raw config it was classified from (the config is what
/// the KV-cache class derives from; omit it and the KV row is simply skipped).
export interface ArchDiffSide {
  card: ArchCard;
  config?: Record<string, unknown> | null;
  metadata?: Record<string, string | number>;
}

function numStr(v: number | undefined): string {
  return v === undefined ? '' : String(v);
}

function kvClassOf(side: ArchDiffSide): string {
  const kv = deriveKvCacheClass({ card: side.card, config: side.config, metadata: side.metadata });
  return kv === null ? '' : kv.cls;
}

// Row spec: a stable key, a human label, and how to read each side's value.
const ROW_SPECS: Array<{ key: string; label: string; read: (s: ArchDiffSide) => string }> = [
  { key: 'family', label: 'Family', read: (s) => s.card.family },
  { key: 'template', label: 'Template', read: (s) => TEMPLATE_LABEL[s.card.template] },
  { key: 'linearKind', label: 'Linear attention', read: (s) => s.card.linearKind ?? '' },
  { key: 'layers', label: 'Layers', read: (s) => numStr(s.card.layers) },
  { key: 'fullAttnLayers', label: 'Full-attention layers', read: (s) => numStr(s.card.fullAttnLayers) },
  { key: 'hidden', label: 'Hidden size', read: (s) => (s.card.hidden === undefined ? '' : humanCount(s.card.hidden)) },
  { key: 'heads', label: 'Attention heads', read: (s) => numStr(s.card.heads) },
  { key: 'kvHeads', label: 'KV heads', read: (s) => numStr(s.card.kvHeads) },
  { key: 'context', label: 'Max context', read: (s) => (s.card.context === undefined ? '' : humanCount(s.card.context)) },
  { key: 'vocab', label: 'Vocab', read: (s) => (s.card.vocab === undefined ? '' : humanCount(s.card.vocab)) },
  { key: 'experts', label: 'Experts', read: (s) => numStr(s.card.experts) },
  { key: 'expertsPerTok', label: 'Active experts / token', read: (s) => numStr(s.card.expertsPerTok) },
  { key: 'sharedExperts', label: 'Shared experts', read: (s) => numStr(s.card.sharedExperts) },
  { key: 'kvClass', label: 'KV cache / token', read: kvClassOf },
];

/// Diff two architecture cards. Rows where BOTH sides are empty are dropped;
/// every remaining row is marked `changed` when the two sides differ. Chips are
/// compared as sets (order-independent) into added/removed lists. Pure.
export function diffArchCards(a: ArchDiffSide, b: ArchDiffSide): ArchDiff {
  const rows: ArchDiffRow[] = [];
  for (const spec of ROW_SPECS) {
    const av = spec.read(a);
    const bv = spec.read(b);
    if (av === '' && bv === '') continue;
    rows.push({ key: spec.key, label: spec.label, a: av, b: bv, changed: av !== bv });
  }
  const aChips = new Set(a.card.chips);
  const bChips = new Set(b.card.chips);
  const chipsAdded = b.card.chips.filter((c) => !aChips.has(c));
  const chipsRemoved = a.card.chips.filter((c) => !bChips.has(c));
  const anyChange = rows.some((r) => r.changed) || chipsAdded.length > 0 || chipsRemoved.length > 0;
  return { rows, chipsAdded, chipsRemoved, anyChange };
}
