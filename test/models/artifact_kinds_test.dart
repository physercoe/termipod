import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/models/artifact_kinds.dart';

void main() {
  group('ArtifactKind', () {
    test('every enum value has a spec entry', () {
      for (final k in ArtifactKind.values) {
        expect(kArtifactKindSpecs.containsKey(k), isTrue,
            reason: 'missing spec for $k');
        expect(kArtifactKindSpecs[k]!.kind, k);
        expect(kArtifactKindSpecs[k]!.label.isNotEmpty, isTrue);
      }
    });

    test('fromSlug round-trips every enum slug', () {
      for (final k in ArtifactKind.values) {
        expect(ArtifactKind.fromSlug(k.slug), k);
      }
    });

    test('fromSlug returns null for unknown or empty', () {
      expect(ArtifactKind.fromSlug(null), isNull);
      expect(ArtifactKind.fromSlug(''), isNull);
      expect(ArtifactKind.fromSlug('not-a-kind'), isNull);
    });
  });

  group('artifactKindSpecFor', () {
    test('returns the direct spec for an MVP slug', () {
      expect(artifactKindSpecFor('tabular').kind, ArtifactKind.tabular);
      expect(artifactKindSpecFor('pdf').kind, ArtifactKind.pdf);
    });

    test('remaps every legacy alias to a valid MVP kind', () {
      // Must mirror backfillLegacyArtifactKind in artifact_kinds.go.
      const legacy = <String, ArtifactKind>{
        'checkpoint': ArtifactKind.externalBlob,
        'dataset': ArtifactKind.externalBlob,
        'other': ArtifactKind.externalBlob,
        'eval_curve': ArtifactKind.metricChart,
        'log': ArtifactKind.proseDocument,
        'report': ArtifactKind.proseDocument,
        'figure': ArtifactKind.image,
        'sample': ArtifactKind.image,
      };
      for (final entry in legacy.entries) {
        expect(artifactKindSpecFor(entry.key).kind, entry.value,
            reason: 'alias ${entry.key} should map to ${entry.value}');
      }
    });

    test('falls back to externalBlob for unknown or empty input', () {
      expect(artifactKindSpecFor(null).kind, ArtifactKind.externalBlob);
      expect(artifactKindSpecFor('').kind, ArtifactKind.externalBlob);
      expect(artifactKindSpecFor('totally-unknown').kind,
          ArtifactKind.externalBlob);
    });
  });
}
