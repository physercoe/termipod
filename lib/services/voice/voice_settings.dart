import 'cloud_stt.dart';

// Re-export the enums so callers that only import voice_settings.dart
// can reference DashScopeRegion / DashScopeAsrModel without a second
// import.
export 'cloud_stt.dart' show DashScopeRegion, DashScopeAsrModel;

/// Immutable voice-input configuration surfaced to the UI. The API key
/// itself is **not** carried on this object — it lives in
/// flutter_secure_storage and is read on-demand. [hasApiKey] is the
/// derived "do we have one" flag UI uses to decide whether to render
/// the mic affordance.
class VoiceSettings {
  const VoiceSettings({
    this.enabled = false,
    this.autoSendPuckTranscripts = true,
    this.region = DashScopeRegion.beijing,
    this.model = DashScopeAsrModel.funAsrRealtime,
    this.languageHints = const ['zh', 'en'],
    this.hasApiKey = false,
  });

  /// Master gate. When false, no mic affordance is rendered anywhere.
  /// Defaults to false — user must opt in (Settings → Voice → Voice
  /// input toggle) because voice requires a DashScope API key and
  /// outbound audio.
  final bool enabled;

  /// Auto-send puck-long-press transcripts directly to the steward
  /// (hands-free Mode A). When false, puck long-press routes through
  /// Mode B's review handler (panel auto-opens with the transcript
  /// pre-filled). Mode B's panel mic button is always
  /// review-then-send regardless of this flag.
  final bool autoSendPuckTranscripts;

  final DashScopeRegion region;
  final DashScopeAsrModel model;
  final List<String> languageHints;
  final bool hasApiKey;

  bool get isReady => enabled && hasApiKey;

  VoiceSettings copyWith({
    bool? enabled,
    bool? autoSendPuckTranscripts,
    DashScopeRegion? region,
    DashScopeAsrModel? model,
    List<String>? languageHints,
    bool? hasApiKey,
  }) {
    return VoiceSettings(
      enabled: enabled ?? this.enabled,
      autoSendPuckTranscripts:
          autoSendPuckTranscripts ?? this.autoSendPuckTranscripts,
      region: region ?? this.region,
      model: model ?? this.model,
      languageHints: languageHints ?? this.languageHints,
      hasApiKey: hasApiKey ?? this.hasApiKey,
    );
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is VoiceSettings &&
          enabled == other.enabled &&
          autoSendPuckTranscripts == other.autoSendPuckTranscripts &&
          region == other.region &&
          model == other.model &&
          _listEq(languageHints, other.languageHints) &&
          hasApiKey == other.hasApiKey;

  @override
  int get hashCode => Object.hash(
        enabled,
        autoSendPuckTranscripts,
        region,
        model,
        Object.hashAll(languageHints),
        hasApiKey,
      );
}

bool _listEq(List<String> a, List<String> b) {
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

/// Stable string keys for serialising the enum-valued settings. Keeps
/// the on-disk format independent of enum declaration order, so adding
/// a new region or model later doesn't shift existing stored values.
String regionToKey(DashScopeRegion region) => switch (region) {
      DashScopeRegion.beijing => 'beijing',
      DashScopeRegion.singapore => 'singapore',
      DashScopeRegion.us => 'us',
    };

DashScopeRegion regionFromKey(String? key) => switch (key) {
      'singapore' => DashScopeRegion.singapore,
      'us' => DashScopeRegion.us,
      _ => DashScopeRegion.beijing,
    };

String modelToKey(DashScopeAsrModel model) => switch (model) {
      DashScopeAsrModel.funAsrRealtime => 'fun-asr-realtime',
      DashScopeAsrModel.paraformerRealtimeV2 => 'paraformer-realtime-v2',
    };

DashScopeAsrModel modelFromKey(String? key) => switch (key) {
      'paraformer-realtime-v2' => DashScopeAsrModel.paraformerRealtimeV2,
      _ => DashScopeAsrModel.funAsrRealtime,
    };
