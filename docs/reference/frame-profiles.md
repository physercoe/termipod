# Frame profiles — authoring guide

> **Type:** reference
> **Status:** Current (2026-04-29)
> **Audience:** contributors (humans + AI agent maintainers)
> **Last verified vs code:** v1.0.329

**TL;DR.** A frame profile is a YAML block that tells the host-runner
how to translate one engine's stream-json output into the hub's typed
agent_event vocabulary. Each rule is a `(match → emit)` pair; an
expression subset (think JSONata-lite) extracts payload fields from
the input frame. This page is the canonical authoring reference —
read the worked examples below before extending or adding a profile.

---

## 1. When to read this

You're reading this if you need to:

- Add a new engine (codex, gemini-cli, …) to termipod.
- Update the claude-code profile because the SDK reshaped a frame.
- Fix an incorrect rule that's producing nil fields in a mobile UI tile.
- Author an overlay file at `<DataRoot>/agent_families/<family>.yaml`
  to override an embedded profile on your host.

Each of these is a YAML edit + agent restart, never a host-runner
rebuild (ADR-010, blueprint §5.3.2).

## 2. The 30-second mental model

A profile sits inside a `Family` entry in `agent_families.yaml`:

```yaml
families:
  - family: claude-code
    bin: claude
    version_flag: --version
    supports: [M1, M2, M4]
    frame_profile:
      description: |
        Most-specific match wins. for_each iterates arrays.
        $.foo accesses the inner scope; $$.foo the outer (parent
        frame during for_each). a || b returns the first non-nil.
      profile_version: 1
      rules:
        - match: { type: rate_limit_event }
          emit:
            kind: rate_limit
            payload:
              window: "$.rate_limit_info.rateLimitType || $.rateLimitType"
```

The host-runner reads each stream-json line into a Go map, then runs
`ApplyProfile(frame, profile)`. The function:

1. Finds every rule whose `match` predicate is satisfied.
2. Picks the rules tied for the *largest* match-keyset (most specific).
3. Fires those rules in declaration order.
4. If no rule matched, emits `kind=raw, payload=<frame verbatim>` so
   the transcript keeps the bytes for later profiling.

There is no global state, no chained transformations, no implicit
default. What's in the rule is what runs.

## 3. Expression grammar

```
expr     := term ( '||' term )*
term     := path | string
path     := '$.' segments              # inner scope (the for_each element)
          | '$$.' segments             # outer scope (parent frame)
          | '$.'                       # the inner scope itself
segments := segment ( '.' segment )*
segment  := identifier | identifier '[' digits ']'
string   := '"' …chars… '"'
```

That's the entire language. Concretely:

| Expression | Meaning |
|---|---|
| `$.foo` | `frame["foo"]`, or nil if absent |
| `$.foo.bar.baz` | nested map dig; nil at any missing depth |
| `$.tools[0]` | first element of `frame["tools"]`; nil if absent or out of bounds |
| `$.models[1].name` | indexed access then dotted dig |
| `$$.message.id` | inside a `for_each`, reach the parent frame |
| `"literal"` | a string constant |
| `$.a \|\| $.b` | first non-nil; missing → fall through |
| `$.a \|\| $.b \|\| "default"` | trailing literal acts as a default value |

**Resolution rules:**

- Missing key at any depth → `nil`. No errors, no panics.
- `nil || x` falls through to `x`.
- Empty string `""` is **non-nil** and wins coalesce. Intentional —
  if an SDK emits `""` for a real field, you can model it.
- Out-of-bounds indices, type mismatches (indexing into a non-array),
  malformed paths all collapse to `nil`.

**Common pitfalls** (especially for AI agents who've seen JSONata):

- `$$.` means "outer scope" here, **not** "root context" as in JSONata.
  The outer scope is the parent frame during a `for_each` walk; outside
  a `for_each` it's `nil`.
- `||` only short-circuits on `nil`. Empty strings, `0`, and `false` are
  non-nil values and win. This differs from JavaScript-style `||` which
  treats falsy values as fall-through-able.
- There is no `.foo` (without `$.` prefix) syntax. Every path starts
  with `$.` or `$$.`.
- There are no functions, comparison operators, arithmetic, or
  ternaries. If you need any of those, the right move is to extend the
  grammar minimally via a follow-up ADR — don't try to encode logic
  through coalesce hackery.

## 4. Rule shape

```yaml
- match: { ... }              # AND-ed top-level field equality (required)
  for_each: <expr>            # optional: array to iterate
  when_present: <expr>        # optional: gate emit on non-nil expression
  emit:
    kind: <string>            # the agent_event kind column
    producer: <string>        # default "agent"
    payload:                  # per-field expression map
      field_name: <expr>
      another:    <expr>
    # OR (mutually exclusive with payload):
    payload_expr: <expr>      # whole-payload passthrough; result must be a map
  sub_rules:                  # only meaningful with for_each
    - match: { ... }
      emit: { ... }
```

**Choosing `payload` vs `payload_expr`:**

- Use **`payload`** when you want to lift specific fields by name —
  the common case (text, tool_call, rate_limit, usage, session.init,
  turn.result). Each field gets its own expression.
- Use **`payload_expr: "$."`** when the legacy translator passes the
  *whole frame* as the payload — system fallback for unknown
  subtypes, error frames, and the deprecated completion alias. The
  expression must resolve to a map; non-map values yield `{}`
  defensively (and surface as a parity-test finding rather than a
  panic).

The two are mutually exclusive in a single emit. If both are set,
`payload_expr` wins.

**Match semantics.** `match` is the dispatch key. Every key in the
match map must literal-equal the corresponding top-level field of the
frame. An empty match (`{}`) matches any frame and is the lowest
possible specificity — useful for a profile-wide catch-all that
overrides the implicit `raw` fallback.

**Specificity = number of keys.** `{type: system, subtype: init}`
(2 keys) beats `{type: system}` (1 key) beats `{}` (0 keys). Among
ties, all rules fire in declaration order.

**`for_each` + `sub_rules`.** When the frame carries an array of
heterogeneous items (claude's `assistant.message.content[]` is the
canonical case), `for_each` resolves to that array and each `sub_rule`
runs against each element. Inside a sub_rule, `$.` refers to the
element and `$$.` to the parent frame.

**`when_present`.** When set, the rule's emit fires only if the
expression resolves to a non-nil value. Used so `usage` events don't
fire as all-nils when the SDK omits `message.usage`. A rule that
matches but is gated by `when_present` does NOT trigger the raw
fallback — the author chose to skip; respect it.

## 5. Worked examples

### Example 1 — three SDK shapes, one rule

claude-code reshaped `rate_limit_event` twice in 2026 (v1.0.326,
v1.0.328). The profile handles all three shapes via coalesce:

**Frames seen in the wild:**

```json
// Old SDKs — flat fields
{"type": "rate_limit_event", "rateLimitType": "5h", "status": "warn", "resetsAt": "2026-04-25T..."}

// Mid SDKs — under system envelope
{"type": "system", "subtype": "rate_limit_event", "rateLimitType": "5h", "status": "allowed"}

// Current SDKs — nested under rate_limit_info
{"type": "rate_limit_event", "rate_limit_info": {"rateLimitType": "five_hour", "status": "allowed", "resetsAt": 1777443000}}
```

**Two rules cover them all:**

```yaml
- match: { type: rate_limit_event }
  emit:
    kind: rate_limit
    payload:
      # Try nested first (current SDK), then flat (legacy + mid).
      window:    "$.rate_limit_info.rateLimitType || $.rateLimitType || $.rate_limit_type"
      status:    "$.rate_limit_info.status || $.status"
      resets_at: "$.rate_limit_info.resetsAt || $.resetsAt || $.resets_at"

- match: { type: system, subtype: rate_limit_event }
  emit:
    kind: rate_limit
    payload:
      window:    "$.rate_limit_info.rateLimitType || $.rateLimitType"
      status:    "$.rate_limit_info.status || $.status"
      resets_at: "$.rate_limit_info.resetsAt || $.resetsAt"
```

When the next SDK release renames `rateLimitType` to `windowType`?
Add `|| $.windowType` to the coalesce, save the overlay file, restart
the agent. Done.

### Example 2 — assistant frame with multi-emit

The assistant frame produces three kinds of agent_events at once:
text, tool_call, and usage. Two rules with the same match-set both
fire because they tie on specificity:

**Frame:**

```json
{
  "type": "assistant",
  "message": {
    "id": "msg_42",
    "model": "claude-opus-4-7",
    "content": [
      {"type": "text", "text": "Reading the file."},
      {"type": "tool_use", "id": "toolu_1", "name": "Read", "input": {"file_path": "/etc/hosts"}}
    ],
    "usage": {"input_tokens": 120, "output_tokens": 40, "cache_read_input_tokens": 9100}
  }
}
```

**Rules:**

```yaml
# Rule A — walk content blocks, dispatch on inner type.
- match: { type: assistant }
  for_each: $.message.content
  sub_rules:
    - match: { type: text }
      emit:
        kind: text
        payload:
          text:       "$.text"
          message_id: "$$.message.id"
    - match: { type: tool_use }
      emit:
        kind: tool_call
        payload:
          id:    "$.id"
          name:  "$.name"
          input: "$.input"

# Rule B — emit usage only when the SDK included it.
- match: { type: assistant }
  when_present: $.message.usage
  emit:
    kind: usage
    payload:
      input_tokens:  "$.message.usage.input_tokens"
      output_tokens: "$.message.usage.output_tokens"
      cache_read:    "$.message.usage.cache_read_input_tokens || $.message.usage.cache_read"
      message_id:    "$.message.id"
      model:         "$.message.model"
```

**Output:** three events — `text` (with `message_id` lifted from
outer scope via `$$`), `tool_call`, and `usage`. Both rules match on
`{type: assistant}` (size 1, tie); both fire in order.

If the SDK ever omits `message.usage`, Rule B's `when_present` gates
the emit. Rule A still fires. No raw fallback because Rule A matched.

### Example 3 — hierarchical dispatch on system.subtype

Three rules, all could match a `{type: system}` frame. Most-specific
wins, others sit dormant:

```yaml
- match: { type: system, subtype: init }
  emit:
    kind: session.init
    payload:
      session_id:      "$.session_id"
      model:           "$.model"
      permission_mode: "$.permissionMode || $.permission_mode"
      mcp_servers:     "$.mcp_servers || $.mcpServers"

- match: { type: system, subtype: rate_limit_event }
  emit:
    kind: rate_limit
    # …same shape as Example 1's rule…

- match: { type: system }
  emit:
    kind: system
    producer: agent
    payload:
      subtype: "$.subtype"
      task_id: "$.task_id"
```

For a `task_started` frame, only the third rule's match-set is
satisfied (`{type: system}`, size 1) → it fires alone. For an init
frame, both rule 1 (size 2) and rule 3 (size 1) match → only rule 1
fires (most specific wins).

## 6. Authoring workflow

The recommended loop for adding a rule (especially when the
maintainer is an AI agent):

1. **Capture the frame.** Get the raw stream-json line you want to
   handle. The SSE replay test corpus at
   `hub/internal/hostrunner/testdata/profiles/<family>/` is a good
   source.
2. **Decide the output kind.** Look at `docs/spine/blueprint.md` and
   `lib/widgets/agent_feed.dart` to see which event kinds the mobile
   UI knows how to render. Re-using an existing kind beats inventing
   a new one.
3. **Write the rule.** Match on the most-specific top-level fields
   that uniquely identify the frame; payload expressions extract
   what the kind's renderer expects.
4. **Validate.** `hub-server profile validate <yaml-path>` (when the
   subcommand lands; see plan Phase 1.6) catches grammar errors
   before runtime.
5. **Add a corpus row.** Append the frame + expected output to the
   parity test fixture. The diff test will then enforce that any
   future edit doesn't regress this case.

If a rule misfires in production, the host-runner's diagnostics
emit a structured log line `frame_unmatched_total{family}` per
unmatched frame and a per-rule diff log when running in canary mode.
Use those to triage before editing.

## 7. References

- ADR: `../decisions/010-frame-profiles-as-data.md`
- Plan: `../plans/frame-profiles-migration.md`
- Loader: `hub/internal/agentfamilies/families.go`
- Evaluator: `hub/internal/hostrunner/profile_eval/eval.go`
- Translator: `hub/internal/hostrunner/profile_translate.go`
- Canonical example profile: `hub/internal/agentfamilies/agent_families.yaml`
  (the `claude-code` entry's `frame_profile` block)
- Schema sidecar: `hub/internal/agentfamilies/agent_families.schema.json`
  (use with editor LSP for autocomplete + validation)
