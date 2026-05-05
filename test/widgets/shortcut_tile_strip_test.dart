import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/shortcut_tile_strip.dart';

void main() {
  group('resolveTilesForPhase — research-template hardcoded mapping (W4)', () {
    test('lit-review phase → [References, Documents]', () {
      expect(
        resolveTilesForPhase(templateId: 'research', phase: 'lit-review'),
        [TileSlug.references, TileSlug.documents],
      );
    });

    test('method phase → [References, Documents, Plans]', () {
      expect(
        resolveTilesForPhase(templateId: 'research', phase: 'method'),
        [TileSlug.references, TileSlug.documents, TileSlug.plans],
      );
    });

    test('experiment phase → [Outputs, Documents, Experiments]', () {
      expect(
        resolveTilesForPhase(templateId: 'research', phase: 'experiment'),
        [TileSlug.outputs, TileSlug.documents, TileSlug.experiments],
      );
    });

    test('paper phase → [Outputs, Documents]', () {
      expect(
        resolveTilesForPhase(templateId: 'research', phase: 'paper'),
        [TileSlug.outputs, TileSlug.documents],
      );
    });

    test('idea phase → [] (conversation-first)', () {
      expect(
        resolveTilesForPhase(templateId: 'research', phase: 'idea'),
        isEmpty,
      );
    });

    test('Reviews is never in any default tile set (gap #4)', () {
      // Template-yaml-schema §11 closed enum has no Reviews slug; the
      // hardcoded research map must not surface one for any phase.
      for (final phase in const ['idea', 'lit-review', 'method', 'experiment', 'paper']) {
        final tiles = resolveTilesForPhase(templateId: 'research', phase: phase);
        // Loose check: TileSlug enum has no `reviews` value at all.
        expect(tiles.toSet().intersection(TileSlug.values.toSet()), tiles.toSet());
      }
      expect(TileSlug.values.any((s) => s.name == 'reviews'), isFalse);
    });
  });

  group('resolveTilesForPhase — fallback', () {
    test('unknown phase → chassis default [Outputs, Documents]', () {
      expect(
        resolveTilesForPhase(templateId: 'whatever', phase: 'oddball'),
        [TileSlug.outputs, TileSlug.documents],
      );
    });

    test('empty phase → chassis default [Outputs, Documents]', () {
      expect(
        resolveTilesForPhase(templateId: '', phase: ''),
        [TileSlug.outputs, TileSlug.documents],
      );
    });
  });

  group('resolveTilesForPhase — phaseTilesYaml override (W7 hook)', () {
    test('YAML mapping wins over hardcoded research map', () {
      final out = resolveTilesForPhase(
        templateId: 'research',
        phase: 'experiment',
        phaseTilesYaml: const {
          'experiment': ['Risks', 'Discussion'],
        },
      );
      expect(out, [TileSlug.risks, TileSlug.discussion]);
    });

    test('unknown YAML slug is silently dropped (closed registry)', () {
      final out = resolveTilesForPhase(
        templateId: 'research',
        phase: 'method',
        phaseTilesYaml: const {
          'method': ['Outputs', 'Bogus'],
        },
      );
      expect(out, [TileSlug.outputs]);
    });
  });

  group('tileSpecFor', () {
    test('every slug has a non-empty label and subtitle', () {
      for (final s in TileSlug.values) {
        final spec = tileSpecFor(s);
        expect(spec.label, isNotEmpty, reason: 'slug=$s');
        expect(spec.subtitle, isNotEmpty, reason: 'slug=$s');
      }
    });
  });
}
