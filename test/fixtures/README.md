# Test fixtures — captured hub JSON shapes

These files mirror the JSON the Go hub returns for high-churn endpoints, so
the mobile app's parse + normalization can be pinned against the real
contract (WS3 of `docs/plans/internal-techdebt-cleanup.md`). The app reads
hub entities as `Map<String, dynamic>` by design (no DTOs); these fixtures
are the substitute safety net — if the hub renames or drops a load-bearing
field, a fixture test fails in CI instead of a card rendering blank on a
device.

Keep the field names in lockstep with the hub structs:

- `sessions_list.json` → `sessionOut` (`hub/internal/server/handlers_sessions.go`)
- `agents_list.json` → `agentOut` (`hub/internal/server/handlers_agents.go`)
- `session_digest.json` → `digestJSON` session rollup (`hub/internal/server/handlers_agent_digest.go`)
- `agent_turns.json` → `turnJSON` listing (`hub/internal/server/handlers_agent_turns.go`)

When the hub contract changes intentionally, update both the struct and the
fixture in the same change.
