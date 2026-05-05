# Run tests

> **Type:** how-to
> **Status:** Current (2026-05-05)
> **Audience:** contributors
> **Last verified vs code:** v1.0.351

**TL;DR.** Every test surface a contributor needs to satisfy CI before
opening a PR — Flutter (analyze + unit + widget), Go (hub +
host-runner + mock-trainer), and the two doc lint scripts. Mirrors
`.github/workflows/ci.yml`.

Sister docs: [`local-dev-environment.md`](local-dev-environment.md)
(SDK setup), [`../../CONTRIBUTING.md`](../../CONTRIBUTING.md) (PR
contract).

---

## 1. Mobile tests (Flutter / Dart)

From the repo root:

```bash
flutter pub get                        # dependencies (skip if already done)
flutter analyze --no-fatal-infos       # static analysis — must be clean
flutter test --exclude-tags=screenshot # unit + widget tests
```

`--exclude-tags=screenshot` skips golden-image tests that need
platform-specific fonts; CI runs the same exclusion. Run them locally
only when intentionally re-baselining goldens:

```bash
flutter test                           # includes screenshot tag
```

Layout under `test/`:

```
test/
├── helpers/        # shared test utilities
├── providers/      # Riverpod provider tests
├── screens/        # screen-level widget tests
├── screenshots/    # golden-image tests (tag: screenshot)
├── services/       # service-layer tests
├── widgets/        # individual widget tests
└── widget_test.dart
```

Add new tests next to the production code they cover. Mock external
boundaries (network, secure storage); don't mock the database (per
`feedback`, prefer integration tests with real sqflite where practical).

**Coverage** target on changed files is ~70%. Generate locally:

```bash
flutter test --coverage --exclude-tags=screenshot
# coverage/lcov.info
```

---

## 2. Hub tests (Go)

The Go services live under `hub/`. Build deps and run:

```bash
cd hub
go test ./...                          # all packages
go vet ./...                           # static analysis
```

If `go` isn't on your PATH (e.g. official-tarball install at
`/usr/local/go/bin`):

```bash
PATH=/usr/local/go/bin:$PATH go test ./...
```

Run a single package or test:

```bash
go test ./internal/server -run TestSpawnAgent -v
```

Integration tests live alongside unit tests in the same package
(`*_test.go`); the e2e acceptance harness is at
`hub/internal/server/e2e_acceptance_test.go`.

---

## 3. Host-runner / mock-trainer tests

Same Go test commands; the `host-runner`, `mock-trainer`,
`hub-mcp-server`, and `hub-mcp-bridge` packages are all under `hub/`
and covered by `go test ./...` from §2. To exercise a single binary's
package:

```bash
cd hub
go test ./cmd/host-runner -v
go test ./cmd/mock-trainer -v
```

End-to-end host-runner ↔ hub flow is in
`hub/internal/server/e2e_acceptance_test.go`.

---

## 4. Doc lint

Two bash scripts enforce `docs/doc-spec.md`:

```bash
scripts/lint-docs.sh        # status block + resolved-link + cross-refs
scripts/lint-glossary.sh    # canonical terms + confusion pairs
```

Run from the repo root. Both run on every PR via CI and **must pass**.

`lint-docs.sh` enforces:
- The 5-line status block (Type / Status / Audience / Last verified
  vs code, plus optional Supersedes) on every doc under `docs/`
  (except `docs/archive/`, `docs/screens/`, `docs/logo/`).
- Resolved discussions link forward to a `decisions/NNN-*.md` or
  `plans/*.md` in their first 30 lines.
- Every `[text](path)` cross-reference to a `.md` target resolves.
- Stale-doc warning (non-failing) when `Last verified vs code` lags
  the current `pubspec.yaml` version by > 5 minor versions.

`lint-glossary.sh` enforces `docs/reference/glossary.md` discipline —
every glossary heading has a body, the §12 confusion-pairs index
resolves, and known-bad spelling variants for canonical terms
(`host-runner`, `app-server`, etc.) don't drift back into prose. Code
contexts (backticks, fenced blocks, file paths) are excluded.

When you add a project-specific term, add it to
`docs/reference/glossary.md` in the same commit (per
`feedback_glossary_first` and PR template §"Term consistency").

---

## 5. CI parity

`.github/workflows/ci.yml` runs on every push to `main` and every PR.
Sequence:

| CI step | Local equivalent |
|---|---|
| Lint docs | `scripts/lint-docs.sh` |
| Lint glossary | `scripts/lint-glossary.sh` |
| Setup Flutter (stable) | `flutter --version` (≥ 3.24) |
| `flutter pub get` | `flutter pub get` |
| `flutter analyze --no-fatal-infos` | `flutter analyze --no-fatal-infos` |
| `flutter test --exclude-tags=screenshot` | `flutter test --exclude-tags=screenshot` |

Watch CI for your branch:

```bash
gh run list --branch <your-branch> --limit 5
gh run watch                          # tails the most recent run
gh run view <run-id> --log-failed     # logs of failing steps
```

Common CI vs local divergences:

- **Flutter version.** CI uses the `stable` channel via
  `subosito/flutter-action@v2`. If you're on `beta` or `master` and
  hit an analyzer-only failure, switch to stable locally.
- **PATH.** CI sets up Go and Flutter explicitly. Locally, ensure
  `/usr/local/go/bin` is on PATH if you used the official tarball.
- **Pub cache.** CI keys cache by `pubspec.yaml`. A local `pubspec.lock`
  drift won't bite CI; deleting `.dart_tool` and re-running `flutter
  pub get` resolves analyzer false-positives.
- **Tests excluded.** CI passes `--exclude-tags=screenshot`; reproduce
  locally to match.

---

## 6. Pre-PR checklist

Before pushing, confirm:

- [ ] `flutter analyze --no-fatal-infos` clean
- [ ] `flutter test --exclude-tags=screenshot` passes
- [ ] If you touched `hub/`: `go test ./...` and `go vet ./...` clean
- [ ] If you touched `docs/`: `scripts/lint-docs.sh` and
      `scripts/lint-glossary.sh` clean
- [ ] If you touched a doc, bumped its `Last verified vs code:`
- [ ] PR template's checklists filled (doc-impact, glossary, memory)

The PR template at `.github/pull_request_template.md` is canonical
for review expectations. CONTRIBUTING.md links it.

---

## 7. Cross-references

- [`../../CONTRIBUTING.md`](../../CONTRIBUTING.md) — PR contract
- [`local-dev-environment.md`](local-dev-environment.md) — SDK setup
- [`../doc-spec.md`](../doc-spec.md) — doc taxonomy
- [`../reference/glossary.md`](../reference/glossary.md) — canonical
  terms
- [`../reference/coding-conventions.md`](../reference/coding-conventions.md)
  — code style
- [`release-testing.md`](release-testing.md) — release-time test
  surface (separate from PR-time)
- `.github/workflows/ci.yml` — CI workflow (canonical)
