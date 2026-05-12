import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/voice_settings_provider.dart';
import '../../services/voice/voice_settings.dart';
import '../../theme/design_colors.dart';

/// Settings UI for the Path C voice-input pipeline. Lets the user
/// enable voice, paste a DashScope API key (stored in
/// flutter_secure_storage), pick a region/model, and toggle the
/// puck-long-press auto-send behavior.
class VoiceSettingsScreen extends ConsumerWidget {
  const VoiceSettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final settings = ref.watch(voiceSettingsProvider);
    final notifier = ref.read(voiceSettingsProvider.notifier);

    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Voice input',
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
      ),
      body: ListView(
        padding: const EdgeInsets.symmetric(vertical: 12),
        children: [
          SwitchListTile(
            secondary: const Icon(Icons.mic_outlined),
            title: const Text('Voice input'),
            subtitle: const Text(
              'Long-press the mic button to dictate. Audio is sent to '
              'DashScope; transcripts return as text. The hub never sees '
              'audio.',
            ),
            value: settings.enabled,
            onChanged: (v) => notifier.setEnabled(v),
          ),
          if (settings.enabled) ...[
            const Divider(height: 1),
            SwitchListTile(
              secondary: const Icon(Icons.send_outlined),
              title: const Text('Auto-send puck transcripts'),
              subtitle: const Text(
                'When off, puck long-press opens the chat for review '
                'before sending. The panel mic button always reviews '
                'first.',
              ),
              value: settings.autoSendPuckTranscripts,
              onChanged: (v) => notifier.setAutoSendPuckTranscripts(v),
            ),
            const Divider(height: 1),
            ListTile(
              leading: const Icon(Icons.vpn_key_outlined),
              title: const Text('DashScope API key'),
              subtitle: Text(
                settings.hasApiKey ? 'Stored securely • tap to replace' : 'Not set',
                style: TextStyle(
                  color: settings.hasApiKey
                      ? DesignColors.success
                      : DesignColors.warning,
                ),
              ),
              trailing: settings.hasApiKey
                  ? IconButton(
                      tooltip: 'Clear stored key',
                      icon: const Icon(Icons.delete_outline),
                      onPressed: () => _confirmClearKey(context, notifier),
                    )
                  : null,
              onTap: () => _showApiKeyDialog(context, notifier),
            ),
            const Divider(height: 1),
            ListTile(
              leading: const Icon(Icons.public),
              title: const Text('Region'),
              subtitle: Text(_regionLabel(settings.region)),
              onTap: () => _showRegionPicker(context, notifier, settings.region),
            ),
            const Divider(height: 1),
            ListTile(
              leading: const Icon(Icons.psychology_outlined),
              title: const Text('Model'),
              subtitle: Text(_modelLabel(settings.model)),
              onTap: () => _showModelPicker(context, notifier, settings.model),
            ),
          ],
        ],
      ),
    );
  }
}

String _regionLabel(DashScopeRegion r) => switch (r) {
      DashScopeRegion.beijing => 'Beijing (default) — dashscope.aliyuncs.com',
      DashScopeRegion.singapore => 'Singapore — dashscope-intl.aliyuncs.com',
      DashScopeRegion.us => 'US — dashscope-us.aliyuncs.com',
    };

String _modelLabel(DashScopeAsrModel m) => switch (m) {
      DashScopeAsrModel.funAsrRealtime =>
        'Fun-ASR realtime — zh + 8 dialects + en + ja',
      DashScopeAsrModel.paraformerRealtimeV2 =>
        'Paraformer realtime v2 — broader language list',
    };

Future<void> _showApiKeyDialog(
  BuildContext context,
  VoiceSettingsNotifier notifier,
) async {
  final controller = TextEditingController();
  final next = await showDialog<String>(
    context: context,
    builder: (ctx) {
      return AlertDialog(
        title: const Text('DashScope API key'),
        content: TextField(
          controller: controller,
          autofocus: true,
          obscureText: true,
          decoration: const InputDecoration(
            labelText: 'sk-…',
            helperText: 'Stored on this device only. Never sent to the hub.',
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(controller.text.trim()),
            child: const Text('Save'),
          ),
        ],
      );
    },
  );
  if (next != null && next.isNotEmpty) {
    await notifier.setApiKey(next);
  }
}

Future<void> _confirmClearKey(
  BuildContext context,
  VoiceSettingsNotifier notifier,
) async {
  final ok = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: const Text('Clear API key?'),
      content: const Text(
        'Voice input will stop working until a new key is entered.',
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          style: FilledButton.styleFrom(
            backgroundColor: DesignColors.error,
          ),
          onPressed: () => Navigator.of(ctx).pop(true),
          child: const Text('Clear'),
        ),
      ],
    ),
  );
  if (ok == true) {
    await notifier.setApiKey(null);
  }
}

Future<void> _showRegionPicker(
  BuildContext context,
  VoiceSettingsNotifier notifier,
  DashScopeRegion current,
) async {
  final next = await showDialog<DashScopeRegion>(
    context: context,
    builder: (ctx) => SimpleDialog(
      title: const Text('Region'),
      children: [
        for (final r in DashScopeRegion.values)
          RadioListTile<DashScopeRegion>(
            title: Text(_regionLabel(r)),
            value: r,
            groupValue: current,
            onChanged: (v) => Navigator.of(ctx).pop(v),
          ),
      ],
    ),
  );
  if (next != null) await notifier.setRegion(next);
}

Future<void> _showModelPicker(
  BuildContext context,
  VoiceSettingsNotifier notifier,
  DashScopeAsrModel current,
) async {
  final next = await showDialog<DashScopeAsrModel>(
    context: context,
    builder: (ctx) => SimpleDialog(
      title: const Text('Model'),
      children: [
        for (final m in DashScopeAsrModel.values)
          RadioListTile<DashScopeAsrModel>(
            title: Text(_modelLabel(m)),
            value: m,
            groupValue: current,
            onChanged: (v) => Navigator.of(ctx).pop(v),
          ),
      ],
    ),
  );
  if (next != null) await notifier.setModel(next);
}
