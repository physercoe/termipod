# Frame profiles — authoring guide

> **Type:** reference
> **Status:** Current (2026-08-05)
> **Audience:** contributors (humans + AI agent maintainers)
> **Last verified vs code:** 2026.730.1231-alpha — every rule in §3–§4
> re-derived from `eval.go` + `profile_translate.go` while porting them
> to TypeScript (§7); the ports are pinned to each other by fixture

**TL;DR.** A frame profile is a YAML block that says how to translate
one engine's stream-json output into the hub's typed agent_event
vocabulary. Each rule is a `(match → emit)` pair; an expression subset
(think JSONata-lite) extracts payload fields from the input frame.
Two runtimes interpret these rules — the host-runner in Go and the
desktop Companion in TypeScript — and §7 covers what that costs you as
an author. This page is the canonical authoring reference; read the
worked examples below before extending or adding a profile.

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
term     := path | string | pred
pred     := ( 'present' | 'absent' | 'nonempty' ) '(' expr ')'
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
| `present($.sig)` | `true` when `$.sig` carries something, else `false` |
| `absent($.sig)` | the negation of `present` |
| `nonempty($.a) \|\| "d"` | like `$.a \|\| "d"`, but an **empty** `$.a` also takes the default |

**Presence predicates.** Typed payloads often carry a boolean *derived*
from whether a source field was populated — `thought.signature_present`
is the canonical case. A hand-written translator writes `x != ""` in Go;
without these a rule could only ship the raw value, which is a different
field with a different meaning, and the two paths could never agree.

"Present" means **carrying something**: `nil`, `""`, `[]`, `{}` and
`false` are all absent. `0` is *present* — a token count of zero is a
real measurement, not a missing one.

`present`/`absent` always return a bool, so a coalesce never falls past
them (`present($.nope) || "d"` is `false`, not `"d"`). `nonempty` is the
one that *does* fall through: it returns its argument when present and
`nil` otherwise, which is how a rule says "treat empty as missing".

**Resolution rules:**

- Missing key at any depth → `nil`. No errors, no panics.
- `nil || x` falls through to `x`.
- Empty string `""` is **non-nil** and wins coalesce. Intentional —
  if an SDK emits `""` for a real field, you can model it. Wrap the term
  in `nonempty(...)` when you want the opposite.
- Out-of-bounds indices, type mismatches (indexing into a non-array),
  malformed paths all collapse to `nil`. A predicate missing its closing
  paren is just a malformed path — `nil`, not an error.

**Common pitfalls** (especially for AI agents who've seen JSONata):

- `$$.` means "outer scope" here, **not** "root context" as in JSONata.
  The outer scope is the parent frame during a `for_each` walk; outside
  a `for_each` it's `nil`.
- `||` only short-circuits on `nil`. Empty strings, `0`, and `false` are
  non-nil values and win. This differs from JavaScript-style `||` which
  treats falsy values as fall-through-able.
- There is no `.foo` (without `$.` prefix) syntax. Every path starts
  with `$.` or `$$.`.
- Beyond the three presence predicates there are no functions,
  comparison operators, arithmetic, or ternaries. If you need any of
  those, the right move is to extend the grammar minimally via a
  follow-up ADR — don't try to encode logic through coalesce hackery.

## 4. Rule shape

```yaml
- match: { ... }              # AND-ed field equality (required)
                              # keys default to top-level; dotted keys
                              # (params.item.type) walk nested objects.
  for_each: <expr>            # optional: array to iterate
  when_present: <expr>        # optional: gate emit on non-nil expression
  emit:
    kind: <string>            # the agent_event kind column
    producer: <string>        # default "agent"
    payload:                  # per-field expression map
      field_name: <expr>
      another:    <expr>
    payload_maps:             # optional: fields whose value is a map
      field_name:             # keyed by DATA, re-shaped per value
        source: <expr>        # must resolve to a map
        fields:               # evaluated per value ($. = that value)
          out_name: <expr>
    payload_lists:            # optional: fields whose value is a list
      field_name:             # re-shaped per element, order kept
        source: <expr>        # must resolve to an array
        fields:               # evaluated per element ($. = that element)
          out_name: <expr>
    # OR (mutually exclusive with payload / payload_maps / payload_lists):
    payload_expr: <expr>      # whole-payload passthrough; result must be a map
  sub_rules:                  # only meaningful with for_each
    - match: { ... }
      emit: { ... }
```

**`payload_maps`** is the map twin of `for_each` + `sub_rules`:
`for_each` walks an *array* and dispatches per element, `payload_maps`
walks a *map* and projects each value while keeping the source's keys.

Reach for it when an object is keyed by **data** rather than by field
names — claude's `modelUsage`, keyed by model id, is the motivating
case. Without it such an object can only pass through verbatim, which
means shipping the engine's own field names (`inputTokens`) where the
typed vocabulary promises ours (`input`). Nothing errors; the consumers
that treat the field as authoritative just read every number as zero.

```yaml
payload_maps:
  by_model:
    source: "$.modelUsage || $.model_usage"
    fields:
      input:  "$.inputTokens || $.input_tokens"
      output: "$.outputTokens || $.output_tokens"
```

Inside `fields`, `$.` is the value being projected and `$$.` is the
rule's own scope. An absent source **omits the field entirely** rather
than emitting `{}` — "no per-model data" and "data for zero models" are
different claims. Values that aren't objects are skipped, so one
malformed entry can't void the map.

**`payload_lists`** is the array twin, and it is **not** `for_each`:
`for_each` turns an array into N *events*, `payload_lists` turns an
array into one *field*. Reach for it when the elements are a collection
the renderer draws as a unit — a plan's steps, a todo list — so that
emitting a row per element would be a different transcript, not a
differently-shaped one.

```yaml
payload_lists:
  entries:
    source: "$.params.plan"
    fields:
      content: "$.step"
      status:  "$.status"
```

Same scope rules and the same absent-vs-empty line as `payload_maps`,
with one addition: an **empty** source array *is* projected, to an empty
list. "The agent published a plan with no steps" is a claim the engine
made; "the frame carried no plan" is not. Elements that aren't objects
are skipped, which shortens the list rather than holding a gap — a
projection declares the shape it produces, and there is no honest
placeholder for an element that can't take it.

Both projections rename **fields**. Neither renames **values** — the
grammar has no comparisons or ternaries (§3), so an engine whose enum
spelling differs from the vocabulary's (codex's plan status
`inProgress` vs `in_progress`) is normalized in its driver, not here.
If you find yourself wanting a value map in YAML, that is the follow-up
ADR §3 describes, not coalesce hackery.

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

### Example 4 — nested matcher (JSON-RPC envelope, codex)

JSON-RPC notifications wrap their payload under `params`, so the
discriminator the rule needs to match (the item's `type`) sits one
level deep. Match keys can be dotted paths to express that —
`params.item.type: agentMessage` walks into `params`, then `item`,
then compares `type`. Flat-key matchers (claude's `type:
assistant`) keep working unchanged; the dot is the toggle.

```yaml
- match:
    method: item/started
    params.item.type: commandExecution
  emit:
    kind: tool_call
    payload:
      id:    "$.params.item.id"
      name:  "\"commandExecution\""
      input: "$.params.item"

- match:
    method: item/started
    params.item.type: agentMessage
  emit:
    kind: system  # final text comes via item/completed; mark the start
    payload:
      kind:    "\"agent_message_started\""
      item_id: "$.params.item.id"
```

Most-specific-match-wins still applies — both rules have
match-keyset size 2, so only the rule whose `params.item.type`
literal matches fires. Unmatched item types fall through to the
implicit kind=raw fallback.

Use dotted matchers when a single method (or other top-level
discriminator) carries multiple shapes inside one nested field.
Don't use them as a substitute for `for_each` over arrays — the
dotted path walks one nested object, not a sequence.

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
6. **Regenerate the cross-language fixture.** A rule edit changes what
   *both* interpreters must produce (§7), and the checked-in fixture
   is what pins them together:

       go test ./internal/hostrunner/ -run Fixture -update-frame-fixture
       cd desktop/electron && npm test

   Skipping this fails `TestFrameProfile_ParityFixtureIsCurrent`, which
   is the intended behaviour — the fixture going stale is the same
   event as the two interpreters disagreeing.

If a rule misfires in production, the host-runner's diagnostics
emit a structured log line `frame_unmatched_total{family}` per
unmatched frame and a per-rule diff log when running in canary mode.
Use those to triage before editing.

## 7. Two interpreters

Profiles are interpreted **twice**, by design. The host-runner reads
them in Go, and the desktop Companion reads the same YAML in
TypeScript because it runs agents locally rather than through a hub
(vision-parity L3/L4). One rule language, two engines interpreting it.

That is the shape that drifts quietly: both implementations stay
green, both stay plausible, and the transcripts they produce stop
agreeing on some engine nobody happens to be watching. Nothing about a
rule edit makes the second interpreter fail.

So neither side is trusted to describe the other. A generated fixture
records what Go actually produces — the profiles verbatim, every
corpus frame's events, a set of synthetic rule shapes the shipped
profiles don't reach, and the expression corners — and the TypeScript
suite replays the same inputs and diffs. Go owns the answers; TS
matches them or fails.

| | |
|---|---|
| Generator + `-update-frame-fixture` | `hub/internal/hostrunner/profile_fixture_test.go` |
| Fixtures | `hub/internal/hostrunner/testdata/profiles/{<family>/parity,translate,grammar}.json` |
| TypeScript interpreter | `desktop/electron/src/frameprofile/` |
| TypeScript parity suite | `desktop/electron/src/frameprofile/parity.test.ts` |

Two consequences for authors:

- **Match values must be strings.** Go compares them as `any != any`,
  so a YAML integer never equals a JSON number decoded from a frame and
  the rule silently never fires; TypeScript has one number type and
  would match. `TestFrameProfile_MatchValuesAreStrings` fails on a
  non-string matcher at the moment it's authored, rather than as a
  transcript that differs on one client.
- **A value rename is not a profile edit.** The grammar has no
  comparisons (§3), so an engine whose enum spelling differs from the
  vocabulary's needs driver-side code — and that code now has to exist
  on *both* sides. Codex's plan status is the worked example:
  `canonicalPlanStatus` in `driver_appserver.go` and in
  `frameprofile/supplements.ts`.

## 8. References

- ADR: `../decisions/010-frame-profiles-as-data.md`
- Plan: `../plans/frame-profiles-migration.md`
- Loader: `hub/internal/agentfamilies/families.go`
- Evaluator: `hub/internal/hostrunner/profile_eval/eval.go`
- Translator: `hub/internal/hostrunner/profile_translate.go`
- TypeScript twin: `desktop/electron/src/frameprofile/{eval,translate}.ts`
- Canonical example profile: `hub/internal/agentfamilies/agent_families.yaml`
  (the `claude-code` entry's `frame_profile` block)
- Schema sidecar: `hub/internal/agentfamilies/agent_families.schema.json`
  (use with editor LSP for autocomplete + validation). Nothing at
  runtime reads it — the loader is `yaml.Unmarshal` — so it drifted
  silently twice before `schema_coverage_test.go` started asserting
  that every `yaml:` tag the loader accepts is a property the schema
  declares. Adding a field to `families.go` without adding it here
  makes the schema reject the *correct* YAML, which teaches authors to
  ignore it; that test is what stops it.
