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
**vendor-agnostic** — agents are identified by a `git config` **handle**,
never by model name, and builders may share a single GitHub account.

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

## 2. Identity — two separate axes

Identity splits into two axes that are easy to conflate. Only one of them
costs a GitHub account, so the account count stays **constant** no matter how
many agents you run.

- **Attribution** — *which agent wrote this* — is pure commit metadata, set
  per host with `git config`. No account needed; scales to any number of
  agents for free.
- **Acting account** — *who pushes, opens the PR, can approve/merge* — is
  decided by the auth credential (token), **not** by `git config`. A token
  always acts as the account that owns it.

**Operator setup, per host** (not set by ticket specs):

```bash
# Attribution — a distinct handle per agent (free, no account):
git config user.name  "<handle>"          # e.g. builder-1; how we tell agents apart
git config user.email "<handle>@users.noreply.github.com"

# Acting account — authenticate gh with a token. All builders MAY share a
# single builder account/token; the maintainer uses a different account.
gh auth login
```

`<handle>` is the agent's attribution handle (any string the operator picks)
— it appears in the branch name and the claim comment, and is how we
distinguish agents. Builders add a `Co-Authored-By: <handle> <email>` trailer
to their commits.

Because `git config` carries attribution, **builders can share one GitHub
account** (one token) — you do not create an account per agent. Keeping the
builder account distinct from the maintainer account is optional; it is only
required if you want the *enforced* approval gate (§7). With a single shared
account, maintainer-only merge stays a convention enforced by CI + review.

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

1. relabel: `gh issue edit <N> --add-label ticket:claimed --remove-label ticket:ready`
2. **comment naming your handle and an ETA** — e.g. `claiming as <handle>, ETA ~30m`.

The **claim comment (your `<handle>`) plus the branch name
(`agent/<handle>/<N>-…`) is the source of truth** for who holds a ticket — not
the GitHub assignee, which is unreliable when builders share one account. (You
*may* also self-assign, but the handle is what counts.)

Rules:

- **One open PR per handle** at a time — keeps the review queue sane.
- **2-hour claim TTL.** If no branch/PR exists within 2h of the claim comment,
  any builder may take it over: reset to `ticket:ready` is unnecessary — just
  drop a new claim comment with your own handle and proceed.

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

**Convention vs enforced gate.** With a single shared GitHub account (builders
and maintainer the same account), "maintainer-only merge" is a *convention* —
builders are told never to merge, and CI + maintainer review is the real
safety net (sufficient in practice). To make it an *enforced* gate, the
builder account must be **distinct** from the maintainer account; then enable
branch protection on `main` (Settings → Branches → require a pull request +
require 1 approval), and GitHub will block merge until the maintainer account
approves — a builder cannot approve its own PR. A token's permission scope
does **not** create this gate; only distinct accounts do.

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

## 10. A builder is a runtime plus a model

"Builder" names a **role**, not a program. Concretely a builder is two
interchangeable parts the operator chooses:

- a **runtime** — the agent harness that reads files, edits code, runs `git`
  and `gh`, and drives a loop; and
- a **model** — the LLM the runtime calls for reasoning.

Some CLIs bundle both (the runtime *is* the model's first-party tool). Other
runtimes are model-agnostic: they speak a provider API, so you can point them
at a **cheaper model** through that provider's endpoint — typically by setting
the runtime's base-URL / auth-token / model environment variables before
launch. Either shape is a valid builder; the protocol never sees the
difference. This is why §2 keeps identity (`git config` handle + shared
account) separate from *which* runtime or model is running — you can swap the
model under a builder without touching its handle, its account, or any ticket.

Pick the model tier to match the work tier (§3): a cheap model on
`tier:mechanical`, a stronger one when you clear a builder for `tier:medium`.

## 11. Running a builder autonomously

A builder need not be driven by a human typing a prompt per ticket. The
reference poller [`scripts/agent-poller.sh`](../../scripts/agent-poller.sh)
runs the loop on the builder's host:

1. it does the cheap GitHub orchestration in shell — finds a `ticket:ready`
   issue at a tier the builder is cleared for, claims it (relabel + handle
   comment, §4), and writes the standing prompt of §5;
2. it hands that prompt to **your** agent via the `$AGENT_CMD` you set — the
   one place a runtime/model is named, and only in the operator's environment,
   never in the repo;
3. the agent runs in the **foreground**, so the loop is one-in-flight by
   construction; and it won't claim more while this builder still has an open
   PR awaiting review.

```bash
export AGENT_HANDLE=builder-1          # = your git config user.name
export AGENT_TIERS=mechanical          # tiers this builder may take
export AGENT_CMD='<your headless agent invocation; prompt on stdin or $PROMPT_FILE>'
bash scripts/agent-poller.sh --dry-run --once   # inspect first
bash scripts/agent-poller.sh                    # then run the loop
```

The poller deliberately does **not** manage the `holds:arb` baton itself — the
agent does, per §6, before it opens an ARB PR. Run the script with `--help`
for the full configuration and safety notes.

A builder must edit files, run scripts, and reach the network (`git push`,
`gh`). If your runtime sandboxes each command (a per-command bubblewrap/seccomp
jail) it may fail before startup on restricted hosts — a telltale is a
network-namespace error such as `bwrap: loopback: Failed RTM_NEWADDR: Operation
not permitted`. On a **trusted** builder host, pass the runtime's own
bypass-sandbox / auto-approve flag in `$AGENT_CMD` (the poller never sandboxes
anything itself); the flag name is runtime-specific — check the runtime's
`--help`. Run the builder as a non-root user.

## 12. This protocol is general

Nothing here is specific to any one workload. The i18n/ARB sweep was the
proving pilot, but the same lifecycle, tiers, identity model, baton, and
verify-before-merge govern **any** delegatable work, for example:

- a mechanical refactor or rename across many files,
- test backfill for an under-covered package,
- dependency or API-migration bumps,
- a documentation sweep,
- generated-code regeneration.

Two things are workload-specific, and both live in the **ticket spec**, not in
this protocol: the **gate** the builder runs before review (e.g.
`scripts/lint-arb.sh` for i18n, `go test ./...` for a Go change — §7 just says
"the gate the ticket names"), and any **hot resource** that needs a baton (§6):
`holds:arb` is one instance; give a new workload's hot file its own
`holds:<resource>` by the same rule. See [ADR-049](../decisions/049-multi-agent-collaboration-via-github.md).

---

## See also

- [ADR-049](../decisions/049-multi-agent-collaboration-via-github.md) — the
  decision + rationale this how-to operationalizes.
- [`AGENTS.md`](../../AGENTS.md) — the bootstrap a builder reads first.
- [Localize a user-facing string](localize-a-string.md) — the recipe most
  mechanical i18n tickets follow (one example workload).
- [`CONTRIBUTING.md`](../../CONTRIBUTING.md) — the human contributor guide.
