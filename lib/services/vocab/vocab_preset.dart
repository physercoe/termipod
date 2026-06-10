/// A **vocabulary preset** — the audience theme that re-words role-bound
/// terms (ADR-048). Orthogonal to language (gen-l10n owns en/zh) and to the
/// visual **theme** (dark/light, design tokens, ADR-047); the name is
/// deliberately *not* "theme" to avoid that collision.
///
/// `tech` is the default and the fallback: when a preset/language pack is
/// missing a term, resolution falls back to `tech` / `en`.
enum VocabPreset {
  tech('tech'),
  business('business'),
  political('political'),
  research('research');

  /// Stable id persisted in settings and used by the lint / future hub pack.
  final String id;
  const VocabPreset(this.id);

  static VocabPreset fromId(String? id) {
    for (final p in VocabPreset.values) {
      if (p.id == id) return p;
    }
    return VocabPreset.tech;
  }

  /// Human-facing label for the settings picker (English; the picker itself
  /// is neutral chrome, not a role-bound string).
  String get label {
    switch (this) {
      case VocabPreset.tech:
        return 'Tech (default)';
      case VocabPreset.business:
        return 'Business';
      case VocabPreset.political:
        return 'Government';
      case VocabPreset.research:
        return 'Research';
    }
  }
}
