/// Element-resolved pointing — the shared vocabulary (D4 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.4 step 5, ADR-062 D-4).
///
/// When the user's annotation rect lands over a `<webview>` guest, the crop is
/// no longer the whole answer: the bridge already knows that region's
/// STRUCTURE, so the message can carry a pointer as well as pixels —
/// `{ tab_id, ref: "@e42", role: "button", name: "Deploy" }`. "Fix this
/// button" stops being ambiguous, and where the partition is action-drivable
/// the same ref is the argument to `browser_click`.
///
/// This module is the pure half: the type, and the two renderings of it (the
/// chip the user sees before sending, and the line the AGENT receives). It is
/// import-free so `node --test` runs it on both sides — the Electron resolver
/// (`electron/src/uipointer.ts`) imports it the same way `annotation.ts`
/// imports `ui_policy.ts`.

/// A structural pointer into one embedded browser tab. Every field is a
/// reference or a label — never content (ADR-062 D-2). `ref` and the AX
/// descriptors are each optional because resolution degrades honestly: a
/// non-interactive element mints no ref, and a page with no accessibility
/// tree yields no role.
export interface UiPointer {
  tab_id: number;
  /// The `@eN` handle from the tab's accessibility snapshot. Absent when the
  /// element under the rect is not interactive (only interactive nodes mint
  /// refs — see compactAxTree).
  ref?: string;
  role?: string;
  name?: string;
  /// Whether `browser_click { ref }` can actually act on this tab: only
  /// `full` partitions are action-drivable — kimiweb and rerunweb are
  /// read-only by policy (ADR-059 D-1), so promising actionability there
  /// would be a lie the agent discovers only on refusal.
  actionable: boolean;
}

/// The compact chip shown in the target row and the composer: `@e42 · button
/// · "Deploy"`. Missing pieces are dropped rather than rendered as blanks.
export function formatPointerLabel(p: UiPointer): string {
  const parts: string[] = [];
  if (p.ref !== undefined && p.ref !== '') parts.push(p.ref);
  if (p.role !== undefined && p.role !== '') parts.push(p.role);
  if (p.name !== undefined && p.name !== '') parts.push(`"${p.name}"`);
  return parts.length > 0 ? parts.join(' · ') : `tab ${String(p.tab_id)}`;
}

/// The line the AGENT reads. The image alone says "somewhere around here";
/// this says which element, and — only when it is true — how to act on it.
/// Written as a sentence rather than JSON because it rides in the message
/// body, which is where a text-mode agent will actually look.
export function formatPointerNote(p: UiPointer): string {
  const what = describeElement(p);
  const where = `browser tab ${String(p.tab_id)}`;
  if (p.ref === undefined || p.ref === '') {
    return `Pointing at ${what} in ${where}.`;
  }
  const how = p.actionable
    ? ` Act on it with browser_click { tabId: ${String(p.tab_id)}, ref: "${p.ref}" }.`
    : ' That tab is read-only for agents, so the ref is for reference, not for clicking.';
  return `Pointing at ${what} in ${where} — ref ${p.ref}.${how}`;
}

function describeElement(p: UiPointer): string {
  const role = p.role !== undefined && p.role !== '' ? p.role : 'element';
  return p.name !== undefined && p.name !== '' ? `the ${role} "${p.name}"` : `a ${role}`;
}

/// Fold the pointer into the user's note. The user's own words come first —
/// they are the message; the pointer is the grounding beneath it. An empty
/// note yields the pointer line alone, so "just point at this" still says
/// something useful.
export function appendPointerNote(note: string, pointer: UiPointer | null | undefined): string {
  if (pointer === null || pointer === undefined) return note;
  const line = formatPointerNote(pointer);
  const trimmed = note.trim();
  return trimmed === '' ? line : `${trimmed}\n${line}`;
}
