# Coordinate agents through GitHub

> **Type:** how-to
> **Status:** Current (2026-06-11)
> **Audience:** contributors (humans + AI agents)
> **Last verified vs code:** v1.0.817

**TL;DR.** Multiple AI coding agents on different hosts collaborate on this
repo using **GitHub as the only shared channel**. Issues are the work queue,
labels are the state machine, branches/PRs are the work units, CI is the
gate. One **maintainer** decomposes work into specced **tickets** and is the
sole merger; any number of **builders** (any vendor or model) claim tickets,
implement them on their own host, and open PRs for review. The protocol is
**vendor-agnostic** — agents are identified by their GitHub account, never by
model name.

---

## 1. Roles

- **Maintainer** — holds merge authority. Decomposes work into tickets,
  writes each ticket's spec, reviews PRs, merges, handles judgment calls
  (architecture, vocabulary/glossary, ADRs, tricky traps), and unblocks
  builders. There is exactly one merge authority at a time.
- **Builder** — any agent (any model/vendor) that claims and implements a
  ticket. A builder never merges and never decides policy; it executes a
  spec and responds to review.

Roles are not vendors. A new CLI or model joins by configuring a GitHub
identity (§2) and reading this doc and `AGENTS.md` — no code or label
changes, no name baked anywhere.

## 2. Identity (operator setup, per host)

Each builder runs on its own host with its **own GitHub account**, so every
commit and claim is attributable. The operator configures this once per host
— it is **not** set by ticket specs:

```bash
git config user.name  "<handle>"          # the builder's GitHub login
git config user.email "<handle>@users.noreply.github.com"
gh auth login                              # as that same account
```

`<handle>` is whatever GitHub login the operator assigned that agent.
Builders add a `Co-Authored-By: <handle> <email>` trailer to their commits.

## 3. The ticket lifecycle

> **Terminology.** A *ticket* here is a GitHub issue specced for delegation —
> the issue-tracker sense. It is distinct from the product **Task** primitive
> (steward-dispatched work, ADR-029); see the
> [glossary](../reference/glossary.md).

State is carried by labels:

| Label | Meaning |
|---|---|
| `ticket:ready` | specced and unclaimed — eligible to pick up |
| `ticket:claimed` | a builder has taken it (see §4) |
| `ticket:in-review` | PR open, CI green, awaiting maintainer review |
| `ticket:changes` | maintainer requested changes — back to the builder |
| `ticket:blocked` | builder is stuck; needs maintainer attention |
| `tier:mechanical` / `tier:medium` / `tier:judgment` | capability required |
| `holds:arb` | the baton — see §6 |

A ticket closes when its PR merges (`Closes #N`).

```
ticket:ready ──claim──▶ ticket:claimed ──PR+green──▶ ticket:in-review
     ▲                                                      │
     │                                            maintainer review
     └──────── ticket:changes ◀───────────────────────┬────┘
                                                       └──▶ merged (Closes #N)

  ticket:blocked  ─── maintainer resolves ──▶ back to ready / claimed
```

Capability tiers describe the *work*, not the agent. An operator tells its
builder at launch which tiers it may take (e.g. "mechanical only").

## 4. Claiming (collision-free)

A builder claims a `ticket:ready` issue at a tier it is cleared for:

1. self-assign: `gh issue edit <N> --add-assignee @me`
2. relabel: `--add-label ticket:claimed --remove-label ticket:ready`
3. comment with an ETA.

Rules:

- **One open PR per builder** at a time — keeps the review queue sane.
- **2-hour claim TTL.** If no PR is open within 2h, any builder may reclaim
  (re-assign, reset the label). The GitHub **assignee** is the source of
  truth for who holds a ticket — there are no per-vendor labels.

## 5. Doing the work

1. Branch off `main`: `agent/<handle>/<N>-<slug>`.
2. Implement **exactly** per the ticket spec. Follow the reference PR it cites.
3. **Self-verify before review** (§7).
4. Open a PR: title per the spec, body `Closes #<N>`, then set
   `ticket:in-review` and request review from the maintainer.
5. Address `ticket:changes` rounds on the same branch.

**Never merge** — that is the maintainer's sole action.

## 6. The ARB baton (hot-file serialization)

`lib/l10n/app_en.arb` / `app_zh.arb` are append-only files every i18n ticket
edits, so parallel PRs conflict. Exactly **one** in-flight ticket may hold
the `holds:arb` baton:

- Before opening a PR that touches `lib/l10n/*.arb`, check no open ticket
  holds `holds:arb`. If free, add it to your ticket; if held, wait.
- The baton releases when your PR merges (or you drop the ticket).
- Tickets that don't touch ARB ignore the baton and parallelize freely.

This generalizes to any other hot file a future workload reveals: name it, give
it a `holds:<file>` baton, serialize.

## 7. Verify before requesting review

A builder must confirm green *itself* before handing a PR to the maintainer:

1. Run the local gate the ticket names (e.g. `bash scripts/lint-arb.sh`).
2. Push; wait for all CI checks to finish.
3. **Re-read `gh pr checks <PR>` and confirm every row says `pass`** — do not
   trust the `--watch` exit code (it lies on some races).
4. Only then set `ticket:in-review`.

The maintainer re-verifies the same way before merging. Merges happen only on
CI green (re-checked) **plus** maintainer approve.

## 8. Escalation — don't guess

If anything is ambiguous (which vocabulary axis a noun takes, an
ICU/placeholder trap, an unexpected test failure, a spec that doesn't match
the code), the builder sets `ticket:blocked`, comments with the specific
question, and stops. Guessing on judgment calls is what the tier system
exists to prevent. The maintainer resolves and flips it back to `ticket:ready`
or `ticket:claimed`.

## 9. Maintainer one-time setup

Create the labels (idempotent):

```bash
bash scripts/setup-agent-labels.sh
```

Write tickets from the
[agent-task issue template](../../.github/ISSUE_TEMPLATE/agent-task.md). Keep
each spec near-mechanical so a `tier:mechanical` builder can one-shot it: name
the exact files, cite a reference PR to copy, list the rules and the deferral
set, and give the verify commands.

---

## See also

- [`AGENTS.md`](../../AGENTS.md) — the bootstrap a builder reads first.
- [Localize a user-facing string](localize-a-string.md) — the recipe most
  mechanical i18n tickets follow.
- [`CONTRIBUTING.md`](../../CONTRIBUTING.md) — the human contributor guide.
