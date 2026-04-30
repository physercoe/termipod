# Decisions (ADRs)

> **Type:** axiom
> **Status:** Current (2026-04-28)
> **Audience:** contributors
> **Last verified vs code:** v1.0.316

**TL;DR.** The decision log. Each numbered file records one
architectural choice, why we made it, and what followed. Append-only:
once Accepted, an ADR is immutable except for status changes. New
decisions supersede old ones via the `Supersedes` link.

Read `../doc-spec.md` §6 for the lifecycle rules. New ADRs use the
next sequential number — don't reserve, don't skip.

---

## Index

| # | Title | Status | Supersedes |
|---|---|---|---|
| [001](001-locked-candidate-a.md) | Locked Candidate-A as MVP demo | Accepted 2026-04-23 | — |
| [002](002-mcp-consolidation.md) | Consolidate to a single MCP service in spawn `.mcp.json` | Accepted 2026-04-27 | — |
| [003](003-a2a-relay-required.md) | A2A relay is required (GPU hosts are NAT'd) | Accepted 2026-04-23 | — |
| [004](004-single-steward-mvp.md) | One steward per team for MVP; per-member deferred | Accepted 2026-04-23 | — |
| [005](005-owner-authority-model.md) | User is owner/director; steward operates the system | Accepted 2026-04-23 | — |
| [006](006-cache-first-cold-start.md) | Mobile renders cached snapshots before network | Accepted 2026-04-27 | — |
| [007](007-mcp-vs-a2a-protocol-roles.md) | MCP for agent↔hub, A2A for agent↔agent | Accepted 2026-04-27 | — |
| [008](008-orchestrator-worker-slice.md) | Adopt the SOTA orchestrator-worker pattern (6-item slice) | Accepted 2026-04-27 | — |
| [009](009-agent-state-and-identity.md) | Agent identity and session lifecycle | Accepted 2026-04-28 | — |
| [010](010-frame-profiles-as-data.md) | Frame profiles as data — vendor schemas leave Go for YAML | Accepted 2026-04-29 | — |
| [011](011-turn-based-attention-delivery.md) | Turn-based delivery for async attention kinds | Accepted 2026-04-29 | — |
| [012](012-codex-app-server-integration.md) | Codex integration via `codex app-server` JSON-RPC, not `codex exec` | Accepted 2026-04-29 | — |
| [013](013-gemini-exec-per-turn.md) | Gemini integration is exec-per-turn-with-resume | Accepted 2026-04-29 | — |
| [014](014-claude-code-resume-cursor.md) | Claude-code resume threads `--resume <session_id>` | Accepted 2026-04-30 | — |

---

## How to add an ADR

1. Pick the next number (`ls decisions/0*-*.md | sort | tail -1`).
2. Filename: `NNN-short-name.md`, lowercase-hyphenated.
3. Use the template below. All five status-block lines required.
4. Index it in this README.
5. If the ADR supersedes another, set the supersedee's status to
   `Superseded` and link forward.

### Template

```markdown
# NNN. Short title

> **Type:** decision
> **Status:** Accepted (YYYY-MM-DD)
> **Audience:** contributors
> **Last verified vs code:** vX.Y.Z
> **Supersedes:** decisions/NNN-prior.md  (optional)

**TL;DR.** One or two sentences — the decision in plain language.

## Context

What forced the question. Why now. What was tried or considered.

## Decision

What we chose. Be precise — the ADR is read for the *what*.

## Consequences

What flows from this. Things that became easier; things that became
harder; things now forbidden by the choice.

## References

- Code: paths, commits
- Related ADRs
- Discussions that fed this decision
```
