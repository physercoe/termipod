import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/hub_client.dart';
import '../../services/hub/hub_profiles.dart';
import '../../theme/design_colors.dart';

/// Wizard for adding or editing a hub connection profile.
///
/// Three modes, all rendered by the same form:
///  - **First-run** (`addNew: false` and no active profile): shown by
///    [ProjectsScreen] when the user has no profiles yet. Saves create
///    the first profile and activate it.
///  - **Edit-active** (`addNew: false`, active profile present): pre-fills
///    from the active profile and updates it in place. Reached from the
///    legacy "Hub settings" entry point and from "Manage profiles" when
///    editing the active row.
///  - **Add-new** (`addNew: true`): opens blank, creates a new profile
///    on save and activates it. Reached from the team-switcher
///    "Add profile" menu item.
///  - **Edit-by-id** (`profileId` non-null): pre-fills from a specific
///    profile (active or not) and saves edit it. Reached from "Manage
///    profiles" when editing a non-active row.
class HubBootstrapScreen extends ConsumerStatefulWidget {
  /// When true, the form starts blank and a successful save creates a
  /// new profile rather than updating the active one.
  final bool addNew;

  /// When non-null, the form pre-fills from this saved profile and a
  /// successful save updates it in place. Takes precedence over
  /// [addNew]. The profile becomes (or remains) active after save.
  final String? profileId;

  const HubBootstrapScreen({super.key, this.addNew = false, this.profileId});

  @override
  ConsumerState<HubBootstrapScreen> createState() => _HubBootstrapScreenState();
}

class _HubBootstrapScreenState extends ConsumerState<HubBootstrapScreen> {
  final _nameCtrl = TextEditingController();
  final _urlCtrl = TextEditingController();
  final _teamCtrl = TextEditingController(text: 'default');
  final _tokenCtrl = TextEditingController();
  final _formKey = GlobalKey<FormState>();

  bool _busy = false;
  String? _probeVersion;
  String? _probeCommit;
  String? _probeBuildTime;
  bool _probeModified = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    if (widget.addNew && widget.profileId == null) {
      // Add mode: leave blank.
      return;
    }
    // Pre-fill: prefer the explicit profileId; otherwise fall back to
    // the active profile (legacy "Hub settings" entry point).
    final hub = ref.read(hubProvider).value;
    HubProfile? prof;
    if (widget.profileId != null) {
      for (final p in hub?.profiles ?? const []) {
        if (p.id == widget.profileId) {
          prof = p;
          break;
        }
      }
    }
    if (prof != null) {
      _nameCtrl.text = prof.name;
      _urlCtrl.text = prof.baseUrl;
      _teamCtrl.text = prof.teamId;
      // Token can only be pre-filled if this is the active profile —
      // otherwise we don't have it loaded into HubConfig. Editing a
      // non-active profile means the user re-enters the token.
      if (hub?.activeProfileId == prof.id && hub?.config != null) {
        _tokenCtrl.text = hub!.config!.token;
      }
      return;
    }
    final cfg = hub?.config;
    if (cfg != null) {
      _urlCtrl.text = cfg.baseUrl;
      _teamCtrl.text = cfg.teamId;
      _tokenCtrl.text = cfg.token;
      final activeId = hub?.activeProfileId;
      if (activeId != null) {
        for (final p in hub?.profiles ?? const []) {
          if (p.id == activeId) {
            _nameCtrl.text = p.name;
            break;
          }
        }
      }
    }
  }

  @override
  void dispose() {
    _nameCtrl.dispose();
    _urlCtrl.dispose();
    _teamCtrl.dispose();
    _tokenCtrl.dispose();
    super.dispose();
  }

  String _probeBannerText() {
    final parts = <String>['Hub reachable — server $_probeVersion'];
    final c = _probeCommit;
    if (c != null && c.isNotEmpty) {
      final short = c.length > 7 ? c.substring(0, 7) : c;
      parts.add('commit $short${_probeModified ? '+dirty' : ''}');
    }
    final bt = _probeBuildTime;
    if (bt != null && bt.isNotEmpty) {
      parts.add('built ${bt.length > 10 ? bt.substring(0, 10) : bt}');
    }
    return parts.join(' · ');
  }

  Future<void> _probeOnly() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() {
      _busy = true;
      _error = null;
      _probeVersion = null;
      _probeCommit = null;
      _probeBuildTime = null;
      _probeModified = false;
    });
    final probe = HubClient(HubConfig(
      baseUrl: _urlCtrl.text.trim(),
      token: '',
      teamId: _teamCtrl.text.trim(),
    ));
    try {
      final info = await probe.getInfo();
      if (!mounted) return;
      setState(() {
        _probeVersion = info['server_version']?.toString();
        _probeCommit = info['commit']?.toString();
        _probeBuildTime = info['build_time']?.toString();
        _probeModified = info['modified'] == true;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Probe failed: $e');
    } finally {
      probe.close();
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _saveAndConnect() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    final cfg = HubConfig(
      baseUrl: _urlCtrl.text.trim(),
      token: _tokenCtrl.text.trim(),
      teamId: _teamCtrl.text.trim(),
    );
    final name = _nameCtrl.text.trim();
    final verifier = HubClient(cfg);
    try {
      await verifier.getInfo();
      await verifier.verifyAuth();
      final notifier = ref.read(hubProvider.notifier);
      if (widget.addNew && widget.profileId == null) {
        await notifier.addProfile(
          baseUrl: cfg.baseUrl,
          token: cfg.token,
          teamId: cfg.teamId,
          name: name.isEmpty ? null : name,
        );
      } else {
        // Edit mode. saveConfig only updates the *active* profile, so
        // when editing a non-active row, switch to it first. Note: that
        // activateProfile call rebuilds the client with the previously-
        // stored token and runs a refresh, so an edit-non-active flow
        // whose old token is stale will briefly show a 401 banner
        // before the saveConfig below replaces the token. Rare enough
        // (and self-resolving) to leave for now; a dedicated
        // updateProfile method is the cleaner fix later.
        final hub = ref.read(hubProvider).value;
        final targetId = widget.profileId ?? hub?.activeProfileId;
        if (targetId != null && targetId != hub?.activeProfileId) {
          await notifier.activateProfile(targetId);
        }
        await notifier.saveConfig(
          baseUrl: cfg.baseUrl,
          token: cfg.token,
          teamId: cfg.teamId,
          name: name.isEmpty ? null : name,
        );
      }
      if (!mounted) return;
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Could not connect: $e');
    } finally {
      verifier.close();
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    final title = widget.addNew && widget.profileId == null
        ? 'Add hub profile'
        : (widget.profileId != null
            ? 'Edit hub profile'
            : 'Termipod Hub');

    return Scaffold(
      appBar: AppBar(
        title: Text(
          title,
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
      ),
      body: SafeArea(
        child: Form(
          key: _formKey,
          child: ListView(
            padding: const EdgeInsets.all(20),
            children: [
              Text(
                'Point this app at a running hub-server.',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 14,
                  color: isDark
                      ? DesignColors.textSecondary
                      : DesignColors.textSecondaryLight,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                'The URL must be reachable from the phone (LAN IP, Tailscale, '
                'ngrok, etc.). The bearer token is issued by the hub admin.',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
              const SizedBox(height: 24),
              TextFormField(
                controller: _nameCtrl,
                autocorrect: false,
                decoration: const InputDecoration(
                  labelText: 'Display name (optional)',
                  helperText: 'Defaults to "team @ host"',
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 16),
              TextFormField(
                controller: _urlCtrl,
                keyboardType: TextInputType.url,
                autocorrect: false,
                decoration: const InputDecoration(
                  labelText: 'Base URL',
                  hintText: 'https://hub.example.org:8443',
                  border: OutlineInputBorder(),
                ),
                validator: (v) {
                  final s = v?.trim() ?? '';
                  if (s.isEmpty) return 'required';
                  final u = Uri.tryParse(s);
                  if (u == null || !u.hasScheme || u.host.isEmpty) {
                    return 'must be a full URL';
                  }
                  return null;
                },
              ),
              const SizedBox(height: 16),
              TextFormField(
                controller: _teamCtrl,
                autocorrect: false,
                decoration: const InputDecoration(
                  labelText: 'Team ID',
                  helperText: 'Defaults to "default" for a single-team install',
                  border: OutlineInputBorder(),
                ),
                validator: (v) =>
                    (v?.trim().isEmpty ?? true) ? 'required' : null,
              ),
              const SizedBox(height: 16),
              TextFormField(
                controller: _tokenCtrl,
                obscureText: true,
                autocorrect: false,
                decoration: const InputDecoration(
                  labelText: 'Bearer Token',
                  helperText: 'Stored in the device keychain',
                  border: OutlineInputBorder(),
                ),
                validator: (v) =>
                    (v?.trim().isEmpty ?? true) ? 'required' : null,
              ),
              const SizedBox(height: 24),
              Row(
                children: [
                  Expanded(
                    child: OutlinedButton(
                      onPressed: _busy ? null : _probeOnly,
                      child: const Text('Probe URL'),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: FilledButton(
                      onPressed: _busy ? null : _saveAndConnect,
                      child: _busy
                          ? const SizedBox(
                              width: 18,
                              height: 18,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            )
                          : const Text('Save & Connect'),
                    ),
                  ),
                ],
              ),
              if (_probeVersion != null) ...[
                const SizedBox(height: 16),
                _StatusBanner(
                  ok: true,
                  text: _probeBannerText(),
                ),
              ],
              if (_error != null) ...[
                const SizedBox(height: 16),
                _StatusBanner(ok: false, text: _error!),
              ],
              const SizedBox(height: 32),
              Text(
                'Security',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  color: colorScheme.onSurface,
                  letterSpacing: 0.5,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                'Use HTTPS or a private network overlay (Tailscale, WireGuard). '
                'The bearer token grants full team access — treat it like an '
                'SSH key.',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _StatusBanner extends StatelessWidget {
  final bool ok;
  final String text;
  const _StatusBanner({required this.ok, required this.text});

  @override
  Widget build(BuildContext context) {
    final color = ok ? DesignColors.primary : DesignColors.error;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: color.withValues(alpha: 0.4)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(ok ? Icons.check_circle : Icons.error_outline,
              color: color, size: 18),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              text,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 12,
                color: color,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
