<!-- Replace each section with concrete content. Delete sections that don't apply. -->

## What changed

<!-- 1-3 bullets. The "what," not the "why" — that goes below. -->

-

## Why

<!-- The problem this solves or the gap it closes. Link to the issue,
ADR, plan, or memory entry that motivated the change. -->

-

## Verification

<!-- How you tested. For mobile changes, list the device walkthrough
steps. For backend, the test commands. -->

- [ ] Tests pass locally / in CI
- [ ] Manual verification (describe):

## Doc / spec updates

<!-- Per docs/doc-spec.md §8, a code change that touches CLI flags,
endpoints, schema, or contracts should update the related doc in the
same PR. Per memory feedback_update_docs_with_api_changes, this is
the rule. -->

- [ ] No doc impact
- [ ] Updated affected doc(s) and bumped `Last verified vs code` line
- [ ] Created/updated an ADR in `docs/decisions/` (architectural
      decisions only)
- [ ] Added an entry to `docs/changelog.md` (user-visible change)

## Term consistency (doc-spec §7)

<!-- Glossary at docs/reference/glossary.md is canonical for every
project-specific term that has more than one possible meaning. New
terms must be added in the same commit; first-use occurrences in
prose link to the glossary entry. CI lint enforces this — run
scripts/lint-glossary.sh locally to pre-check. -->

- [ ] No new project-specific term
- [ ] Added new term(s) to `docs/reference/glossary.md` (with
      `Distinguish from:` line if collisions exist + entry in §12)
- [ ] First-use linked in any new doc prose

## Memory / context

<!-- If this PR establishes a durable lesson worth remembering across
sessions, write it to memory. Per feedback_session_distillation:
save from success, not just corrections. -->

- [ ] Not applicable
- [ ] Added/updated memory entry: `<name>.md`

## Anything else
