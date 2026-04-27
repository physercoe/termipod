# Worker report — v1

Workers spawned via `agents.fanout` complete by calling
`reports.post`. The call shape:

```
reports.post({
  status: "success" | "partial" | "failed",
  summary_md: "...one-paragraph human-readable summary...",
  output_artifacts: ["trackio://run-id", "blob://sha256"],
  budget_used_usd: 0.42,
  next_steps: ["follow-up tasks the steward might want to spawn"]
})
```

Fields:

- **status** (required) — three values only.
  - `success` — task completed; results in summary + artifacts.
  - `partial` — got some results but didn't finish (budget, error,
    timeout, scope-creep).
  - `failed` — couldn't make progress; reason in summary_md.
  Any other string is rejected by the server.
- **summary_md** (required) — one paragraph, prose. The steward's
  synthesis pass reads this; keep it factual, not chatty.
- **output_artifacts** (optional) — URI list. Each URI must be a real
  pointer the steward can follow:
  - `trackio://<project>/<run>` for metric runs
  - `blob://<sha256>` for attached files
  - `doc://<id>` for hub-stored documents
  - `https://...` for external links
- **budget_used_usd** (optional) — what this worker burned. Helps the
  steward decide whether to fan out more.
- **next_steps** (optional) — one-liners. The steward may ignore them
  or use them to plan the next wave.

`reports.post` is the only correct way to signal "I'm done" — the
steward's `agents.gather` waits for either this event or terminal
agent status. Free-text "I finished" messages will not unblock the
gather.
