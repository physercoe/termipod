import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/voice/cloud_stt.dart';
import 'package:termipod/services/voice/voice_settings.dart';

void main() {
  group('VoiceSettings defaults', () {
    test('opt-in: enabled defaults to false', () {
      const s = VoiceSettings();
      expect(s.enabled, isFalse);
    });

    test('autoSendPuckTranscripts defaults to true (hands-free Mode A)', () {
      const s = VoiceSettings();
      expect(s.autoSendPuckTranscripts, isTrue);
    });

    test('Beijing region + fun-asr-realtime model are the defaults', () {
      const s = VoiceSettings();
      expect(s.region, DashScopeRegion.beijing);
      expect(s.model, DashScopeAsrModel.funAsrRealtime);
    });

    test('default languageHints are zh + en for code-switching', () {
      const s = VoiceSettings();
      expect(s.languageHints, ['zh', 'en']);
    });

    test('isReady requires both enabled and hasApiKey', () {
      const noKey = VoiceSettings(enabled: true);
      const noEnable = VoiceSettings(hasApiKey: true);
      const ready = VoiceSettings(enabled: true, hasApiKey: true);
      expect(noKey.isReady, isFalse);
      expect(noEnable.isReady, isFalse);
      expect(ready.isReady, isTrue);
    });
  });

  group('VoiceSettings.copyWith', () {
    test('returns identical content when called with no overrides', () {
      const s = VoiceSettings(
        enabled: true,
        autoSendPuckTranscripts: false,
        region: DashScopeRegion.singapore,
        model: DashScopeAsrModel.paraformerRealtimeV2,
        languageHints: ['yue'],
        hasApiKey: true,
      );
      expect(s.copyWith(), equals(s));
    });

    test('overrides only the named fields', () {
      const s = VoiceSettings();
      final updated = s.copyWith(enabled: true, hasApiKey: true);
      expect(updated.enabled, isTrue);
      expect(updated.hasApiKey, isTrue);
      expect(updated.region, DashScopeRegion.beijing);
      expect(updated.languageHints, ['zh', 'en']);
    });
  });

  group('VoiceSettings equality', () {
    test('equal when all fields match', () {
      const a = VoiceSettings(enabled: true, languageHints: ['zh']);
      const b = VoiceSettings(enabled: true, languageHints: ['zh']);
      expect(a, equals(b));
      expect(a.hashCode, equals(b.hashCode));
    });

    test('differ when languageHints differ', () {
      const a = VoiceSettings(languageHints: ['zh', 'en']);
      const b = VoiceSettings(languageHints: ['zh']);
      expect(a, isNot(equals(b)));
    });
  });

  group('region/model serialisation', () {
    test('region keys round-trip', () {
      for (final r in DashScopeRegion.values) {
        expect(regionFromKey(regionToKey(r)), r);
      }
    });

    test('regionFromKey defaults to beijing on unknown / null input', () {
      expect(regionFromKey(null), DashScopeRegion.beijing);
      expect(regionFromKey('moonbase'), DashScopeRegion.beijing);
    });

    test('region keys are stable strings (not enum index)', () {
      expect(regionToKey(DashScopeRegion.beijing), 'beijing');
      expect(regionToKey(DashScopeRegion.singapore), 'singapore');
      expect(regionToKey(DashScopeRegion.us), 'us');
    });

    test('model keys round-trip', () {
      for (final m in DashScopeAsrModel.values) {
        expect(modelFromKey(modelToKey(m)), m);
      }
    });

    test('modelFromKey defaults to fun-asr-realtime on unknown input', () {
      expect(modelFromKey(null), DashScopeAsrModel.funAsrRealtime);
      expect(modelFromKey('whisper-large'), DashScopeAsrModel.funAsrRealtime);
    });

    test('model keys match the DashScope wire identifiers', () {
      expect(modelToKey(DashScopeAsrModel.funAsrRealtime), 'fun-asr-realtime');
      expect(modelToKey(DashScopeAsrModel.paraformerRealtimeV2),
          'paraformer-realtime-v2');
    });
  });
}
