# AGENTS.md — repository instructions for coding agents

This file is the bootstrap for any coding agent working in this repository.
Follow the user's or operator's explicit task first. A GitHub ticket is required
only when the task was assigned through the ticket workflow described below.

## Start here

1. Read [`CLAUDE.md`](CLAUDE.md) for the current architecture, repository map,
   commands, terminology, and coding conventions. Despite the filename, those
   instructions apply to every coding agent.
2. Inspect `git status`, the current branch, and recent history before editing.
   Preserve unrelated tracked changes and untracked files; they belong to the
   user unless the task says otherwise.
3. Read the implementation and its nearby tests before deciding what to change.
   Search before claiming a symbol, tool, test, or behavior exists or is absent.
4. For documentation work, also read [`docs/README.md`](docs/README.md) and
   [`docs/doc-spec.md`](docs/doc-spec.md). For a ticket-driven task, read
   [`docs/how-to/agent-collaboration.md`](docs/how-to/agent-collaboration.md).

## Choose the correct work mode

### Direct task mode (default)

An explicit user or operator request is sufficient authorization to work. Do
not search for, claim, relabel, or create a ticket unless asked.

- Start from current `main`, unless the task names an existing branch or is
  intentionally stacked on an unmerged dependency.
- Use the branch prefix required by the operator or runtime. Do not rename an
  existing task branch merely to match another convention.
- Implement the requested scope, verify it, commit, push, and open a normal PR.
  Do not add `Closes #...` or ticket labels when there is no ticket.

### Ticket builder mode (only when explicitly invoked)

Use the GitHub ticket state machine only when the operator tells you to take a
`ticket:ready` issue, the task names such an issue, or
[`scripts/agent-poller.sh`](scripts/agent-poller.sh) launched the work.

1. Claim the eligible issue by moving `ticket:ready` to `ticket:claimed` and
   comment `claiming as <handle>, ETA ~30m`.
2. Branch from `main` as `agent/<handle>/<N>-<slug>`. The claim comment and
   branch identify the builder; a shared GitHub account does not.
3. Follow the issue spec exactly. If the spec conflicts with the code or needs
   a judgment call, set `ticket:blocked`, comment the specific question, and
   stop rather than guessing.
4. Open the PR with `Closes #<N>`. Once every check is green, move the issue to
   `ticket:in-review` and request maintainer review.
5. Address `ticket:changes` on the same branch. The two-hour claim TTL,
   one-open-PR-per-handle rule, and full lifecycle remain defined in the
   collaboration how-to.

## Shared-file coordination

Before editing `lib/l10n/*.arb`, query open issues carrying `holds:arb`.

- In ticket builder mode, acquire `holds:arb` on your issue and re-check that
  you are the sole holder before editing. Release it whenever the ticket parks
  in `ticket:changes` or `ticket:blocked`; re-acquire it and rebase on `main`
  before resuming.
- In direct task mode, do not edit ARB files while another issue holds the
  baton. If ARB work has no ticket to carry the baton, ask the operator how to
  reserve the merge slot before changing those files.

The baton is a merge-slot mutex, not a permanent ownership claim.

## Work safely

- Make the smallest coherent change that fixes the underlying problem.
- Preserve public contracts unless the task explicitly changes them.
- Keep code, comments, commit messages, and documentation in English.
- Follow the glossary for collision-prone vocabulary. Do not invent a term to
  avoid resolving an ambiguity.
- ADRs are append-only. Do not rewrite an accepted decision's history.
- Do not bump versions, cut tags, publish releases, merge PRs, or modify remote
  state beyond the requested branch/PR workflow unless explicitly asked.
- Do not discard or overwrite user changes. Never include unrelated generated
  files, caches, build output, or untracked artifacts in a commit.

## Verify proportionally

Run the narrowest relevant checks first, then the repository gate appropriate
to the paths changed:

| Surface | Typical local gate |
|---|---|
| Hub / host-runner (`hub/**`) | `cd hub && go build ./... && go test ./... && go vet ./...` |
| Mobile (`lib/**`, `test/**`) | `flutter analyze --no-fatal-infos` and `flutter test --exclude-tags=screenshot` |
| Desktop frontend (`desktop/src/**`) | `cd desktop && npm test && npm run build` |
| Electron shell (`desktop/electron/**`) | `cd desktop/electron && npm run typecheck && npm run build && npm test` |
| Rust vault (`desktop/vault-*`) | `cargo test` in the affected crate; CI also proves the WASM build |
| Shared design tokens | `node design-tokens/build.mjs --check` and `scripts/lint-desktop-tokens.sh` |
| GitHub Actions | `scripts/lint-github-actions.sh` |
| Documentation | the relevant doc lints under `scripts/lint-*.sh` |

The path classifier in CI skips expensive Go, Flutter, desktop, and CodeQL
lanes when their surfaces are untouched. Repository-wide lints still run in
`Analyze & Test`, and workflow/classifier changes deliberately exercise every
lane. Path filtering is an optimization, not permission to skip relevant local
verification.

After pushing:

1. Wait for GitHub checks to settle.
2. Run `gh pr checks <PR>` again. Confirm `CI gate`, `CodeQL gate`, and every
   applicable job pass; path-filtered jobs may legitimately report `skipped`.
   Investigate every failure or cancellation, and do not trust only the
   `--watch` exit code.
3. Report any test that could not run locally and why.
4. Request maintainer review. Never merge the PR yourself.

## Commits and handoff

Commit with the configured `git config` identity and include a
`Co-Authored-By` trailer for that identity. Keep commits reviewable and avoid
mixing unrelated cleanup into the task. The final handoff should link the PR,
summarize behavior changes, list verification performed, and call out any
remaining risk or follow-up.
