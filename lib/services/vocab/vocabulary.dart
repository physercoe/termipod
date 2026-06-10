import 'vocab_axis.dart';
import 'vocab_packs.dart';
import 'vocab_preset.dart';
import 'vocab_term.dart';

/// Resolves role-bound terms for one `(preset, language)` (ADR-048).
///
/// Resolution is fallback-tolerant so a missing pack or axis never throws:
/// requested `(preset, language)` → `(preset, 'en')` → `(tech, language)` →
/// `(tech, 'en')`. `(tech, 'en')` is complete by invariant
/// (`vocab_pack_test.dart`), so [term] always returns.
class Vocabulary {
  final VocabPreset preset;

  /// `'en'` or `'zh'`. Any other value resolves through the `'en'` fallback.
  final String language;

  const Vocabulary(this.preset, this.language);

  /// The base pack: `(tech, 'en')`, guaranteed complete.
  static Map<VocabAxis, VocabTerm> get _base =>
      kVocabPacks[VocabPreset.tech]!['en']!;

  VocabTerm term(VocabAxis axis) {
    final byLang = kVocabPacks[preset];
    final pack = byLang?[language] ??
        byLang?['en'] ??
        kVocabPacks[VocabPreset.tech]?[language] ??
        _base;
    return pack[axis] ?? _base[axis]!;
  }

  // --- Convenience getters for the most-used title-case singulars. Call
  //     `term(axis)` directly when a non-title form (lower / plural) is
  //     needed. ---

  String get steward => term(VocabAxis.roleSteward).title;
  String get agent => term(VocabAxis.roleAgent).title;
  String get principal => term(VocabAxis.rolePrincipal).title;
  String get council => term(VocabAxis.roleCouncil).title;
}
