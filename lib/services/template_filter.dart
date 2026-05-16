/// Project-scope filter for the agent-template pickers.
///
/// Templates carry `applicable_to.template_ids: [<project-template-id>, ...]`
/// on the YAML. The hub parses it during `listTemplates` and exposes it
/// as `applicable_template_ids` on each row. The mobile project pickers
/// (Spawn worker, Plan-create's agent-template field) filter against the
/// current project's `template_id` so a research project doesn't see
/// agent templates that only make sense for a bio-sim project, and vice
/// versa.
///
/// Back-compat default: a template with no `applicable_to:` field comes
/// back with an empty `applicable_template_ids` list and is treated as
/// team-shared (visible in every project picker). This keeps every
/// bundled template visible until a steward explicitly scopes new ones.

/// Returns the subset of [rows] visible from a project whose
/// `template_id` is [projectTemplateId]. Templates with an empty (or
/// absent) `applicable_template_ids` are team-shared and always pass.
/// A scoped template passes when its list contains [projectTemplateId];
/// when [projectTemplateId] is null/empty (legacy project with no
/// template binding), only team-shared rows pass.
List<Map<String, dynamic>> filterTemplatesForProject(
  List<Map<String, dynamic>> rows,
  String? projectTemplateId,
) {
  final pid = (projectTemplateId ?? '').trim();
  return rows.where((r) {
    final raw = r['applicable_template_ids'];
    if (raw is! List || raw.isEmpty) {
      return true; // team-shared
    }
    if (pid.isEmpty) {
      return false; // scoped template + no project template binding
    }
    for (final id in raw) {
      if (id.toString() == pid) return true;
    }
    return false;
  }).toList(growable: false);
}
