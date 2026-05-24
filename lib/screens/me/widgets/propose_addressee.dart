/// ADR-030 W19.5 — variant-selector helpers for per-kind propose
/// cards (W15-W18) and the steward-side inbox (W19).
///
/// Shared between [MeScreen] (the principal's Me-tab) and the per-kind
/// propose card widgets so the primary-vs-stalled decoration uses a
/// single predicate. Kept as top-level functions (no class) so the
/// callers don't have to instantiate anything; the data flows in via
/// the raw attention map shape that hub_client returns.
library;

/// Returns `true` when the viewer's tier matches the row's
/// `assigned_tier` — render the **primary** variant (Approve / Reject
/// buttons, no top pill).
///
/// Returns `false` for stalled propose rows that surfaced via the
/// escalation walk (the viewer is not the addressee but
/// `escalation_state` has surfaced the row to their tier) — render
/// the **stalled** variant (top pill, Override / View source buttons).
///
/// `myTier` is one of `worker | project-steward | general-steward |
/// principal` per ADR-030's 4-tier ladder. In MVP the only mobile
/// viewer is the principal, but the function accepts the tier as a
/// parameter so the W19 steward-side inbox can reuse this exact
/// predicate without duplication.
///
/// Edge cases:
/// - empty `assigned_tier` (legacy or pre-ADR-030 row) → `true`
///   (primary variant; the legacy fallback)
/// - `assigned_tier` set but `myTier` empty → `false` (the viewer
///   can't be the addressee of anything if they have no tier)
bool isAddresseeOfPropose(Map<String, dynamic> attention, String myTier) {
  final assignedTier = (attention['assigned_tier'] ?? '').toString();
  if (assignedTier.isEmpty) return true;
  if (myTier.isEmpty) return false;
  return assignedTier == myTier;
}

/// Returns `true` when the propose row has walked past the addressee's
/// inactivity_deadline and the sweep has surfaced it to a higher tier.
/// `escalation_state` is one of `none | escalated_steward |
/// escalated_principal` (migration 0042 CHECK).
///
/// The top digest card (W19.6 mobile half) counts these; the per-kind
/// card decorates them with a top pill (`⏱ Stuck Nh — addressed to
/// @<addressee>`).
bool isStalledPropose(Map<String, dynamic> attention) {
  final esc = (attention['escalation_state'] ?? 'none').toString();
  return esc != 'none' && esc != '';
}

/// Returns the human-readable label for the row's [escalation_state]
/// — `Stuck` when escalated, empty otherwise. Suitable for the top
/// pill on a stalled per-kind card.
///
/// The duration is computed separately from `escalated_at` or the
/// latest `attention.escalation_advanced` audit row; this helper
/// just owns the lead noun.
String stalledPillLabel(Map<String, dynamic> attention) {
  return isStalledPropose(attention) ? 'Stuck' : '';
}
