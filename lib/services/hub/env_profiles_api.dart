import 'hub_transport.dart';

/// Team environment profiles (env-profiles plan) — reusable bundles of
/// {setup_script + env_vars + secret_refs + network_policy} a spawn attaches
/// via `env_profile_id`. Metadata only (blueprint §4): the hub holds env_vars +
/// setup_script; secret_refs point into the team's zero-knowledge vault, never
/// values. Backs the spawn-sheet picker and the management screen.
class EnvProfilesApi {
  final HubTransport _t;
  EnvProfilesApi(this._t);

  Future<List<Map<String, dynamic>>> listEnvProfiles() =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/env-profiles');

  Future<Map<String, dynamic>> getEnvProfile(String id) async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/env-profiles/$id');
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> createEnvProfile(
      Map<String, dynamic> body) async {
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/env-profiles', body);
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> updateEnvProfile(
      String id, Map<String, dynamic> patch) async {
    final out =
        await _t.patch('/v1/teams/${_t.cfg.teamId}/env-profiles/$id', patch);
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> deleteEnvProfile(String id) =>
      _t.delete('/v1/teams/${_t.cfg.teamId}/env-profiles/$id');
}
