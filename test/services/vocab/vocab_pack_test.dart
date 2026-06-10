import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/vocab/vocab_axis.dart';
import 'package:termipod/services/vocab/vocab_packs.dart';
import 'package:termipod/services/vocab/vocab_preset.dart';
import 'package:termipod/services/vocab/vocabulary.dart';

// ADR-048 — the vocabulary-preset runtime. These pin the pack-completeness
// invariant (also enforced offline by scripts/lint-vocab.sh) plus the
// resolution + fallback contract that call sites depend on.
void main() {
  const langs = ['en', 'zh'];

  group('pack completeness', () {
    test('every (preset, language) pack defines every axis', () {
      for (final preset in VocabPreset.values) {
        for (final lang in langs) {
          final pack = kVocabPacks[preset]?[lang];
          expect(pack, isNotNull,
              reason: 'missing pack ${preset.id}/$lang');
          for (final axis in VocabAxis.values) {
            expect(pack![axis], isNotNull,
                reason: 'pack ${preset.id}/$lang missing ${axis.id}');
          }
        }
      }
    });

    test('tech/en is the complete fallback base', () {
      final base = kVocabPacks[VocabPreset.tech]!['en']!;
      expect(base.length, VocabAxis.values.length);
    });
  });

  group('resolution', () {
    test('headline role terms resolve per preset (en)', () {
      String steward(VocabPreset p) =>
          Vocabulary(p, 'en').term(VocabAxis.roleSteward).title;
      expect(steward(VocabPreset.tech), 'Steward');
      expect(steward(VocabPreset.business), 'Manager');
      expect(steward(VocabPreset.political), 'Secretary');
      expect(steward(VocabPreset.research), 'Supervisor');
    });

    test('headline role terms resolve per preset (zh)', () {
      String principal(VocabPreset p) =>
          Vocabulary(p, 'zh').term(VocabAxis.rolePrincipal).title;
      expect(principal(VocabPreset.tech), '负责人');
      expect(principal(VocabPreset.business), '老板');
      expect(principal(VocabPreset.political), '领导');
      expect(principal(VocabPreset.research), '课题组负责人');
    });

    test('english grammatical forms compose (regular + irregular)', () {
      final research = Vocabulary(VocabPreset.research, 'en');
      final study = research.term(VocabAxis.entityProject);
      expect(study.title, 'Study');
      expect(study.lower, 'study');
      expect(study.plural, 'Studies');
      expect(study.pluralLower, 'studies');

      final steward = Vocabulary(VocabPreset.tech, 'en').term(VocabAxis.roleSteward);
      expect(steward.plural, 'Stewards');
      expect(steward.pluralLower, 'stewards');
    });

    test('acronym principal keeps case across forms', () {
      final pi = Vocabulary(VocabPreset.research, 'en').term(VocabAxis.rolePrincipal);
      expect(pi.title, 'PI');
      expect(pi.lower, 'PI');
      expect(pi.pluralLower, 'PIs');
    });

    test('zh terms collapse to a single form', () {
      final mgr = Vocabulary(VocabPreset.business, 'zh').term(VocabAxis.roleSteward);
      expect(mgr.title, '经理');
      expect(mgr.lower, '经理');
      expect(mgr.plural, '经理');
      expect(mgr.pluralLower, '经理');
    });
  });

  group('fallback', () {
    test('unknown language falls back to en for that preset', () {
      final v = Vocabulary(VocabPreset.business, 'fr');
      expect(v.term(VocabAxis.roleSteward).title, 'Manager');
    });

    test('convenience getters mirror term().title', () {
      final v = Vocabulary(VocabPreset.political, 'en');
      expect(v.steward, v.term(VocabAxis.roleSteward).title);
      expect(v.agent, 'Operative');
      expect(v.principal, 'Leader');
    });
  });

  group('preset id round-trip', () {
    test('fromId maps known ids and defaults to tech', () {
      for (final p in VocabPreset.values) {
        expect(VocabPreset.fromId(p.id), p);
      }
      expect(VocabPreset.fromId('nonsense'), VocabPreset.tech);
      expect(VocabPreset.fromId(null), VocabPreset.tech);
    });
  });
}
