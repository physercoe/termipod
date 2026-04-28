# Code as a first-class artifact

> **Status: DEFERRED (2026-04-26).** Captured as a gap; not on the
> active workband. Code-review surfaces in mobile need real-app
> research that hasn't happened yet — what does diff-on-mobile feel
> like in GitHub Mobile vs. Codex superapp vs. Cursor mobile? — and
> a reference scan we can match against. Until that's done this doc
> is preserved as a sketch, not a spec.
>
> **Active focus instead:** steward wedges (transcript, approvals,
> sessions, hub-meta separation) — the band that lets a user replace
> Happy / CloudCLI for daily work. See
> `docs/plans/transcript-ux-comparison.md` and
> `docs/sessions.md`.

> Originally drafted: a sketch for discussion. Not implementable
> from this doc as written. Captures the gap, names the missing
> primitive, and lists open questions.

## 1. Why this doc exists

`docs/sessions.md` §5.1 lists the artifacts that constitute
the system's durable memory: templates, briefings, decisions,
plans, policies, attention-resolutions, run metric digests, member
directory.

**Code is missing from that list, and it shouldn't be.** Today an
agent that does coding work leaves bytes on disk in a worktree, may
write a row in `artifacts` (path-based), and may post a turn-result
that mentions what it changed — but there is no first-class "the
agent produced this code" artifact that the user can review,
approve, link to a decision, or roll back. That is a real gap for a
harness whose primary use case is AI/CS research and software
building (blueprint §1).

## 2. What makes code a special artifact

Compared to the existing artifact kinds, code has properties they
don't:

| Property | Generic doc / brief | Code |
|---|---|---|
| Free-form text | yes | no — it has a grammar |
| Mutated as a stream | rare (briefs are usually rewritten) | constant — every save / commit is a delta |
| Has an external lineage system | no | yes — git, with commits as an audit trail of its own |
| Composes with runtime artifacts | weakly (a brief can cite a run) | strongly — same code is what produced the run |
| Reviewable line-by-line | optional | the standard review unit |
| Can be tested / executed | no | yes |
| Can fail in production | no | yes |
| Has authorship attribution | doc author | author + reviewer + agent attribution all matter |
| Deletable safely | usually yes | only via the lineage (revert, drop branch) |

Treating code as "just another file artifact" loses every row in this
table. Hence: a separate primitive.

## 3. What we have today

- **Worktrees**: blueprint §6 + `agent-lifecycle.md` describe per-agent
  git worktrees (`worktrees` table) where an agent's edits land. The
  harness owns the directory; the agent has scoped write access.
- **`artifacts` table**: a generic primitive for "this agent produced
  this file" — but path-flat, no diff awareness, no PR link, no
  test-result link.
- **Audit log**: captures `kind=spawn`, `kind=task`, but no `kind=code_change`.
- **No PR / review surface in mobile.** Browse-files exists in
  Settings (file manager via SFTP) but isn't tied to a code-review
  flow.

So the bytes exist; the *meaning* doesn't.

## 4. Sketch of the missing primitive

Tentative shape — call it a `CodeChange` artifact:

```
CodeChange {
  id              UUID
  agent_id        FK agents
  session_id      FK sessions (when steward-sessions ships)
  worktree_id     FK worktrees
  base_commit     SHA (the worktree's branch base when the change started)
  head_commit     SHA (current tip; may be the same as base if uncommitted)
  branch          name
  files           [{ path, lines_added, lines_removed, status }]
  summary         model-written one-liner (e.g., "Add /v1/_info build metadata")
  body            model-written markdown ("Why" + "What changed")
  pr              { provider: github, url, number, status } | null
  test_results    [{ kind, passed, log_url, ran_at }]
  status          draft | proposed | approved | merged | reverted
  proposed_at     ts
  approved_at     ts | null
  approver_id     FK auth_tokens | null
  created_at, updated_at
}
```

Two important things this enables:

1. **Code becomes reviewable in the app.** The mobile UI gets a
   "Code" tab on a project (peer to "Documents" / "Outputs") showing
   draft CodeChanges, proposed CodeChanges awaiting approval, merged
   CodeChanges as a chronological log. Tapping shows a diff with
   syntax highlighting and the model's "why" body.

2. **Code participates in the decision tier system** (§6.5 of
   `sessions.md`). A CodeChange transitioning `proposed →
   approved` is a Significant or Strategic decision depending on:
   - branch (main vs. feature)
   - merge target (the `pr` link)
   - external effects (deploy chain wired to the branch)

This collapses today's vague "the agent edits files and we don't
really know what" into a clean review surface that matches how
human teams already work with code.

## 5. Where the diff comes from

Three options, increasing in fidelity:

### 5.1 Compute on demand from the worktree
At read time, run `git diff base_commit..head_commit` in the
host-runner. Cheap, always fresh, but requires the host-runner to be
reachable when the user opens the diff.

### 5.2 Persist diffs as blobs
At write time (commit, or on a per-edit basis), store the patch as a
hub `blobs` row, linked from the CodeChange. Survives host going
offline. More plumbing.

### 5.3 Persist commits as semantic objects
Beyond patches, store an extracted summary: function/symbol-level
changes ("modified `handleInfo`", "added field `runner_commit` to
`hostOut`"). Useful for browse / search, expensive to compute.

Tentative: 5.1 for v1 (compute on demand), 5.2 for offline, 5.3
post-MVP if the search/browse use case matures.

## 6. Where the PR / review flow lives

### 6.1 GitHub-native flow
The agent runs `gh pr create` (or equivalent) → CodeChange.pr is
populated → mobile shows PR status + reviewers + checks alongside
the diff. Approval inside our app posts a `gh pr review --approve`.

### 6.2 Hub-native flow
Some teams won't be on GitHub (research labs, internal infra). The
hub itself can be the review surface — CodeChange.status transitions
gated by hub approval, and merge is "host-runner runs git merge".
This is what we'd ship for the "vendor-agnostic" wedge of the
positioning memo.

Tentative: support both. PR.provider field discriminates.

## 7. Tests as siblings, not substitutes

A CodeChange should reference test runs (the `test_results` list).
Common pattern: an agent writes code, runs tests, gets results,
includes them in the CodeChange before marking `proposed`. The
director sees "code + test result" as one unit and decides.

This is also the plug-point for CI: when CI finishes a run on the
proposed branch, the CodeChange auto-attaches the result. Before
W1.A inline approval, this becomes the *content* of the approval
card for code merges: diff summary + test pass/fail + link to logs.

## 8. Authorship and identity

Every CodeChange has three identities to track:

- **Authoring agent** — the spawn that wrote the code.
- **Driving session** — the steward session in which the work was
  scoped (when steward-sessions ships).
- **Approving director** — the human who said yes.

`git`'s commit author/committer fields can carry agent identity if
we configure them at agent boot. The hub layer adds the session and
approver linkages (since git doesn't know about either).

## 9. Open questions

1. **Granularity.** Is one CodeChange per commit, per branch, per
   PR, or per "logical change"? Tentative: per branch is the
   user-meaningful unit; commits are sub-rows. PRs link to CodeChange,
   not the other way around. ("This branch produced these N
   commits, became this PR.")
2. **What about uncommitted edits?** A draft CodeChange can exist
   without commits — files modified in the worktree, captured as a
   pending diff. Status = `draft` until the agent commits.
3. **Long-running agents that touch many branches.** Steward might
   spawn 5 worker spawns, each on its own branch, each producing a
   CodeChange. The steward session's distillation lists all 5 as
   referenced artifacts.
4. **Reverts and conflicts.** A merged CodeChange that needs to be
   reverted produces a *new* CodeChange (the revert), linked back.
   Conflicts during merge need a UI flow we don't have today.
5. **How does this differ from the existing `artifacts` table?**
   The existing `artifacts` is generic file-level. CodeChange is
   semantic-level. Either we extend artifacts (add code-shaped
   columns, keep one table) or we add a separate table that
   references artifacts for the underlying file storage. Tentative:
   separate table, references files in the worktree by path; we
   don't try to mirror file bytes in the hub.
6. **Multi-language / multi-repo.** If an agent works across two
   worktrees (e.g. mux-pod + a sibling repo), is that one CodeChange
   or two? Tentative: two (one per worktree); a session can list
   both as referenced artifacts.
7. **Search and discovery.** Browsing CodeChange history per project
   is straightforward. Cross-project ("find all CodeChanges that
   touched this dependency") requires indexing we don't have today.
8. **Token cost when loading into a session.** Code is the most
   token-heavy artifact kind. Sessions should load *summaries +
   diffs of recent CodeChanges*, not full bodies. Default limits
   matter here even more than for briefs.

## 10. What this enables (motivating use cases)

- **"Review my agent's work without leaving the app."** Director
  taps a CodeChange in the project's Code tab, sees the diff, tests
  passed, the agent's why-summary, approves. Push.
- **"What did the agent do yesterday?"** Activity feed shows
  CodeChange transitions (proposed / approved / merged / reverted)
  alongside other audit events.
- **"Cite this change in a decision."** Decision artifacts
  (`sessions.md` §6) can reference CodeChange ids the same
  way they reference any other artifact.
- **"Trust-but-verify on autonomy."** In a Significant-tier session
  where commits don't ask, the digest at close lists the
  CodeChanges produced. Director skims, approves the batch or
  reverts the bad ones.

## 11. Where this fits on the roadmap

This is a multi-week wedge, dependent on:

- **Decision tiers** (§6.5 of `sessions.md`) — CodeChange
  approvals route through tier infrastructure.
- **Inline approval card** (W1.A from `transcript-ux-comparison.md`)
  — that wedge ships first; CodeChange becomes a richer payload
  inside the same card pattern.
- **Sessions** (the larger lift in `sessions.md`) — useful
  but not blocking; `session_id` can be NULL on CodeChange until
  sessions exist, then populated.

Suggested sequencing:

1. Inline approval card (W1.A) ships first with text-only payloads.
2. CodeChange table + diff-on-demand endpoint lands; mobile shows
   diffs in the inline card when `kind=code_change`.
3. PR linkage (GitHub provider) wires up.
4. Hub-native review flow (vendor-agnostic provider).
5. Test-result attachment.
6. Cross-project search.

## 12. What we don't do

- **In-app code editing.** Reviewing yes; authoring no. The user
  edits via SSH terminal or local IDE; the harness is for direction
  and review, not authoring (per the IA director-not-operator memory).
- **Storing full source in the hub.** The hub stores
  metadata + diffs; bytes live in the host's worktree (and `git` on
  whichever remote it pushes to). This keeps the data-ownership law
  intact (blueprint §4).
- **Replacing GitHub.** PRs / reviews on GitHub remain canonical for
  teams that use it; we're a mobile-first surface that links to
  them, not a replacement.

— draft 1, 2026-04-26
