# Localize a user-facing string

> **Type:** how-to
> **Status:** Current (2026-06-10)
> **Audience:** contributors
> **Last verified vs code:** v1.0.815

**TL;DR.** Every user-facing string goes through gen-l10n (en + zh).
**Role-bound** strings — ones naming a steward, agent, principal, project,
… — additionally route their noun through the **vocabulary preset**
([ADR-048], `lib/services/vocab/`) so it re-words per audience. This is the
migration pattern the #138 sweep (WS-C…H of
[the program plan](../plans/themed-vocabulary-and-i18n-sweep.md)) applies
file by file.

---

## 1. Decide: neutral or role-bound?

Check `docs/reference/vocabulary.md` §1. If the string names a concept on a
vocab axis (steward / agent / principal / council / team / project /
workspace / task / plan / run / …) it is **role-bound**. Otherwise — generic
UI, errors, SSH/tmux/terminal vocabulary, settings chrome — it is
**neutral** (~80% of strings).

> **Don't trust appearances — grep `lib/services/vocab/vocab_axis.dart`.** A
> noun that reads like a fixed type label can still have a `VocabAxis`
> (`entityDocument`, `entityRun`, … exist). If an axis matches the concept,
> the string is **role-bound** and must route through `vocab.term(axis)` — even
> a one-word label like "Document" or "Run". Hardcoding it as a plain ARB
> string means it won't re-word when the director themes their vocabulary.

## 2. Neutral string → plain ARB

1. Add the key to **both** `lib/l10n/app_en.arb` and `app_zh.arb`.
2. Read it with `final l10n = AppLocalizations.of(context)!;` → `l10n.key`.
3. Reuse an existing key before adding one — common actions already exist
   (`buttonCancel`, `buttonClose`, `buttonDelete`, `buttonSave`,
   `buttonCreate`, `buttonEdit`, …). Don't add `cancel2`.

```dart
// before
const Text('Cancel')
// after
Text(l10n.buttonCancel)
```

Placeholders use ICU and must be declared on the template (en) side:

```json
"closeSessionContent": "Remove \"{sessionName}\" from active sessions?",
"@closeSessionContent": { "placeholders": { "sessionName": { "type": "String" } } }
```

The same `{placeholder}` set must appear in the zh value — `lint-arb.sh`
fails the build otherwise.

## 3. Role-bound string → ARB template + vocab term

The **sentence** is localized (en/zh); the **role noun** comes from the
active preset. Read the term and compose:

```dart
final voc = ref.watch(vocabularyProvider);          // ConsumerWidget / ref in scope
final s = voc.term(VocabAxis.roleSteward);
Text(stewardCategoryLabel(cat, steward: s.lower, stewards: s.pluralLower));
```

- `voc.steward / .agent / .principal / .council` give the title-case
  singular for simple labels; `voc.term(axis)` exposes `title / lower /
  plural / pluralLower` when grammar matters (English). zh collapses to one
  form, so the same call works in both languages.
- When a string is a *sentence* with the noun embedded, prefer an ARB
  template with a `{role}` placeholder filled by the term, so the
  surrounding words still localize:
  `l10n.noAgentsYet(voc.term(VocabAxis.roleAgent).pluralLower)`.

## 4. Keep context-free helpers pure (helper → enum)

Pure helpers (e.g. `sessions_list_controller.dart`) must stay widget-free so
they remain unit-testable. **Don't** reach for `AppLocalizations` or
`vocabularyProvider` inside them. Either return a stable enum the widget maps
at render, or take the resolved term forms as parameters with tech defaults:

```dart
// pure, testable, back-compatible
String stewardCategoryLabel(StewardCategory c,
    {String steward = 'steward', String stewards = 'stewards'}) { … }

// widget supplies the active preset's forms
final s = ref.watch(vocabularyProvider).term(VocabAxis.roleSteward);
stewardCategoryLabel(cat, steward: s.lower, stewards: s.pluralLower);
```

## 5. Key naming

`areaComponentRole`, lower-camelCase, grouped by surface:
`sessionsCategoryDetached`, `meAttentionSection`, `settingVocabPreset`. Encode
the vocab axis in the name where one applies (`stewardX`, `projectX`,
`agentX`) so the role-bound set is greppable.

## 6. Before you push

```bash
scripts/lint-arb.sh     # en/zh lockstep + placeholders (no Flutter needed)
scripts/lint-vocab.sh   # vocabulary-preset pack completeness
```

CI then runs `flutter analyze` + `flutter test` (gen-l10n regenerates from
the ARB; a malformed ARB fails the build). There is no local Flutter SDK —
rely on CI for the generated `AppLocalizations`.

## References

- [ADR-048] — themed vocabulary overlay (vocabulary presets).
- `docs/reference/vocabulary.md` — the 21 role-bound axes.
- `docs/plans/themed-vocabulary-and-i18n-sweep.md` — the sweep program.

[ADR-048]: ../decisions/048-themed-vocabulary-overlay.md
