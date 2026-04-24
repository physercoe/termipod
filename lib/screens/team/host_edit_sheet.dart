import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Edits a host's non-secret SSH hint and capabilities map (blueprint
/// §5.3.2 / §5.3.3). SSH hint is the shorthand the phone uses to
/// pre-fill a Connection when a user binds an existing host; the server
/// actively refuses any field that looks like a secret (password,
/// private_key, etc.). Capabilities is a free-form JSON object the
/// client uses to drive the driving-mode fallback list.
class HostEditSheet extends ConsumerStatefulWidget {
  final Map<String, dynamic> host;
  const HostEditSheet({super.key, required this.host});

  @override
  ConsumerState<HostEditSheet> createState() => _HostEditSheetState();
}

class _HostEditSheetState extends ConsumerState<HostEditSheet> {
  late final TextEditingController _host;
  late final TextEditingController _user;
  late final TextEditingController _port;
  late final TextEditingController _caps;
  bool _submitting = false;
  String? _capsError;

  @override
  void initState() {
    super.initState();
    final hint = _parsedHint();
    _host = TextEditingController(text: (hint['host'] ?? '').toString());
    _user =
        TextEditingController(text: (hint['username'] ?? '').toString());
    _port = TextEditingController(
        text: (hint['port'] == null) ? '' : hint['port'].toString());
    _caps = TextEditingController(text: _formatCaps());
  }

  @override
  void dispose() {
    _host.dispose();
    _user.dispose();
    _port.dispose();
    _caps.dispose();
    super.dispose();
  }

  Map<String, dynamic> _parsedHint() {
    final raw = widget.host['ssh_hint_json'];
    if (raw is Map) return raw.cast<String, dynamic>();
    if (raw is String && raw.trim().isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        if (decoded is Map) return decoded.cast<String, dynamic>();
      } catch (_) {}
    }
    return const {};
  }

  String _formatCaps() {
    final raw = widget.host['capabilities'];
    if (raw is Map) {
      try {
        return const JsonEncoder.withIndent('  ').convert(raw);
      } catch (_) {}
    }
    if (raw is String && raw.trim().isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        return const JsonEncoder.withIndent('  ').convert(decoded);
      } catch (_) {
        return raw;
      }
    }
    return '{}';
  }

  Map<String, dynamic> _buildHint() {
    final m = <String, dynamic>{};
    final host = _host.text.trim();
    final user = _user.text.trim();
    final port = int.tryParse(_port.text.trim());
    if (host.isNotEmpty) m['host'] = host;
    if (user.isNotEmpty) m['username'] = user;
    if (port != null) m['port'] = port;
    return m;
  }

  Future<void> _submit() async {
    final hostId = (widget.host['id'] ?? '').toString();
    if (hostId.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;

    Map<String, dynamic> caps;
    final capsText = _caps.text.trim();
    if (capsText.isEmpty) {
      caps = const {};
    } else {
      try {
        final decoded = jsonDecode(capsText);
        if (decoded is! Map) {
          setState(() => _capsError = 'Capabilities must be a JSON object.');
          return;
        }
        caps = decoded.cast<String, dynamic>();
      } catch (e) {
        setState(() => _capsError = 'Invalid JSON: $e');
        return;
      }
    }

    setState(() {
      _submitting = true;
      _capsError = null;
    });

    final messenger = ScaffoldMessenger.of(context);
    try {
      await client.updateHostSSHHint(hostId, _buildHint());
      await client.updateHostCapabilities(hostId, caps);
      await ref.read(hubProvider.notifier).refreshAll();
      if (!mounted) return;
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() => _submitting = false);
      messenger.showSnackBar(SnackBar(content: Text('Update failed: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    final name = (widget.host['name'] ?? '').toString();
    return DraggableScrollableSheet(
      initialChildSize: 0.85,
      minChildSize: 0.5,
      maxChildSize: 0.95,
      expand: false,
      builder: (_, scroll) => Container(
        decoration: const BoxDecoration(
          color: DesignColors.surfaceDark,
          borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
        ),
        padding: EdgeInsets.fromLTRB(
          16,
          12,
          16,
          16 + MediaQuery.of(context).viewInsets.bottom,
        ),
        child: ListView(
          controller: scroll,
          children: [
            Center(
              child: Container(
                width: 36,
                height: 4,
                margin: const EdgeInsets.only(bottom: 12),
                decoration: BoxDecoration(
                  color: DesignColors.borderDark,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            Text(
              name.isEmpty ? 'Edit host' : 'Edit $name',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 18,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 4),
            Text(
              'Secret fields (passwords, keys, passphrases) are refused '
              'by the hub; store those on the device.',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
            const SizedBox(height: 16),
            _sectionTitle('SSH hint'),
            _field(label: 'Host', controller: _host, mono: true),
            _field(label: 'Username', controller: _user, mono: true),
            _field(
              label: 'Port',
              controller: _port,
              mono: true,
              keyboardType: TextInputType.number,
              inputFormatters: [FilteringTextInputFormatter.digitsOnly],
            ),
            const SizedBox(height: 16),
            _sectionTitle('Capabilities (JSON)'),
            TextField(
              controller: _caps,
              enabled: !_submitting,
              minLines: 6,
              maxLines: 16,
              style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
              decoration: InputDecoration(
                border: const OutlineInputBorder(),
                isDense: true,
                hintText: '{\n  "tmux": "3.4",\n  "nvidia_smi": true\n}',
                hintStyle: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
                errorText: _capsError,
              ),
              onChanged: (_) {
                if (_capsError != null) setState(() => _capsError = null);
              },
            ),
            const SizedBox(height: 20),
            FilledButton(
              onPressed: _submitting ? null : _submit,
              child: _submitting
                  ? const SizedBox(
                      width: 14,
                      height: 14,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Text('Save changes'),
            ),
          ],
        ),
      ),
    );
  }

  Widget _sectionTitle(String s) => Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Text(
          s,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 12,
            fontWeight: FontWeight.w700,
            letterSpacing: 0.4,
            color: DesignColors.textMuted,
          ),
        ),
      );

  Widget _field({
    required String label,
    required TextEditingController controller,
    bool mono = false,
    TextInputType? keyboardType,
    List<TextInputFormatter>? inputFormatters,
  }) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.only(bottom: 4),
            child: Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                letterSpacing: 0.5,
                color: DesignColors.textMuted,
              ),
            ),
          ),
          TextField(
            controller: controller,
            enabled: !_submitting,
            keyboardType: keyboardType,
            inputFormatters: inputFormatters,
            style: mono
                ? GoogleFonts.jetBrainsMono(fontSize: 13)
                : GoogleFonts.spaceGrotesk(fontSize: 14),
            decoration: const InputDecoration(
              border: OutlineInputBorder(),
              isDense: true,
            ),
          ),
        ],
      ),
    );
  }
}
