# Contributor readiness — must-have governance + onboarding

> **Type:** plan
> **Status:** Draft (2026-05-05) — not yet started; sibling to [`doc-uplift.md`](doc-uplift.md)
> **Audience:** contributors (maintainers, doc authors)
> **Last verified vs code:** v1.0.351

**TL;DR.** Doc-uplift covers the *system-design* doc axis (arc42 / C4
/ Diátaxis). It does **not** cover the *contributor-experience* axis
— the GitHub-conventional governance files plus contributor-specific
how-tos that an external reviewer or contributor needs to clone, set
up, and submit a PR. Spot-check on 2026-05-05 confirmed: `LICENSE`
(Apache 2.0) ✅, root `README.md` ✅, `.github/pull_request_template.md`
✅ — but no `CONTRIBUTING.md`, `SECURITY.md`, issue templates,
local-dev-environment guide, or test-running guide. This plan ships
**5 must-have items** — 3 governance files + 2 contributor how-tos
(G2 `CODE_OF_CONDUCT.md` was deferred 2026-05-05). Runs in parallel
with doc-uplift; both gate the lifecycle plan's W1. Estimated effort:
~4–6 working days solo. License confirmed Apache 2.0; single-repo
(mobile + hub co-located in `mux-pod`).

---

## 1. Why this plan exists

The 2026-05-05 doc audit and resulting `doc-uplift.md` plan focused
on **system documentation** (architecture, schema, API, flows). A
follow-up question — "is the codebase contributor-ready?" — surfaced
a parallel gap: the **contributor-experience layer** (governance +
onboarding runbooks) is incomplete.

Why this matters for the MVP demo:

- **Reviewers may attempt a contribution.** A reviewer reading the
  docs may notice an issue and want to file it or fix it. If
  `CONTRIBUTING.md` doesn't exist, the path to contribute isn't
  legible. This is a basic open-source-project signal that mature
  reviewers will check.
- **Industry-grade includes governance.** arc42 / C4 / Diátaxis
  cover system design but not project governance. GitHub conventions
  (CONTRIBUTING / CODE_OF_CONDUCT / SECURITY / templates) are the
  governance layer.
- **AI agents inspecting the codebase look for these files first.**
  Many agent inspection patterns start with `LICENSE` →
  `README.md` → `CONTRIBUTING.md` → `.github/`. Gaps in this
  ordering surface as "this project may not be production-grade".

User decisions on 2026-05-05:
- Real gap, worth a sibling plan
- **MUST-HAVE only** (no nice-to-have items like CODEOWNERS, code
  review checklist, module-level READMEs)
- Single repo
- License is Apache 2.0 (confirmed)

---

## 2. Spot-check — what's already there

```
mux-pod/
├── LICENSE                                    ✅ Apache 2.0
├── README.md                                  ✅ 21 KB user-level
├── README.zh.md                               ✅ (translation)
├── README.ja.md                               ❌ (deferred — not blocking)
├── .github/
│   ├── pull_request_template.md               ✅ excellent (doc-spec aware)
│   ├── FUNDING.yml                            ✅
│   ├── dependabot.yml                         ✅
│   ├── workflows/
│   │   ├── ci.yml                             ✅
│   │   ├── codeql.yml                         ✅
│   │   ├── release.yml                        ✅
│   │   └── release-ios.yml                    ✅
│   └── ISSUE_TEMPLATE/                        ❌ (this plan adds)
├── CONTRIBUTING.md                            ❌ (this plan adds)
├── CODE_OF_CONDUCT.md                         ❌ (this plan adds)
├── SECURITY.md                                ❌ (this plan adds)
└── docs/
    └── how-to/
        ├── install-hub-server.md              ✅
        ├── install-host-runner.md             ✅
        ├── run-the-demo.md                    ✅
        ├── release-testing.md                 ✅
        ├── report-an-issue.md                 ✅ (tester-side)
        ├── local-dev-environment.md           ❌ (this plan adds)
        └── run-tests.md                       ❌ (this plan adds)
```

The existing PR template is a strong cultural signal — it explicitly
surfaces doc-spec, glossary discipline, and memory updates. The new
docs in this plan inherit that bar.

---

## 3. Scope — 6 must-have items

### 3.1 P0 — Governance files (3)

GitHub-conventional, repo-root (not under `docs/`):

- **G1** `CONTRIBUTING.md` — PR process, conventions, testing, doc requirements
- **G2** ~~`CODE_OF_CONDUCT.md`~~ — **deferred** (2026-05-05); skipped from this plan, may revisit
- **G3** `SECURITY.md` — vulnerability disclosure policy
- **G4** `.github/ISSUE_TEMPLATE/bug_report.md` + `feature_request.md`

### 3.2 P0 — Contributor how-tos (2)

Under `docs/how-to/`:

- **H1** `local-dev-environment.md` — clone → install Flutter + Go SDK → run hub locally → run mobile → run host-runner → run mock-trainer (the cold-start gap)
- **H2** `run-tests.md` — `flutter test` / `flutter analyze` / `go test` / lint scripts / CI parity

### 3.3 Out of scope (deferred per "must-have only")

| Item | Reason for deferral |
|---|---|
| `CODEOWNERS` | Single-maintainer; revisit when multi-maintainer |
| `README.ja.md` | Not blocking review or contribution |
| Code review checklist doc | PR template covers the load-bearing pieces |
| `how-to/release-cut-process.md` | `release-testing.md` covers testing the release; cut process is internal-only for now |
| `how-to/debug.md` | Activity feed + audit_events serve as primary debug surface; explicit guide can wait |
| `how-to/contribute-a-template.md` | A2 + A6 reference docs (post-doc-uplift) serve as authoring guides |
| `tutorials/03-first-contribution.md` | Doc-uplift's `tutorials/00-getting-started.md` covers user onboarding; contributor-onboarding tutorial deferred until first external contributor |
| Module-level READMEs (`lib/README.md`, `hub/README.md`) | Spine + reference docs are the entry points; module-level redirect can wait |
| Branch-naming convention | `main` is the only branch in active use; defer formalization |

These can land later; they don't block external review/contribution.

---

## 4. Sequencing + dependency graph

```
G1 CONTRIBUTING ──────┐                  (anchors process)
                       │
G3 SECURITY ───────────┤                  (independent)
                       │
G4 ISSUE_TEMPLATE ─────┤                  (independent; parallels G1)
                       │
                       ↓
                 ┌──────────────┐
                 │  H1 local-   │  ← G1 references it; can ship before
                 │  dev-env     │
                 └──────┬───────┘
                        │
                        ↓
                 ┌──────────────┐
                 │  H2 run-     │  ← G1 references it; can ship before
                 │  tests       │
                 └──────────────┘
```

**Critical path:** none strictly enforced; G1 references H1 + H2 so
those should land first or in same PR if ideal.

**Parallelism:** all 5 items can be authored in any order. G3/G4 are
independent. G1/H1/H2 cross-link; ship as a package.

**Solo path:** H1 → H2 → G1 → G4 → G3 (~4–6 days).

**Two-contributor path:** A on H1+H2 (the substantial how-tos);
B on G1+G3+G4 (the governance set). Joins at end. ~2.5 days.

---

## 5. Per-item specs

### 5.1 G1 — `CONTRIBUTING.md`

**Goal.** The PR contract. Everything a contributor needs to know to
make their first contribution land cleanly.

**File added.** `/CONTRIBUTING.md` (repo root)

**Content outline.**
1. Welcome — scope of contributions accepted (mobile / hub / docs /
   tests / examples)
2. License consent — Apache 2.0; no CLA required
3. Reporting issues — link to `.github/ISSUE_TEMPLATE/`; routing
   (bug vs feature vs question)
4. Setting up local development — link to
   `docs/how-to/local-dev-environment.md` (H1)
5. Branch and commit conventions:
   - Branch from `main`; PR back to `main`
   - Commit message: `<type>(<scope>): <subject>`
     - Types: `feat` / `fix` / `docs` / `refactor` / `test` /
       `chore` / `perf` / `ci`
     - Scope: short (e.g., `lifecycle`, `ui`, `hub`, `release`)
     - Subject: imperative, lowercase, no trailing period
6. Code style — link to `docs/reference/coding-conventions.md`
7. Testing — link to `docs/how-to/run-tests.md` (H2); expectation:
   tests pass, analyze clean, lint scripts pass
8. Doc requirements — link to `docs/doc-spec.md`; reference the PR
   template's doc-impact checklist
9. Submitting a PR — template fills, review expectations, typical
   response time (best-effort, no SLA)
10. After merge — release cadence (continuous tag-on-demand per
    `feedback_no_auto_release`), when changes ship to users
11. Project structure pointer — `docs/README.md` is the index; spine
    docs are `docs/spine/`; references at `docs/reference/`
12. Communication — issue tracker is primary; project Slack/Discord
    if applicable (TBD; defer if not present)

**Acceptance.**
- [ ] All 12 sections present
- [ ] Cross-links resolve (H1, H2, doc-spec, coding-conventions,
      glossary, PR template)
- [ ] Cite the PR template's doc-impact + glossary + memory checklists
- [ ] Lint clean if placed under docs/, otherwise GitHub renders correctly
- [ ] Reviewed against the 2026-05-05 PR template's expectations (no
      drift)

**Effort.** 1–2 days.

**Dependencies.** H1 + H2 should exist (or land in same PR) so
references aren't dangling.

### 5.2 G2 — `CODE_OF_CONDUCT.md` *(deferred 2026-05-05)*

Deferred from this plan; may revisit later. Sub-items, references,
and acceptance retained below for the eventual revisit but not in
scope for the current shipping wave.

### 5.3 G3 — `SECURITY.md`

**Goal.** Vulnerability disclosure policy.

**File added.** `/SECURITY.md` (repo root)

**Content outline.**
1. Supported versions — currently latest tag; pre-1.0 alpha policy
   (every alpha tag is the only supported version)
2. Reporting a vulnerability — preferred channel: **GitHub Security
   Advisory** (private). Fallback: a maintainer-monitored email
   (TBD — confirm with user before populating)
3. Response timeline — best-effort within 7 days for triage; no SLA
4. Disclosure policy — coordinated disclosure preferred; minimum
   30-day window for fix before public disclosure
5. Out of scope — issues that require physical device access; issues
   in third-party engines (Claude / Codex / Gemini) — report to
   those vendors

**Acceptance.**
- [ ] All 5 sections present
- [ ] GitHub Security Advisory enabled in repo settings (verify)
- [ ] Linked from `CONTRIBUTING.md` §3 + `README.md` license footer

**Effort.** 0.5 day.

**Dependencies.** None. Confirm preferred email contact with user
before populating.

### 5.4 G4 — Issue templates

**Goal.** Structured bug + feature request templates.

**Files added.**
- `.github/ISSUE_TEMPLATE/bug_report.md`
- `.github/ISSUE_TEMPLATE/feature_request.md`
- `.github/ISSUE_TEMPLATE/config.yml` — optional: disable blank
  issues to force template use

**Bug report template content.**
- Description (1–3 sentences)
- Reproduction (step-by-step)
- Expected behavior
- Actual behavior
- Environment:
  - Mobile OS + version (iOS / Android)
  - Hub version (from version_test endpoint or `hub --version`)
  - Network type (Tailnet / direct / etc.)
- Logs / Activity audit feed (paste relevant entries)
- Screenshots (if mobile UX issue)
- Pointer to `docs/how-to/report-an-issue.md` for the tester
  vocabulary guide

**Feature request template content.**
- Problem (motivation in user terms)
- Proposed solution
- Alternatives considered
- Scope (mobile / hub / agents / docs / multiple)
- Impact / who benefits
- Pointer to `docs/discussions/` for in-flight design conversations

**Acceptance.**
- [ ] Bug + feature templates render correctly when filing a new
      issue
- [ ] config.yml disables blank issues (optional but recommended)
- [ ] Templates include rendered preview links to docs

**Effort.** 0.5 day.

**Dependencies.** None.

### 5.5 H1 — `how-to/local-dev-environment.md`

**Goal.** Cold-start guide: clone → install all deps → run the full
stack locally → make a test change. The single most-load-bearing
contributor doc.

**File added.** `docs/how-to/local-dev-environment.md` (~300–400 lines)

**Content outline.**
1. Prerequisites
   - macOS / Linux / WSL2 (Windows native untested)
   - Flutter SDK 3.10+ (point at official install)
   - Dart 3.x (bundled with Flutter)
   - Go 1.22+ (per memory: `/usr/local/go/bin` may need PATH)
   - Android Studio / Xcode for mobile target
   - Docker (optional; for some host-runner test scenarios)
   - Network access to engine vendors (Claude / Codex / Gemini API)
     — explicitly call out which keys are needed
2. Clone the repo
3. Hub — local development
   - Build: `cd hub && go build -o ./hub-dev ./cmd/hub`
   - Initialize data root: bring up empty SQLite + bundled templates
   - Start: `./hub-dev serve --data-root /tmp/hub-dev`
   - Create a team token for mobile to authenticate
   - Verify: `curl -H "Authorization: Bearer <token>"
     http://localhost:NNNN/v1/_info`
4. Mobile — local development
   - `flutter pub get`
   - Configure dev hub URL in app (Settings → Hub config or via
     bootstrap screen)
   - `flutter run` (target: real device or emulator)
   - Verify: app connects, loads cached overview
5. Host-runner — local development
   - Install on dev machine: `cd host-runner && go build`
   - Configure: bind to local hub
   - Verify: hub sees host-runner registered
6. Mock-trainer harness (per memory: v1.0.169 + v1.0.170)
   - When to use (GPU-less testing of Experiment phase)
   - Bring-up steps
7. Engine credentials
   - Claude Code: anthropic API key
   - Codex CLI: openai API key
   - Gemini CLI: gcloud auth
   - Where to put them (per-team token store; not in source)
8. Making a change
   - Branch from main
   - Edit
   - Run tests (`docs/how-to/run-tests.md` — H2)
   - Verify locally on device
   - Submit PR
9. Common issues + fixes
   - Hub port conflict
   - Mobile cache stale (clear in Settings)
   - Engine auth failures
   - Host-runner reachability
10. Cleanup — how to wipe local state when done

**Acceptance.**
- [ ] A new contributor can follow the doc end-to-end on a fresh
      machine and reach "make a test change → see it in mobile" in
      ≤90 minutes (excluding SDK install time)
- [ ] Each step has a verification check
- [ ] Cross-references to existing how-tos
  (`install-hub-server.md`, `install-host-runner.md`,
  `run-the-demo.md`)
- [ ] Lint clean

**Effort.** 1–2 days.

**Dependencies.** None — pulls from existing how-tos. May surface
gaps in those; fix inline if found.

**Open prep items.** Flutter and Go SDK setup steps may benefit
from environment-tested verification (i.e., one of us actually
walks through them on a clean machine before merge).

### 5.6 H2 — `how-to/run-tests.md`

**Goal.** Test running guide. Covers all test surfaces and lint
scripts the contributor needs to satisfy CI.

**File added.** `docs/how-to/run-tests.md` (~150–200 lines)

**Content outline.**
1. Mobile tests
   - `flutter test` — unit + widget tests
   - `flutter analyze` — static analysis (must be clean)
   - `flutter test integration_test/` — if integration tests exist
   - Coverage target — current ~70% on changed files
2. Hub tests
   - `cd hub && go test ./...` (with PATH if needed)
   - `go vet ./...`
   - Specific test patterns (table-driven; integration tests via
     `_integration_test.go` files if present)
3. Host-runner tests
   - Same Go commands, scoped to host-runner module
4. Doc lint
   - `bash scripts/lint-docs.sh` — status blocks + broken links + cross-refs
   - `bash scripts/lint-glossary.sh` — glossary contract
5. CI parity
   - What runs on PR (link to `.github/workflows/ci.yml`)
   - Common CI vs local divergence (PATH, Flutter version, etc.)
6. Pre-PR checklist
   - All tests pass
   - Analyze clean
   - Lint scripts pass
   - PR template's doc-impact checklist completed

**Acceptance.**
- [ ] All 6 sections present
- [ ] Each test command has expected output snippet
- [ ] CI parity table maps each local command to its CI counterpart
- [ ] Cross-references from CONTRIBUTING.md §7

**Effort.** 1 day.

**Dependencies.** None.

---

## 6. Acceptance for the plan as a whole

When all 5 items ship:

- [ ] An external reviewer landing on the repo from a search engine
      can answer "is this project contributor-ready?" with
      yes from `LICENSE` + `README.md` + `CONTRIBUTING.md` +
      `SECURITY.md` in <5 minutes
- [ ] An AI agent inspecting the repo finds expected files at
      conventional locations on first walk
- [ ] A contributor following `local-dev-environment.md` reaches a
      working stack in ≤90 minutes (verified once during dress
      rehearsal)
- [ ] A contributor reading `CONTRIBUTING.md` knows: how to file an
      issue, how to set up dev, how to format their commit, how to
      verify their change, what to expect on review
- [ ] All cross-links resolve (lint-docs.sh clean; broken-link FAILs
      block CI per existing rule)

---

## 7. Test + verification

### 7.1 Lint

`bash scripts/lint-docs.sh` runs on the 2 docs/how-to/ docs (H1, H2).
Repo-root governance docs (G1, G3, G4) are not under `docs/`; the
lint doesn't apply. Ensure GitHub renders them correctly via preview.

### 7.2 Live walkthrough (H1)

H1 must be walked on a clean dev environment before merge. Otherwise
the steps drift from reality. One contributor (any) does this; logs
gaps; updates H1.

### 7.3 Issue template render check

After merge, file a test issue using each template; verify rendering
is correct on github.com. Delete test issues.

### 7.4 PR template adjacent check

PR template already covers doc-impact + glossary + memory. Ensure
G1 references it correctly without duplicating the same checklists.

---

## 8. Risks + mitigations

| # | Risk | Mitigation |
|---|---|---|
| 1 | H1 (local dev env) drifts from reality on every Flutter / Go SDK update | Mark with `Last verified vs code: vN.N.N`; re-verify quarterly |
| 2 | CONTRIBUTING.md and PR template duplicate checklists, creating drift | CONTRIBUTING references PR template by section; PR template is canonical |
| 3 | Vulnerability disclosure email goes unread (no maintainer SLA) | Use GitHub Security Advisory (built-in routing); email is fallback only; commit to weekly check |
| 4 | Engine credentials accidentally documented in H1 with real keys | Explicitly use placeholders; pre-merge review for secret leakage |
| 5 | Issue templates fall out of date as features land | Tied to PR template's discipline (doc-impact checklist forces template review when affected) |
| 6 | This plan competes with doc-uplift for the same calendar window | They're independent; can run parallel with two contributors. Solo: do this plan first (5–7 days) since it's smaller and unblocks reviewers earlier |

---

## 9. Open prep items

1. **Vulnerability disclosure email** — confirm with user before
   populating G3. If using GitHub Security Advisory only, no email
   needed.
2. **Project communication channel** (Slack / Discord) — confirm
   whether to surface in CONTRIBUTING.md §12. If none, skip the
   section.
3. **Engine API key acquisition path** — H1 should link to vendor
   docs; check current vendor onboarding flows for accuracy.
4. **Mock-trainer harness invocation** — exact commands per memory
   (v1.0.169 + v1.0.170); verify before writing into H1.
5. **CI workflow PATH fixes** — `PATH=/usr/local/go/bin:$PATH` per
   memory; verify whether H2's local commands need the same fix
   prefix.

---

## 10. Calendar implications for the lifecycle plan

This plan is **a sibling to `doc-uplift.md`**, not a successor. Both
gate the lifecycle plan:

```
        doc-uplift.md         contributor-readiness.md
              │                          │
              │ (P0+P1, ~14-21 days)     │ (5-7 days)
              ↓                          ↓
              └───────────┬──────────────┘
                          ↓
                  Lifecycle W1 starts
```

Two contributors: ship in ~3 calendar weeks before W1.
Solo: ship in ~4–5 calendar weeks before W1.

If pressure to start lifecycle: contributor-readiness can ship
**ahead of** doc-uplift (it's smaller and unblocks external reviewers
earlier). Doc-uplift continues in parallel with W1 only if its P0+P1
ship before W1 acceptance criteria need them — which they do
(W1's acceptance ties to demo beats; demo beats reference docs;
docs need uplift first).

Recommendation: **contributor-readiness in calendar week 1**, then
doc-uplift P0+P1 in calendar weeks 2–3, then lifecycle W1 starts in
calendar week 4.

---

## 11. Cross-references

- [`doc-uplift.md`](doc-uplift.md) — sibling plan; system-design
  doc axis
- [`project-lifecycle-mvp.md`](project-lifecycle-mvp.md) — gated on
  this plan + doc-uplift P0+P1
- [`doc-spec.md`](../doc-spec.md) — doc taxonomy + status block
  contract
- `.github/pull_request_template.md` — existing PR template that
  governance docs cross-reference
- [`how-to/install-hub-server.md`](../how-to/install-hub-server.md)
  — H1 references for hub bring-up
- [`how-to/install-host-runner.md`](../how-to/install-host-runner.md)
  — H1 references for host-runner bring-up
- [`how-to/run-the-demo.md`](../how-to/run-the-demo.md) — H1
  references for end-to-end smoke
- [`how-to/release-testing.md`](../how-to/release-testing.md) —
  adjacent to H2; release vs PR test scopes differ
- [`reference/coding-conventions.md`](../reference/coding-conventions.md)
  — G1 references for code style
- Contributor Covenant 2.1 —
  https://www.contributor-covenant.org/version/2/1/code_of_conduct/
- GitHub Security Advisory docs —
  https://docs.github.com/en/code-security/security-advisories
