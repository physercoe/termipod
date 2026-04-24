import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/hub_client.dart';
import '../../theme/design_colors.dart';

/// First-run wizard that collects the hub base URL, team id, and bearer
/// token, probes the endpoint, and saves on success.
///
/// Shown by [ProjectsScreen] when [HubState.configured] is false. Also reached
/// from Settings > Termipod Hub to re-configure an existing install.
class HubBootstrapScreen extends ConsumerStatefulWidget {
  const HubBootstrapScreen({super.key});

  @override
  ConsumerState<HubBootstrapScreen> createState() => _HubBootstrapScreenState();
}

class _HubBootstrapScreenState extends ConsumerState<HubBootstrapScreen> {
  final _urlCtrl = TextEditingController();
  final _teamCtrl = TextEditingController(text: 'default');
  final _tokenCtrl = TextEditingController();
  final _formKey = GlobalKey<FormState>();

  bool _busy = false;
  String? _probeVersion;
  String? _error;

  @override
  void initState() {
    super.initState();
    final cfg = ref.read(hubProvider).value?.config;
    if (cfg != null) {
      _urlCtrl.text = cfg.baseUrl;
      _teamCtrl.text = cfg.teamId;
      _tokenCtrl.text = cfg.token;
    }
  }

  @override
  void dispose() {
    _urlCtrl.dispose();
    _teamCtrl.dispose();
    _tokenCtrl.dispose();
    super.dispose();
  }

  Future<void> _probeOnly() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() {
      _busy = true;
      _error = null;
      _probeVersion = null;
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
    final verifier = HubClient(cfg);
    try {
      await verifier.getInfo();
      await verifier.verifyAuth();
      await ref.read(hubProvider.notifier).saveConfig(
            baseUrl: cfg.baseUrl,
            token: cfg.token,
            teamId: cfg.teamId,
          );
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

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Termipod Hub',
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
                  text: 'Hub reachable — server version $_probeVersion',
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
