/// Steward-spawn contract for the desktop sheet (ui/StewardSpawn.tsx) —
/// desktop parity with mobile's spawn_steward_sheet.dart. Pure and
/// import-free so `node --test` covers it directly.
///
/// The conventions mirror `lib/services/steward_handle.dart`, the one place
/// the handle taxonomy lives on mobile — a steward is spawned from a
/// `agents/steward*.yaml` template (its `default_role: team.*` is what
/// escalates the role hub-side; the spawn's `kind` stays the ENGINE parsed
/// from the template's `backend.kind`). Handles: plain `steward` (the legacy
/// default) or `<name>-steward` for domain stewards. The team-scoped general
/// concierge (`@steward`, the `steward.general*` template) is a singleton
/// with its own hub ensure-endpoint and is deliberately NOT offered here.

/// Is this handle a domain/legacy steward (`steward` / `*-steward`)? The
/// `@steward` concierge and project stewards (`@steward.<pid8>`) do not
/// match — by design, this predicate drives the collision check against
/// user-typed names, which can never take an `@` form.
export function isStewardHandle(handle: string): boolean {
  return handle === 'steward' || handle.endsWith('-steward');
}

/// Validate the sheet's Name field. Returns an error CODE (an i18n key
/// suffix under `steward.err*`) or null when acceptable. The user types the
/// bare domain (`research`, `infra-east`) or plain `steward`; the shape rule
/// is checked post-normalization, i.e. against what the hub would store.
export function validateStewardName(raw: string): 'required' | 'shape' | null {
  const h = normalizeStewardHandle(raw);
  if (h === '') return 'required';
  if (h === 'steward') return null;
  return /^[a-z][a-z0-9-]*-steward$/.test(h) ? null : 'shape';
}

/// Bare name → the handle the hub stores. Idempotent: `steward` and an
/// already-suffixed `<name>-steward` pass through unchanged.
export function normalizeStewardHandle(raw: string): string {
  const h = raw.trim();
  if (h === '' || h === 'steward' || h.endsWith('-steward')) return h;
  return `${h}-steward`;
}

/// Default for the Name field: `steward` when no live steward owns it,
/// otherwise empty so the user picks a domain.
export function defaultStewardName(liveHandles: ReadonlySet<string>): string {
  return liveHandles.has('steward') ? '' : 'steward';
}

/// The steward templates the sheet offers, from the team's `agents` template
/// listing: every `steward*.yaml` EXCEPT the `steward.general*` singleton
/// (the concierge has its own ensure-spawn path and must not appear next to
/// user-named domain stewards). Sorted; falls back to the shipped default so
/// the sheet works before the listing loads.
export function stewardTemplatePicks(names: readonly string[]): string[] {
  const picks = names.filter((n) => n.startsWith('steward') && !n.startsWith('steward.general'));
  picks.sort();
  return picks.length > 0 ? picks : ['steward.v1.yaml'];
}

/// Parse `steward.research.v1.yaml` → `research` to seed the Name field when
/// the user picks a domain template; '' for anything off-convention so a
/// name the user already typed is never clobbered.
export function suggestedNameFor(template: string): string {
  const m = /^steward\.([a-z][a-z0-9-]*)\.v\d+\.yaml$/.exec(template);
  return m === null ? '' : m[1];
}

/// Extract `backend.kind: <engine>` from a steward template body — the value
/// the spawn request sends as `kind` (the ENGINE; the steward persona rides
/// the spec YAML). Mirrors mobile's block-aware mini-parser: find the
/// top-level `backend:` block, then an indented `kind:` whose value is one
/// token; reset at the next top-level key so we never bleed across blocks.
/// null when the field can't be located (malformed / off-convention YAML).
export function parseBackendKind(yaml: string): string | null {
  const childRe = /^\s+kind:\s*([A-Za-z0-9_.-]+)\s*$/;
  let inBlock = false;
  for (const line of yaml.split('\n')) {
    const trimmed = line.trimEnd();
    if (trimmed === 'backend:') {
      inBlock = true;
      continue;
    }
    if (inBlock && trimmed !== '' && !trimmed.startsWith(' ')) inBlock = false;
    if (inBlock) {
      const m = childRe.exec(line);
      if (m !== null) return m[1];
    }
  }
  return null;
}
