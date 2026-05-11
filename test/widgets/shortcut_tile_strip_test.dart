import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/shortcut_tile_strip.dart';

void main() {
  group('resolveTilesForPhase — research-template hardcoded safety-net map', () {
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

    test('idea phase → [Documents] (v1.0.483 — director needs path to memos)', () {
      expect(
        resolveTilesForPhase(templateId: 'research', phase: 'idea'),
        [TileSlug.documents],
      );
    });

    test('Reviews is never in any default tile set (gap #4)', () {
      for (final phase in const [
        'idea',
        'lit-review',
        'method',
        'experiment',
        'paper'
      ]) {
        final tiles = resolveTilesForPhase(templateId: 'research', phase: phase);
        expect(tiles.toSet().intersection(TileSlug.values.toSet()),
            tiles.toSet());
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

  group('resolveTilesForPhase — W5 resolution chain (override > template > safety-net)',
      () {
    test('phaseTileOverrides wins over phaseTilesTemplate', () {
      final out = resolveTilesForPhase(
        templateId: 'research',
        phase: 'method',
        phaseTileOverrides: const {
          'method': ['Outputs'],
        },
        phaseTilesTemplate: const {
          'method': ['References', 'Documents'],
        },
      );
      expect(out, [TileSlug.outputs]);
    });

    test('phaseTilesTemplate wins over hardcoded safety-net map', () {
      final out = resolveTilesForPhase(
        templateId: 'research',
        phase: 'method',
        phaseTilesTemplate: const {
          'method': ['Risks', 'Discussion'],
        },
      );
      expect(out, [TileSlug.risks, TileSlug.discussion]);
    });

    test('unknown override slug is silently dropped (closed registry)', () {
      final out = resolveTilesForPhase(
        templateId: 'research',
        phase: 'method',
        phaseTileOverrides: const {
          'method': ['Outputs', 'Bogus'],
        },
      );
      expect(out, [TileSlug.outputs]);
    });

    test('phaseTilesYaml deprecated alias still works as template tier', () {
      // ignore: deprecated_member_use_from_same_package
      final out = resolveTilesForPhase(
        templateId: 'research',
        phase: 'method',
        phaseTilesYaml: const {
          'method': ['Plans'],
        },
      );
      expect(out, [TileSlug.plans]);
    });
  });

  group('parsePhaseTilesMap', () {
    test('parses well-formed map', () {
      final out = parsePhaseTilesMap({
        'idea': ['Documents'],
        'lit-review': ['References', 'Documents'],
      });
      expect(out, {
        'idea': ['Documents'],
        'lit-review': ['References', 'Documents'],
      });
    });

    test('returns null for null / non-map inputs', () {
      expect(parsePhaseTilesMap(null), isNull);
      expect(parsePhaseTilesMap('not-a-map'), isNull);
      expect(parsePhaseTilesMap(42), isNull);
    });

    test('returns null for empty map', () {
      expect(parsePhaseTilesMap(<String, dynamic>{}), isNull);
    });

    test('drops non-string slugs', () {
      final out = parsePhaseTilesMap({
        'idea': ['Documents', 42, null],
      });
      expect(out, {
        'idea': ['Documents'],
      });
    });

    test('drops phases whose list parses to empty', () {
      final out = parsePhaseTilesMap({
        'idea': [42, null],
        'lit-review': ['References'],
      });
      expect(out, {
        'lit-review': ['References'],
      });
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
