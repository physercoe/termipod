# Multi-engine frame parsing — vendor schemas as data, not code

> **Type:** discussion
> **Status:** Resolved (2026-04-29) → `../decisions/010-frame-profiles-as-data.md`
> **Audience:** contributors
> **Last verified vs code:** v1.0.328

**TL;DR.** Every claude-code SDK release reshapes its stream-json
frames; we currently absorb the churn in Go (host-runner) and Dart
(mobile renderer). Adding codex, gemini-cli, and friends multiplies
the surface, because each speaks a different dialect of "stdout JSON
with a model on the other end." This doc audits the coupling, lays
out four design options, and recommends extending the existing
`agent_families.yaml` overlay system with declarative *frame
profiles* so a new SDK release is a YAML edit, not a hub rebuild.

**Resolved:** Option A accepted. See ADR-010
(`../decisions/010-frame-profiles-as-data.md`) for the chosen
design and `../plans/frame-profiles-migration.md` for the migration
phases.

---

## 1. Why this is becoming a rabbit hole

Three forces collide:

1. **SDK churn.** claude-code reshaped `rate_limit_event` twice in
   the v1.0.x window — once moving the signal under
   `system.subtype=rate_limit_event`, once nesting the fields under
   `rate_limit_info` (v1.0.326 and v1.0.328 both shipped fixes for
   this). Field naming flips between camelCase and snake_case across
   releases.
2. **Engine plurality.** Today only claude-code is a first-class
   citizen — `driver_stdio.go::translate()` reads claude's frames by
   shape. `agent_families.yaml` lists four engines we want to support
   (claude-code, codex, gemini-cli), and the roadmap adds
   more. Each speaks a different stream-json dialect with the same
   underlying *concepts* (assistant text, tool calls, usage, rate
   limits).
3. **Re-deploy friction.** The host-runner is a Go binary built and
   tagged via CI; the user installs it on each host. Every SDK shape
   change forces a new tag and a re-deploy. The pace of upstream
   breakage already outruns our release cadence.

The blueprint (§5.3.2) explicitly wants "new agent families to land
as drop-in declarations." Today only the *launch* contract honors
that (binary name, mode support, billing rules). The *parse* contract
silently violates it — every new field requires Go edits.

---

## 2. What is hardcoded today

### Host-runner (Go, requires recompile)

`hub/internal/hostrunner/driver_stdio.go::translate()` and helpers:

| Frame | Coupling |
|---|---|
| `system.subtype=init` | Hardcoded field lift: `model`, `cwd`, `permissionMode`/`permission_mode`, `tools`, `mcp_servers`/`mcpServers`, `slash_commands`/`slashCommands`, `agents`, `skills`, `plugins`, `version`/`claude_code_version`, `output_style`, `fast_mode_state` |
| `system.subtype=rate_limit_event` | Branch added Apr 2026; falls through to `translateRateLimit` |
| `system.subtype=task_started/_updated/_notification` | Pass-through; mobile decides what to lift (see below) |
| `assistant.message.content[]` | Hardcoded block-type switch: `text`, `tool_use`, fall-through to `raw`. Picks `id`, `name`, `input` for tool_use |
| `assistant.message.usage` | Field lift: `input_tokens`, `output_tokens`, `cache_read_input_tokens`/`cache_read`, `cache_creation_input_tokens`/`cache_create`, `service_tier` |
| `user.message.content[]` | Filters `tool_result` blocks; lifts `tool_use_id`, `content`, `is_error` |
| `rate_limit_event` (top-level) | `translateRateLimit` digs into `rate_limit_info`/`rateLimitInfo`/flat — three shapes |
| `result` | `normalizeTurnResult`: lifts `total_cost_usd`/`cost_usd`, `duration_ms`, `num_turns`, `terminal_reason`/`subtype`, `permission_denials`, `fast_mode_state`/`fastModeState`, `modelUsage`/`model_usage`. Walks `modelUsage` and renames inner camelCase keys |
| `error`, `raw` | Pass-through |

Every casing variant (`rateLimitType` vs `rate_limit_type`, etc.) is
a hand-written `firstNonNil` call.

### Mobile (Dart, requires app rebuild)

`lib/widgets/agent_feed.dart`:

- `_humanWindow` — string table mapping `5h` / `5_hour` / `five_hour`
  / `weekly_opus` / etc. to display labels.
- `_systemBody` — task-subtype dispatch (`task_started` /
  `task_updated` / `task_notification`).
- `AskUserQuestion` tool-name detection (added v1.0.328).
- `_shortModelName` — claude-specific model-name shortener
  (`claude-opus-4-7-…` → `opus 4.7`).
- Per-engine icon/color in `AgentEventCard.toolIconFor` for known
  tool names.

### Already config (good)

- `agent_families.yaml` — engine launch contract (`bin`,
  `version_flag`, `supports`, `incompatibilities`). Has overlay
  loading at `<DataRoot>/agent_families/<family>.yaml` and an
  `Invalidate()` call so edits land without a restart. **The
  parsing-rules extension would slot directly into this.**

---

## 3. Design options

### Option A — Declarative frame profile in `agent_families.yaml`

Extend `Family` with a `frame_profile` block: a pure-data structure
that describes "for this `type`, take these fields, emit this typed
event kind."

Sketch (illustrative, not final):

```yaml
- family: claude-code
  bin: claude
  ...
  frame_profile:
    # Each entry: when (matcher) → emit (event-shape map)
    rules:
      - match: { type: system, subtype: init }
        emit:
          kind: session.init
          producer: agent
          payload:
            session_id:    "$.session_id"
            model:         "$.model"
            permission_mode: "$.permissionMode || $.permission_mode"
            mcp_servers:   "$.mcp_servers || $.mcpServers"
            # …
      - match: { type: rate_limit_event }
        emit:
          kind: rate_limit
          payload:
            window:    "$.rate_limit_info.rateLimitType || $.rateLimitType"
            status:    "$.rate_limit_info.status || $.status"
            resets_at: "$.rate_limit_info.resetsAt || $.resetsAt"
            # …
      - match: { type: assistant }
        for_each: "$.message.content[]"
        emit:
          when:
            - type: text   → kind: text       payload: { text: "$.text", message_id: "$$.message.id" }
            - type: tool_use → kind: tool_call payload: { id: "$.id", name: "$.name", input: "$.input" }
        also:
          when_present: "$.message.usage"
          kind: usage
          payload: { input_tokens: "$.input_tokens", … }
```

The expression language can be JSONata, JMESPath, or a tiny
hand-rolled subset (`"$.a.b || $.c"`). Pure data; no eval; user
overlays drop in via the existing `<DataRoot>/agent_families/<name>.yaml`
hot-reload path.

**Pros**
- Zero hub rebuild for a field rename or new fallback path.
- Each engine ships its own profile; codex / gemini-cli are net-add
  YAML files, not Go diffs.
- The overlay path already exists — operators can fix a broken
  upstream shape on their own host without waiting for a release.
- Pure data; no security boundary issues (nothing executes).

**Cons**
- Yet-another-DSL to learn and debug. A typo in a field path is a
  silent miss until someone notices the rate-limit pill stopped
  lighting up.
- Conditional dispatch (`if subtype=rate_limit_event then run X`) is
  awkward in pure-data form. Solved by a `when` predicate but adds
  syntax.
- Doesn't solve the *mobile* coupling — Dart still needs vendor
  knowledge for icons, model-name shorteners, `AskUserQuestion`
  detection. (See §5.)

### Option B — Translator subprocess per engine

Each engine ships a separate binary (Go, Python, anything) speaking
JSON-RPC over stdio. Host-runner pipes raw frames in, gets normalized
events out. Like `git-credential-*` helpers.

**Pros**
- Maximum flexibility — translators can do anything (state machines,
  external lookups, etc.).
- Can be written in any language; vendor maintainers could publish
  their own.

**Cons**
- One more process per agent, one more crash boundary.
- Packaging story (where does the user get the translator binary?
  who signs it?).
- Wire protocol drift between host-runner and translator — yet
  another contract.
- Overkill for what's mostly field-renames-and-casing.

### Option C — Embedded scripting (Lua / Starlark)

Each engine has a script file. Embedded defaults; user overlays.

**Pros**
- More expressive than Option A — full conditional logic, pattern
  match, etc.
- Sandboxable.

**Cons**
- Pulls a language runtime into the hub binary (~MB of cgo or pure-Go
  interpreter).
- Operators have to learn another language to overlay; YAML wins on
  the "drop a file and fix it now" axis.
- Overkill for the actual problem shape.

### Option D — Keep hardcoded, factor cleanly

Move all the field paths into per-engine constant tables in Go. The
"new field" experience improves (one diff, one file) but a host-runner
rebuild is still required.

**Pros**
- Nothing new to learn. Fastest to ship.

**Cons**
- Doesn't solve the user's stated pain ("hot reload, don't recompile").
- The per-engine plurality still bloats the binary as we add codex,
  gemini-cli, etc. — each one's quirks live in Go forever.
- Operators can't fix upstream breakage themselves.

---

## 4. Recommendation

**Adopt Option A** (declarative frame profile in
`agent_families.yaml`). Reasoning:

- The overlay infrastructure already exists and is hot-reloaded.
- Pure-data is cheap to read, cheap to debug, cheap to security-audit
  — no eval, no sandbox.
- ~80% of the SDK churn we've already absorbed is field renames and
  casing fallbacks; pure-path expressions cover that exactly.
- The remaining ~20% (conditional dispatch, per-block-type switches
  inside `assistant.message.content`) wants a small `when` predicate
  + a `for_each` walker, both expressible in JSONata syntax.

**Pick JSONata over JMESPath** if we go this way. JSONata is the
accidental standard for this kind of mapping (used by Camel,
Microsoft Power Automate, n8n); it has a Go implementation
(`github.com/blues/jsonata-go`); and it handles fallback / coalescing
cleanly (`a.b ? a.b : c.d`). JMESPath is simpler but doesn't have
fallback as a first-class construct, which is exactly what 90% of
our rules need.

**Migration path:**

1. Land the profile schema + loader + interpreter behind a feature
   flag. Existing `translate()` keeps running.
2. Author the claude-code profile that reproduces today's behavior
   exactly. Add a parity test that runs both translators on a corpus
   of recorded frames and asserts identical `agent_events` rows.
3. Flip the default to profile-driven. Keep the Go translator as a
   fallback for one release in case the profile is buggy.
4. Remove the Go translator; ship codex / gemini-cli profiles
   as the proof of multi-engine.
5. Document the profile schema in `reference/frame-profiles.md` so
   operators have something to point at when overlay-editing.

Total estimated work: 2–3 wedges over ~2 weeks at the current pace.
Most of the investment is in the parity test corpus — once that
exists, profile authoring is fast and the migration carries low risk.

---

## 5. The mobile half — same problem, different shape

The host-runner is half the story. The mobile renderer also embeds
vendor knowledge:

- `_humanWindow` mapping (could be in profile: `display_labels`).
- Model-name shorteners (could be in profile: `model_short_pattern`).
- Tool icons (could be in profile: `tool_icons.<name>`).
- `AskUserQuestion` detection (this is *renderer* coupling, not
  parsing — and it's intrinsic; the inline-question card needs to know
  the tool by name to render its custom UI).

The rendering coupling is genuinely harder to data-drive because
mobile widgets are *visual*, not just textual transforms. A
reasonable split:

- **Decorative knowledge** (window labels, icons, name shorteners)
  ships as part of the frame profile, served to mobile via the
  existing `agent_families` API. Mobile renders generically.
- **Interactive widgets** (AskUserQuestion buttons, approval cards,
  etc.) stay coded in Dart, but keyed off a `tool_widgets:` registry
  in the profile so adding a new tool widget is "Dart code + one YAML
  line" rather than "Dart code + grep-and-edit".

This second half can ship after the host-runner half; they're
independent.

---

## 6. Open questions

- **Profile language choice.** JSONata, JMESPath, or a hand-rolled
  subset? JSONata is the recommendation above; alternatives have
  different size/expressiveness/familiarity tradeoffs.
- **Versioning the profile schema.** Profiles will themselves
  evolve. Should we stamp `profile_version: 1` in the YAML and
  reject unknown versions?
- **Authorship.** Do we expect vendor maintainers to publish profiles
  upstream (claude-code repo ships a `termipod-profile.yaml`), or do
  we always own the profile? Today's coupling implicitly says we
  always own it; that should probably stay until vendors are
  motivated.
- **Diagnostics.** When a profile rule misfires (silent nil), how
  does the operator find out? Probably a debug overlay on the mobile
  side that highlights "this card's payload was empty — profile
  mismatch?" with a link to the rule.
- **Streaming partial frames.** Today's translator is one-frame-in,
  zero-or-more-events-out. Does any engine emit frames that need to
  be merged across reads (e.g. partial JSON)? claude-code doesn't;
  worth checking codex and gemini-cli before committing.

---

## 7. References

- Current implementation: `hub/internal/hostrunner/driver_stdio.go::translate()`
  and `translateRateLimit` / `normalizeTurnResult` helpers
- Existing overlay infrastructure: `hub/internal/agentfamilies/families.go`
  — read this before sketching the loader.
- Recent SDK-shape fixes that motivated this doc:
  - v1.0.326 — added `system.subtype=rate_limit_event` branch
  - v1.0.328 — added nested `rate_limit_info` peek
- Blueprint reference: `../spine/blueprint.md` §5.3.2 (engine
  pluggability axiom — "new agent families land as drop-in
  declarations")
- Doc spec: `../doc-spec.md` — this is a DISCUSSION (open question);
  it resolves into an ADR + plan when the team picks Option A.
