/// The role-bound vocabulary axes (ADR-048, `docs/reference/vocabulary.md` §1).
///
/// Each axis is a concept whose wording shifts together when the active
/// **vocabulary preset** changes (tech / business / political / research).
/// Strings inside one axis always co-vary — if `roleSteward` becomes
/// "Manager", every reference to the steward in that preset follows.
///
/// This list is the single source of truth for the set of swappable terms.
/// `kVocabPacks` (vocab_packs.dart) must define every axis here for all 8
/// (preset × language) packs; `vocab_pack_test.dart` and `lint-vocab.sh`
/// enforce that completeness.
enum VocabAxis {
  // Roles
  roleSteward('role.steward'),
  roleAgent('role.agent'),
  rolePrincipal('role.principal'),
  roleCouncil('role.council'),

  // Entities
  entityTeam('entity.team'),
  entityProject('entity.project'),
  entityWorkspace('entity.workspace'),
  entityTask('entity.task'),
  entityPlan('entity.plan'),
  entityRun('entity.run'),
  entitySchedule('entity.schedule'),
  entityTemplate('entity.template'),
  entityChannel('entity.channel'),
  entityReview('entity.review'),
  entityDocument('entity.document'),
  entityOutput('entity.output'),

  // Surfaces
  surfaceAttention('surface.attention'),
  surfaceApproval('surface.approval'),
  surfaceDirective('surface.directive'),
  surfaceBrief('surface.brief'),

  // Borderline-technical, kept neutral in practice but still an axis so a
  // preset *may* override it (vocabulary.md §1 treats `entity.host` as
  // neutral by default).
  entityHost('entity.host');

  /// Stable dotted id, e.g. `role.steward`. Used by docs, the lint, and any
  /// future hub-served pack so call sites never depend on enum ordering.
  final String id;
  const VocabAxis(this.id);
}
