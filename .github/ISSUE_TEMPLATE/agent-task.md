---
name: Agent ticket (delegated work)
about: A specced unit of work for a builder agent to claim and implement.
title: ''
labels: ticket:ready
assignees: ''
---

<!--
Maintainer fills this in. Keep it near-mechanical so a tier:mechanical builder
can one-shot it. See docs/how-to/agent-collaboration.md §9.
Add a tier label (tier:mechanical | tier:medium | tier:judgment).
-->

## Goal
<!-- one line -->

## Tier
<!-- tier:mechanical | tier:medium | tier:judgment -->

## Baton
<!-- "holds:arb required (touches lib/l10n/*.arb)" or "none" -->

## Pattern
<!-- e.g. "Follow merged PR #196 exactly." -->

## Files
-

## Rules
-

## Verify (before requesting review)
1. <!-- local gate, e.g. `bash scripts/lint-arb.sh` -->
2. push branch `agent/<handle>/<N>-<slug>`
3. all CI checks green
4. re-check `gh pr checks <PR>` — confirm every row says `pass`

## Definition of done
- PR open, `Closes #<N>`, CI green, label set to `ticket:in-review`.

## Escalation
- If anything is ambiguous → set `ticket:blocked` + comment your question.
  Do not guess.
