/// Predicate for "is this agent a steward?". Multi-steward UX (per
/// docs/wedges/multi-steward.md) uses a handle-suffix convention so
/// the architecture stays schema-free:
///
///   - Plain `steward` is the legacy default; existing single-steward
///     installs keep working unchanged.
///   - `*-steward` (`research-steward`, `infra-steward`, …) marks
///     domain stewards spawned from `agents.steward.<name>` templates.
///
/// One predicate replaces the 9 hardcoded `handle == 'steward'` checks
/// scattered across the app — adding a new steward never needs another
/// code edit, and the convention lives in exactly one place.
bool isStewardHandle(String? handle) {
  if (handle == null || handle.isEmpty) return false;
  return handle == 'steward' || handle.endsWith('-steward');
}

/// Human label for a steward handle. Trims the `-steward` suffix on
/// domain handles so AppBars and chips read `research` / `infra`
/// instead of `research-steward` / `infra-steward`. Plain `steward`
/// stays as-is.
String stewardLabel(String? handle) {
  if (handle == null || handle.isEmpty) return '';
  if (handle == 'steward') return handle;
  if (handle.endsWith('-steward')) {
    return handle.substring(0, handle.length - '-steward'.length);
  }
  return handle;
}

/// Validator for the spawn-steward sheet's handle field. Returns null
/// when the input is acceptable, an error string otherwise.
///
/// Rules:
///   - Plain `steward` is allowed only when no other live steward
///     uses it (caller checks the live set, not us).
///   - Domain handles must match `[a-z][a-z0-9-]*-steward`.
///   - Anything else (e.g. `Steward`, `my steward`, `worker-bee`) is
///     rejected with a one-line hint.
String? validateStewardHandle(String raw) {
  final h = raw.trim();
  if (h.isEmpty) return 'Handle is required';
  if (h == 'steward') return null;
  final pattern = RegExp(r'^[a-z][a-z0-9-]*-steward$');
  if (!pattern.hasMatch(h)) {
    return 'Use lowercase + dashes ending in `-steward` '
        '(e.g. research-steward)';
  }
  return null;
}
