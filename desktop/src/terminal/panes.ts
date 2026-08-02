/// Which sessions the Terminal *surface* tiles (#319 split panes).
///
/// Extracted from TerminalPanel because this is arithmetic, not rendering, and
/// it decides something invisible-until-wrong: in surface mode the pane set —
/// NOT `activeId` — is what actually renders. A session can be the active tab,
/// own the tab-bar highlight, and still be on no screen at all, which is how the
/// reconnect bug hid (see reconcilePanes rule 3).

/// Recompute the tiled set after the tab list or the active tab changed.
///
/// Three rules, in order:
///
///  1. **Prune.** Drop ids whose tab is gone — a closed session must not hold a
///     slot.
///  2. **Seed.** If nothing survives, tile the active tab, so the surface is
///     never blank while tabs are open.
///  3. **Adopt.** If the active tab is tiled nowhere, it takes the surface. This
///     is the rule that was missing. `addTab` makes a new session active but
///     never tiles it, and rule 1 returns the previous set untouched whenever it
///     still holds one live id — so opening a session while ANY other tab was
///     tiled left the new one invisible behind the old pane. Replacing (rather
///     than joining) matches what clicking a tab already does; a split re-adds
///     its own new pane in the same batch, so it never reaches this rule.
///
/// Returns `prev` by identity when nothing changed, so a chatty tab list does
/// not churn the pane state.
///
/// Every rule that tiles `activeId` first checks that it names a real tab. The
/// store only ever sets it to one, but the two pieces of state land in separate
/// updates and a prune can be a render ahead — and an unguarded rule 3 would
/// then answer a stale activeId by evicting the live pane and tiling a tab that
/// no longer exists, i.e. a blank surface with sessions open.
export function reconcilePanes(prev: readonly string[], tabIds: readonly string[], activeId: string | null): string[] {
  const active = activeId !== null && tabIds.includes(activeId) ? activeId : null;
  const live = prev.filter((id) => tabIds.includes(id));
  if (live.length === 0) return active !== null ? [active] : [];
  if (active !== null && !live.includes(active)) return [active];
  return live.length === prev.length ? (prev as string[]) : live;
}
