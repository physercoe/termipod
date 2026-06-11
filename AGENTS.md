# AGENTS.md — for AI agents working this repo

You are a **builder** in a multi-agent workflow coordinated entirely through
GitHub. Read this file, then read
[`docs/how-to/agent-collaboration.md`](docs/how-to/agent-collaboration.md) for
the full protocol and [`CLAUDE.md`](CLAUDE.md) for repo conventions. The
protocol is vendor-agnostic: you are identified by your GitHub account, not by
which model or CLI you are.

## Your loop

1. **Find** an open issue labeled `ticket:ready` at a `tier:` you are cleared
   for. (Your operator told you which tiers at launch.)
2. **Claim** it:
   `gh issue edit <N> --add-assignee @me --add-label ticket:claimed --remove-label ticket:ready`,
   then comment with an ETA. (2-hour TTL; keep at most one open PR at a time.)
3. **Branch** off `main`: `agent/<your-handle>/<N>-<slug>`.
4. **Implement** exactly per the issue's spec — follow the reference PR it
   cites, file for file.
5. If the issue touches `lib/l10n/*.arb`, **acquire the `holds:arb` baton**
   first — only one ticket may hold it at a time.
6. **Self-verify**: run the gate the spec names, push, wait for CI, and
   confirm `gh pr checks <PR>` shows **every row `pass`** (do not trust the
   `--watch` exit code).
7. **Open a PR** (`Closes #<N>`), set `ticket:in-review`, request maintainer
   review. Address `ticket:changes` rounds on the same branch.

## Hard rules

- **Never merge.** Merging is the maintainer's sole action.
- **Never touch `lib/l10n/*.arb` without the `holds:arb` baton.**
- **Never guess on a judgment call.** Ambiguous vocabulary axis,
  ICU/placeholder trap, or spec-vs-code mismatch → set `ticket:blocked`,
  comment your specific question, and stop.
- Commit as your own configured GitHub identity; add a `Co-Authored-By`
  trailer for your handle.
- **English only** in code, comments, and docs.

Full protocol and rationale:
[`docs/how-to/agent-collaboration.md`](docs/how-to/agent-collaboration.md).
