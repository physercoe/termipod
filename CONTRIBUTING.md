# Contributing to TermiPod

Thanks for considering a contribution. TermiPod is a Flutter mobile
app + Go backend for directing CLI-agent work across remote hosts.
Mobile, hub, host-runner, docs, and tests are all in this repo, and
contributions to any of them are welcome.

This file is the PR contract — read it once before your first
contribution. The companion docs are
[`docs/how-to/local-dev-environment.md`](docs/how-to/local-dev-environment.md)
(setting up your environment) and
[`docs/how-to/run-tests.md`](docs/how-to/run-tests.md) (satisfying CI).

---

## 1. What we accept

- **Mobile** — Flutter / Dart code under `lib/`, tests under `test/`
- **Hub services** — Go code under `hub/` (`hub-server`, `host-runner`,
  `mock-trainer`, MCP bridge), tests in `*_test.go`
- **Documentation** — anything under `docs/`, plus repo-root
  governance files
- **Templates + examples** — YAML templates under `hub/templates/`
- **Bug reports + feature requests** — via
  [GitHub Issues](https://github.com/physercoe/termipod/issues)

For larger changes (new architecture, breaking API), please open a
discussion or issue first so we can align on direction before code
lands.

By contributing, you agree your work is licensed under the project's
[Apache License 2.0](LICENSE). No CLA is required.

---

## 2. Reporting issues

Use the issue tracker for bugs, feature requests, and questions:

- **Bug?** File via the
  [bug report template](.github/ISSUE_TEMPLATE/bug_report.md).
  Include reproduction, expected vs. actual, environment (mobile OS,
  hub version, network type), and any logs from the Activity tab.
- **Feature?** File via the
  [feature request template](.github/ISSUE_TEMPLATE/feature_request.md).
  Lead with the problem in user terms, not the proposed solution.
- **Security vulnerability?** Do **not** open a public issue.
  Follow [SECURITY.md](SECURITY.md) — preferred channel is GitHub's
  private security advisory.

If you're filing a bug from the app, the tester guide
[`docs/how-to/report-an-issue.md`](docs/how-to/report-an-issue.md)
walks the in-app vocabulary.

---

## 3. Setting up local development

The cold-start guide is
[`docs/how-to/local-dev-environment.md`](docs/how-to/local-dev-environment.md).
At a glance:

```bash
git clone https://github.com/physercoe/termipod.git
cd termipod
flutter pub get                  # mobile deps
cd hub && go build ./...         # hub binaries
```

You'll need Flutter 3.24+, Go 1.23+, `tmux` 3.2+, and an Android
emulator or device. Detailed prerequisites and the full bring-up
(including hub bootstrap and host-runner registration) are in the
how-to.

---

## 4. Branching + commit conventions

Branch from `main`; PR back into `main`. We don't use a
develop/release branching model.

Branch names: `<type>/<short-slug>`, e.g.

- `feat/lifecycle-phase-ribbon`
- `fix/sse-reconnect-backoff`
- `docs/contributor-readiness`

Commit messages follow Conventional Commits:

```
<type>(<scope>): <subject>

[optional body]

[optional footer]
```

- **Types** — `feat`, `fix`, `docs`, `refactor`, `test`, `chore`,
  `perf`, `ci`
- **Scope** — short, e.g. `lifecycle`, `ui`, `hub`, `release`,
  `mobile`, `docs`
- **Subject** — imperative, lowercase, no trailing period

Example: `feat(lifecycle): add phase ribbon to project detail`.

Squash-merge is the default; we keep `main` linear.

---

## 5. Code style

- **Dart / Flutter** — `flutter analyze` must be clean. The lint
  config is `analysis_options.yaml` (extends `flutter_lints`).
- **Go** — `go vet ./...` clean; format with `gofmt`. Idiomatic Go;
  table-driven tests preferred for unit tests.
- **Comments** — terse and load-bearing. Don't restate what the code
  does; explain non-obvious *why* (constraints, invariants,
  workarounds). See the project's existing code for tone.
- **Markdown / docs** — wrap prose at ~72 columns; one sentence per
  line is fine. Cross-link liberally.

For the longer style story, see
[`docs/reference/coding-conventions.md`](docs/reference/coding-conventions.md).

---

## 6. Testing

Tests are non-negotiable for code changes:

```bash
flutter analyze --no-fatal-infos         # mobile static check
flutter test --exclude-tags=screenshot   # mobile tests
cd hub && go test ./... && go vet ./...  # hub tests (if touched)
scripts/lint-docs.sh                     # docs (if touched)
scripts/lint-glossary.sh                 # docs (if touched)
```

Full surface (including coverage and CI parity) is in
[`docs/how-to/run-tests.md`](docs/how-to/run-tests.md).

CI (`.github/workflows/ci.yml`) runs lint, analyze, and tests on every
PR. Match it locally before pushing.

---

## 7. Documentation requirements

The repo treats documentation as part of the deliverable. The doc
contract is [`docs/doc-spec.md`](docs/doc-spec.md): every doc under
`docs/` carries a 5-line status block (Type / Status / Audience / Last
verified vs code / optional Supersedes), and project-specific terms
live in [`docs/reference/glossary.md`](docs/reference/glossary.md).

When your change touches:

- **CLI flags or REST endpoints** — update the affected doc in the
  same PR (per PR template §"Doc / spec updates")
- **A schema or contract** — update the relevant reference doc + bump
  its `Last verified vs code` line
- **An architectural decision** — add an ADR under
  `docs/decisions/NNN-*.md`
- **A user-visible behavior** — add a changelog entry in
  `docs/changelog.md`
- **A new project-specific term** — add to `docs/reference/glossary.md`
  in the same commit; first-use in prose links to the entry

The PR template's **Doc / spec updates** and **Term consistency**
checklists enforce this. Both lint scripts (`scripts/lint-docs.sh`,
`scripts/lint-glossary.sh`) run in CI and **must pass**.

---

## 8. Submitting a pull request

```bash
git checkout -b <type>/<slug>
# edit + test
git add -A
git commit -m "<type>(<scope>): <subject>"
git push -u origin <type>/<slug>
gh pr create
```

The PR template at
[`.github/pull_request_template.md`](.github/pull_request_template.md)
is the canonical PR checklist. Fill each section honestly; delete
sections that don't apply.

The PR template covers:

- **What changed** — concise bullets (the *what*)
- **Why** — link the issue / ADR / plan that motivated the change
- **Verification** — how you tested
- **Doc / spec updates** — see §7 above
- **Term consistency** — see §7 above
- **Memory / context** — durable lessons worth remembering across
  sessions

Review is best-effort; there is no formal SLA. Maintainers will leave
comments on the PR; respond inline or with new commits. We squash-merge
once approved.

---

## 9. After your PR merges

- Releases are tag-on-demand (no auto-tagging on doc-only commits per
  `feedback_no_auto_release` — see memory). Maintainers tag a release
  when binary-affecting changes are queued.
- The release workflow (`.github/workflows/release.yml`) builds Android
  APKs + iOS IPA on tag push and attaches them to the GitHub Release.
- See [`docs/how-to/release-testing.md`](docs/how-to/release-testing.md)
  for what release-time testing looks like (separate from per-PR
  testing).

---

## 10. Project structure (where to look)

```
termipod/
├── lib/                         # Flutter app
├── hub/                         # Go services
├── docs/
│   ├── README.md                # docs index — start here
│   ├── doc-spec.md              # doc contract
│   ├── spine/                   # architecture (blueprint, IA, …)
│   ├── reference/               # APIs, schemas, glossary
│   ├── how-to/                  # operator + contributor runbooks
│   ├── decisions/               # ADRs (architectural decisions)
│   ├── plans/                   # in-flight implementation plans
│   ├── discussions/             # design discussions (some Resolved)
│   └── tutorials/               # learning-oriented onboarding
├── scripts/                     # lint + helper scripts
├── test/                        # Flutter tests
├── pubspec.yaml                 # mobile version (kept in sync via `make bump`)
└── Makefile                     # build / bump / analyze / test
```

For architecture, read [`docs/spine/blueprint.md`](docs/spine/blueprint.md)
first. For the docs map, read
[`docs/README.md`](docs/README.md).

---

## 11. Communication

The issue tracker is the primary channel. There is no project Slack /
Discord at this time. For longer design conversations, use a
`docs/discussions/<topic>.md` file (PR'd in) rather than a
long issue thread — see existing examples under `docs/discussions/`.

---

## 12. Thanks

Thanks for taking the time. Small fixes are as welcome as feature
work. If anything in this doc — or in the local-dev-environment guide —
is wrong or unclear when you try to follow it, that's itself a bug;
file an issue or open a PR fixing the doc.
