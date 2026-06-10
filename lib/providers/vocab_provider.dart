import 'dart:ui' show PlatformDispatcher;

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/vocab/vocab_preset.dart';
import '../services/vocab/vocabulary.dart';
import 'settings_provider.dart';

/// Resolve the active vocabulary language from the locale setting
/// (`'system' | 'en' | 'zh'`), mirroring `main.dart`'s locale override.
/// Only `zh` is non-default; everything else (including unsupported system
/// locales) resolves to `en`.
String resolveVocabLanguage(String localeSetting) {
  final code = localeSetting == 'system'
      ? PlatformDispatcher.instance.locale.languageCode
      : localeSetting;
  return code == 'zh' ? 'zh' : 'en';
}

/// The active [Vocabulary] — the `(preset, language)` resolver every
/// role-bound call site reads. Rebuilds when the preset or locale setting
/// changes, so a picker change re-words the UI live (ADR-048).
final vocabularyProvider = Provider<Vocabulary>((ref) {
  final preset = VocabPreset.fromId(
    ref.watch(settingsProvider.select((s) => s.vocabPreset)),
  );
  final language = resolveVocabLanguage(
    ref.watch(settingsProvider.select((s) => s.locale)),
  );
  return Vocabulary(preset, language);
});
