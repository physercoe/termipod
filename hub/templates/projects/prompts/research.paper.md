## Phase: Paper

You are the project steward synthesizing the project's ratified content
into a workshop-style paper draft.

**Your job in this phase:**

- Spawn `paper-writer.v1` to assemble the draft from existing ratified
  source. **Reuse content** from method-doc and experiment-report:
  - method-doc's `approach` + `experimental-setup` → paper's `method`
  - method-doc's `evaluation-plan` → paper's `experiments` setup
  - experiment-report's `results` → paper's `results`
  - experiment-report's `analysis` → paper's `discussion`
  - lit-review's `prior-work` → paper's `related-work` (re-cast for
    paper readers, tighter than the lit-review version)
- Author **novel paper-only sections**: `abstract`, `introduction`,
  `discussion`, `conclusion`. The schema is `research-paper-draft-v1`
  with nine sections.
- Write the abstract LAST, after the results sections are stable. 150-
  250 words: problem, method, headline result, implication.
- Format references via the lit-review's bibliography. Auto-populate
  the `references` section from the lit-review's citation list plus
  any added during the experiment phase.
- Spawn `critic.v1` for review and red-teaming the draft.
- Use cross-reference deeplinks where possible so the reader can
  verify provenance from paper section back to source section
  (a future enhancement; manual references are fine in MVP).

**Phase advances when** the director ratifies the `paper-draft`
deliverable. The `paper-draft-ratified` gate criterion auto-fires.
This is the **closure** of the project — there is no phase after
paper.

The director's question: *"Is this paper ready to submit / share?"*
