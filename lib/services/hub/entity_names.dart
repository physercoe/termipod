import 'dart:convert';

/// Resolves hub entity ULIDs to human-readable labels for list UI. Hub
/// surfaces return ids; screens join against the already-loaded hub-state
/// lists so the user sees names instead of `01k…`-shaped strings.
///
/// All helpers fall back to the raw id when the target isn't in the list —
/// a new row the dashboard hasn't yet refreshed in to, or a cross-team
/// reference that won't resolve. A visible id is still better than a blank.

String projectNameFor(
  String id,
  List<Map<String, dynamic>> projects, {
  String fallback = '',
}) {
  if (id.isEmpty) return fallback;
  for (final p in projects) {
    if ((p['id'] ?? '').toString() == id) {
      final name = (p['name'] ?? '').toString();
      if (name.isNotEmpty) return name;
    }
  }
  return fallback.isEmpty ? id : fallback;
}

String agentHandleFor(
  String id,
  List<Map<String, dynamic>> agents, {
  String fallback = '',
}) {
  if (id.isEmpty) return fallback;
  for (final a in agents) {
    if ((a['id'] ?? '').toString() == id) {
      final handle = (a['handle'] ?? '').toString();
      if (handle.isNotEmpty) return handle;
    }
  }
  return fallback.isEmpty ? id : fallback;
}

/// Composes a short descriptor for a run when the server doesn't carry
/// a name column. The runs table stores `config_json` — for ablation
/// sweeps that's `{"n_embd": 128, "optimizer": "adamw"}`. The returned
/// label joins the two most recognizable hyperparameters into a tight
/// "n_embd=128 · adamw" string. Falls back to a short id prefix when
/// the config is empty or unparseable.
String runLabelFor(Map<String, dynamic> row) {
  final name = (row['name'] ?? '').toString();
  if (name.isNotEmpty) return name;
  final cfg = _parseConfig(row['config_json']);
  if (cfg.isNotEmpty) {
    final parts = <String>[];
    // Pull the ablation-sweep shape first (size + optimizer), fall back to
    // whatever scalar keys exist. Cap at two pieces so the label stays
    // one-line-friendly.
    void addKV(String key, {bool prefix = true}) {
      final v = cfg[key];
      if (v == null) return;
      final s = v is num || v is String || v is bool ? v.toString() : '';
      if (s.isEmpty) return;
      parts.add(prefix ? '$key=$s' : s);
    }
    addKV('n_embd');
    addKV('optimizer', prefix: false);
    if (parts.length < 2) {
      for (final entry in cfg.entries) {
        if (entry.key == 'n_embd' || entry.key == 'optimizer') continue;
        final v = entry.value;
        if (v is! num && v is! String && v is! bool) continue;
        parts.add('${entry.key}=$v');
        if (parts.length >= 2) break;
      }
    }
    if (parts.isNotEmpty) return parts.join(' · ');
  }
  final id = (row['id'] ?? '').toString();
  return id.length > 8 ? id.substring(id.length - 6) : (id.isEmpty ? '(run)' : id);
}

Map<String, dynamic> _parseConfig(Object? raw) {
  if (raw is Map) return raw.cast<String, dynamic>();
  if (raw is String && raw.isNotEmpty) {
    try {
      final decoded = jsonDecode(raw);
      if (decoded is Map) return decoded.cast<String, dynamic>();
    } catch (_) {}
  }
  return const {};
}
