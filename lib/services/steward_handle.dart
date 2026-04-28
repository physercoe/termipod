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

/// Validator for the spawn-steward sheet's name field. Returns null
/// when the input is acceptable, an error string otherwise.
///
/// User-facing convention: the field is called "Name" and the user
/// types the bare domain (`research`, `infra`, …); the app appends
/// the `-steward` suffix via [normalizeStewardHandle] before
/// persisting. Plain `steward` is the default and stays as-is.
///
/// Rules (post-normalization — what the server actually stores):
///   - `steward` (the default) is allowed.
///   - `<name>-steward` where `<name>` matches `[a-z][a-z0-9-]*`.
///   - Anything else (`Steward`, `my steward`, `worker-bee`) rejected.
///
/// Live-uniqueness is checked by the caller against the
/// live-steward set; this function only validates the shape.
String? validateStewardHandle(String raw) {
  final h = raw.trim();
  if (h.isEmpty) return 'Name is required';
  if (h == 'steward') return null;
  final pattern = RegExp(r'^[a-z][a-z0-9-]*-steward$');
  if (!pattern.hasMatch(h)) {
    return 'Use lowercase letters, digits, and dashes '
        '(e.g. research, infra-east)';
  }
  return null;
}

/// Maps the user-facing bare name to the internal steward handle
/// the hub stores. The spawn sheet labels this field as "Name" and
/// asks the user to type just the domain (e.g. `research`,
/// `infra-east`); this function appends the `-steward` suffix so
/// the server-side `isStewardHandle` predicate keeps working
/// without leaking the suffix convention into the UI.
///
/// Idempotent: if the input already ends in `-steward` (paste from
/// the legacy hint, or copied from the agents table), it's returned
/// unchanged. Plain `steward` is the default and stays as-is.
String normalizeStewardHandle(String raw) {
  final h = raw.trim();
  if (h.isEmpty) return h;
  if (h == 'steward') return h;
  if (h.endsWith('-steward')) return h;
  return '$h-steward';
}
