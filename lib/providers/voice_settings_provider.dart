import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../services/voice/voice_settings.dart';

/// Riverpod Notifier for voice-input configuration. Implements the
/// `await _ready` pattern (see feedback_prefs_load_race) so mutators
/// don't race the async prefs load and clobber freshly-written values
/// with stale defaults.
class VoiceSettingsNotifier extends Notifier<VoiceSettings> {
  static const _enabledKey = 'voice_enabled';
  static const _autoSendKey = 'voice_auto_send_puck';
  static const _regionKey = 'voice_region';
  static const _modelKey = 'voice_model';
  static const _languageHintsKey = 'voice_language_hints';
  static const _apiKeyStorageKey = 'voice_dashscope_api_key';

  final Completer<void> _ready = Completer<void>();

  /// Resolves when the on-disk state has been loaded into [state].
  /// Mutators await this before writing so the very first user toggle
  /// after app launch isn't overwritten by a late-arriving _load.
  Future<void> get ready => _ready.future;

  @override
  VoiceSettings build() {
    _load();
    return const VoiceSettings();
  }

  Future<void> _load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      const storage = FlutterSecureStorage();
      final apiKey = await storage.read(key: _apiKeyStorageKey);

      state = VoiceSettings(
        enabled: prefs.getBool(_enabledKey) ?? false,
        autoSendPuckTranscripts: prefs.getBool(_autoSendKey) ?? true,
        region: regionFromKey(prefs.getString(_regionKey)),
        model: modelFromKey(prefs.getString(_modelKey)),
        languageHints: prefs.getStringList(_languageHintsKey) ??
            const ['zh', 'en'],
        hasApiKey: apiKey != null && apiKey.isNotEmpty,
      );
    } catch (_) {
      // Defensive: stay on default settings if storage is unavailable.
      // Voice is purely additive — a load failure should not crash the
      // app or hide the rest of the UI.
    } finally {
      if (!_ready.isCompleted) _ready.complete();
    }
  }

  Future<void> setEnabled(bool value) async {
    await _ready.future;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_enabledKey, value);
    state = state.copyWith(enabled: value);
  }

  Future<void> setAutoSendPuckTranscripts(bool value) async {
    await _ready.future;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_autoSendKey, value);
    state = state.copyWith(autoSendPuckTranscripts: value);
  }

  Future<void> setRegion(DashScopeRegion region) async {
    await _ready.future;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_regionKey, regionToKey(region));
    state = state.copyWith(region: region);
  }

  Future<void> setModel(DashScopeAsrModel model) async {
    await _ready.future;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_modelKey, modelToKey(model));
    state = state.copyWith(model: model);
  }

  Future<void> setLanguageHints(List<String> hints) async {
    await _ready.future;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setStringList(_languageHintsKey, hints);
    state = state.copyWith(languageHints: List.unmodifiable(hints));
  }

  /// Stores [key] in secure storage, or clears the entry when [key] is
  /// null or empty. Updates [VoiceSettings.hasApiKey] accordingly.
  Future<void> setApiKey(String? key) async {
    await _ready.future;
    const storage = FlutterSecureStorage();
    if (key == null || key.isEmpty) {
      await storage.delete(key: _apiKeyStorageKey);
      state = state.copyWith(hasApiKey: false);
    } else {
      await storage.write(key: _apiKeyStorageKey, value: key);
      state = state.copyWith(hasApiKey: true);
    }
  }

  /// Reads the API key from secure storage. Returns null when nothing
  /// is stored. Callers (the recording session, the test sheet) read
  /// this on-demand rather than caching it in the model — keeps the
  /// secret out of Riverpod's observable state graph.
  Future<String?> readApiKey() async {
    await _ready.future;
    const storage = FlutterSecureStorage();
    return storage.read(key: _apiKeyStorageKey);
  }
}

final voiceSettingsProvider =
    NotifierProvider<VoiceSettingsNotifier, VoiceSettings>(
  VoiceSettingsNotifier.new,
);
