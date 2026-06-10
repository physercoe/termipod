/// One resolved term on a vocabulary axis, carrying the grammatical forms a
/// call site may need.
///
/// English supplies all four forms (the defaults derive the regular cases so
/// authoring stays terse — see `_en` in vocab_packs.dart). Chinese has no
/// case or plural inflection, so a single string fills every form
/// (`VocabTerm('管家')`).
class VocabTerm {
  /// Title-case singular, e.g. "Steward". Always provided.
  final String title;

  final String? _lower;
  final String? _plural;
  final String? _pluralLower;

  const VocabTerm(
    this.title, {
    String? lower,
    String? plural,
    String? pluralLower,
  })  : _lower = lower,
        _plural = plural,
        _pluralLower = pluralLower;

  /// Lower-case singular, e.g. "steward". Defaults to [title] (correct for zh
  /// and for acronyms like "PI" that must not be down-cased).
  String get lower => _lower ?? title;

  /// Title-case plural, e.g. "Stewards". Defaults to [title] (zh).
  String get plural => _plural ?? title;

  /// Lower-case plural, e.g. "stewards". Defaults to [lower].
  String get pluralLower => _pluralLower ?? lower;
}
