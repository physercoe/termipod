# Kimi Code (TypeScript) engine — implementation plan

> **Type:** plan
> **Status:** In review (single PR)
> **Audience:** contributors
> **Last verified vs code:** v1.0.821 / kimi-code 0.27.0 on macOS arm64, 2026-07-19

**TL;DR.** Add `kimi-code-ts` as a new engine family targeting the
**TypeScript rewrite** of Kimi Code CLI (`MoonshotAI/kimi-code`,
single compiled binary, npm/curl distribution) — distinct from the
existing `kimi-code` family, which targets the legacy **Python**
`kimi-cli` line (PyPI, floor 1.43.0). This lifts the HOLD declared in
[`kimi-code-engine.md` §9.1](kimi-code-engine.md): we ran the real TS
binary on a host and diffed its flag set, ACP surface, and MCP config
mechanism against what the legacy integration assumes. The TS build
still speaks ACP (`kimi acp`), so the engine rides the existing
M1/ACPDriver path with **M4 (tmux pane) as the sole fallback** — same
shape as the Python line, but with a different MCP injection
mechanism (project-level `.kimi-code/mcp.json`, auto-discovered — no
`--mcp-config-file` splice) and a trimmed flag set (`--yolo` only;
`--thinking` is gone).

**Verified on-host (kimi-code 0.27.0), replacing every §9.1 unknown:**

- `kimi acp` survives the rewrite. `initialize` returns
  `protocolVersion: 1`, `agentCapabilities.loadSession: true`,
  `promptCapabilities: {image: true, audio: false, embeddedContext:
  true}`, `mcpCapabilities: {http: true, sse: true}`,
  `sessionCapabilities: {list, resume}`. No `pdf` prompt capability
  is advertised (the Python line's `prompt_pdf: true` was assumed,
  never verified — we do not copy that assumption).
- ⚠️ `--mcp-config-file` is **gone** (confirmed: not in `--help`, not
  in the binary's flag table). Replacement: MCP config is
  auto-discovered from `mcp.json` at two levels — user
  (`$KIMI_CODE_HOME/mcp.json` or `~/.kimi-code/mcp.json`) and project
  (`<cwd>/.kimi-code/mcp.json`, which takes precedence). We write the
  **project-level** file in the per-spawn workdir; the operator's
  user-level file is never touched and still loads underneath.
- ⚠️ `--thinking` is **gone**. Thinking is now config-driven
  (`[thinking]` in `~/.kimi-code/config.toml`) and exposed per-session
  via ACP config options (see known gap below). Top-level flags that
  survive: `--yolo/-y`, `--auto`, `--plan`, `-m/--model`,
  `-p/--prompt`, `--output-format text|stream-json`, `-S/--session`,
  `-c/--continue`.
- Auth: `initialize` advertises one auth method `{id: "login", type:
  "terminal"}` (device-code flow; `kimi login` in a shell, or `kimi
  acp --login` as the ACP terminal-auth entry point). ACPDriver does
  not drive terminal-auth, so login stays out-of-band exactly like the
  Python line — `default_auth_method: ""` plus the AUTH_REQUIRED
  remediation path.
- ⚠️ `session/new` returns **`configOptions`** (selects for `model`,
  `thinking`, `mode` — e.g. `mode` options `default`/`plan`/`auto`/
  `yolo`) instead of the `modes`/`availableModels` blocks the Python
  line emits. `session/set_model` exists (returns `-32602` on a bad
  session id, not `-32601` method-not-found). See Known gaps.
- New: a headless one-shot mode exists — `kimi -p "<prompt>"
  --output-format stream-json` emits NDJSON (`{"role":"assistant",
  "content":…}` rows plus a `meta` resume hint). This makes an M2
  exec-per-turn mode possible in principle; it is **out of scope**
  here (needs its own frame profile).

## 1. Goal

After this plan:

- A `kimi-code-ts` steward template ships built-in and can be
  selected alongside claude / codex / gemini / kimi in the steward
  picker.
- `kimi --yolo acp` is wired through `launchM1` → `ACPDriver` with no
  Go diff to the driver core — only declarative additions plus the
  family-specific MCP writer and remediation string.
- MCP injection works with zero argv changes: the hub writes
  `<workdir>/.kimi-code/mcp.json` containing the `termipod` →
  `hub-mcp-bridge` entry (deep-merged with any existing project-level
  file, fail-loud on malformed JSON), and the TS build discovers it
  because the spawn cmd runs `cd <workdir>` first.
- Resume works: `loadSession: true` means paused kimi-code-ts agents
  resume via `session/load` through the existing ACP resume splice.

## 2. Non-goals

- **Renaming or changing the legacy `kimi-code` family.** The Python
  line is still patched upstream (1.47.0 at time of writing) and
  existing teams may rely on it. The two families coexist; operators
  pick per template. A future ADR may retire the Python family if
  upstream EOLs it.
- **M2 / exec-per-turn via `-p --output-format stream-json`.** The
  mode exists but its NDJSON schema (`role`/`content` rows, `meta`
  resume hints) matches neither claude's stream-json envelope nor
  gemini's; it needs a dedicated frame profile. Follow-up wedge.
- **ACP `configOptions` translation.** The TS build's session/new
  reply carries model/thinking/mode as `configOptions` selects.
  ACPDriver only parses the legacy `availableModes`/`availableModels`
  shape, so the mobile/desktop mode-model picker does not hydrate for
  this family. Follow-up wedge (see Known gaps).
- **ACP-level MCP passthrough.** The TS build advertises
  `mcpCapabilities: {http, sse}`, but ACPDriver hardcodes
  `mcpServers: []` in session/new (driver_acp.go) and hub-mcp-bridge
  is a stdio command. File-based project config is the supported
  path; protocol-level injection is a separate ADR if ever wanted.
- **Driving the terminal-type ACP auth method from the hub.**
  `kimi login` is interactive (device code in a browser); operators
  run it once per host, same as the Python line.
- **M4 LocalLogTail adapter.** kimi-code-ts M4 falls through to the
  generic PaneDriver, same as every non-claude/antigravity family.

## 3. Vocabulary

- **kimi-code-ts** — family name in `agent_families.yaml`. Binary
  `kimi`; product "Kimi Code CLI" (TypeScript rewrite,
  `MoonshotAI/kimi-code`); data root `~/.kimi-code` (overridable via
  `KIMI_CODE_HOME`). Chosen over `kimi-code-next` (ages poorly) and
  over re-pointing the existing `kimi-code` family (breaking change
  for teams on the Python build).
- **Python kimi-cli / legacy `kimi-code` family** — the existing
  integration (`MoonshotAI/kimi-cli`, PyPI, floor 1.43.0), untouched
  by this plan.
- **Project-level `mcp.json`** — `<workdir>/.kimi-code/mcp.json`,
  auto-discovered by the TS build; overrides same-named user-level
  entries. Our MCP injection point.
- **`configOptions`** — the TS build's ACP session-config surface:
  `session/new` returns typed select lists (`model`, `thinking`,
  `mode`) rather than `availableModes`/`availableModels`.

## 4. Surfaces affected

| Surface | Change |
|---|---|
| `hub/internal/agentfamilies/agent_families.yaml` | New `family: kimi-code-ts` row: `bin: kimi`, `supports: [M1, M4]`, `default_auth_method: ""`, `runtime_mode_switch: {M1: rpc}`, `prompt_image: {M1: true}`, `prompt_pdf: {M1: false}`; comments pin the verified flag set + the bin-name collision caveat |
| `hub/internal/hostrunner/launch_m2.go` | `writeMCPConfigForFamily` gains a `kimi-code-ts` case → new `writeKimiTSMCPConfig` (writes/merges `<workdir>/.kimi-code/mcp.json`, 0o700/0o600, fail-loud on malformed existing file, replace-not-skip on the `termipod` entry) |
| `hub/internal/hostrunner/launch_m1.go` | **No change** — no argv splice needed (auto-discovery replaces `--mcp-config-file`) |
| `hub/internal/hostrunner/driver_acp.go` | `authRequiredRemediation` gains a `kimi-code-ts` case → `kimi login` remediation |
| `hub/internal/server/handlers_sessions.go` | Add `kimi-code-ts` to the `spliceACPResume` arm of `handleResumeSession` |
| `hub/internal/server/respawn_with_spec_mutation.go` | Same addition (defensive parity) |
| `hub/internal/server/template.go` | `contextFileNameForKind`: `kimi-code-ts` joins the `AGENTS.md` arm |
| `hub/internal/hubmcpserver/scaffolds_templates.go` | Engine enum + `engineCmd` case returning `kimi --yolo acp` |
| `hub/templates/agents/steward.kimi-ts.v1.yaml` (new) | M1-only steward template; `cmd: "kimi --yolo acp"` |
| `hub/templates/prompts/steward.kimi-ts.v1.md` (new) | Steward prompt mirroring `steward.kimi.v1.md`, adjusted for the new MCP config path and flag set |
| `desktop/src/surfaces/AgentSpawn.tsx` | Add `kimi-code-ts` to the `ENGINES` picker array |
| `test/widgets/agent_feed_kind_classification_test.dart` | Add `kimi-code-ts` to both `knownNonKinds` skip-lists |
| `docs/decisions/054-kimi-code-ts-engine.md` (new) | ADR pinning the coexistence decision, the MCP-injection mechanism swap, and the deferred configOptions wedge |
| `docs/plans/kimi-code-engine.md` §9.1 | One-line pointer: HOLD lifted, superseded by this plan |

## 5. Implementation notes

### 5.1. Family row (agent_families.yaml)

```yaml
  - family: kimi-code-ts
    bin: kimi
    version_flag: --version
    supports: [M1, M4]
    default_auth_method: ""
    runtime_mode_switch:
      M1: rpc
    prompt_image:
      M1: true
      M4: false
    prompt_pdf:
      M1: false
      M4: false
```

`runtime_mode_switch.M1: rpc` is declared because `session/set_model`
exists on the wire — but see Known gaps: without `configOptions`
translation the picker never hydrates, so this is inert today and
forward-correct once the translation wedge lands.

**Bin-name collision caveat (documented in the row comments):** both
the Python and TS builds install as `kimi`. The capability probe is
bin-existence-based, so on a host with either build installed, BOTH
families report available. Disambiguation signals if ever needed:
Python prints `kimi, version 1.x.y`; the TS build prints a bare
`0.x.y`. Templates should be chosen deliberately; the family row
comments tell operators how to check (`kimi --version`).

### 5.2. MCP writer (launch_m2.go)

`writeKimiTSMCPConfig(workdir, hubURL, token)`:

1. `mkdir -p <workdir>/.kimi-code` (0o700).
2. If `<workdir>/.kimi-code/mcp.json` already exists (e.g. the
   workdir is a real repo the operator pre-configured), read it;
   malformed JSON fails the spawn loud. Missing file → empty
   `{ "mcpServers": {} }` seed.
3. Splice/replace `mcpServers.termipod` → `{command:
   "hub-mcp-bridge", env: {HUB_URL, HUB_TOKEN}}`; existing entries
   pass through.
4. Write back 0o600.

Unlike the Python-line writer we do **not** merge the operator's
user-level file: the TS build loads user-level
`~/.kimi-code/mcp.json` itself and applies project-over-user
precedence, so operator servers keep working with zero copying and
zero secret duplication into workdirs.

### 5.3. Steward template

`steward.kimi-ts.v1.yaml` mirrors the kimi steward with:

- `template: agents.steward.kimi-ts`, `backend.kind: kimi-code-ts`
- `driving_mode: M1`, `fallback_modes: [M4]`
- `cmd: "kimi --yolo acp"` — `--yolo` keeps the same consent posture
  as the kimi steward (engine-layer auto-approve, self-gating via
  `request_approval`); `--thinking` is dropped (flag removed
  upstream; thinking is on by default in the TS build's config).
- `display_label: "Steward (kimi-ts)"`

The prompt file is `steward.kimi.v1.md` with two surgical edits:
"configured in your per-spawn `.kimi/mcp.json`" → "`.kimi-code/
mcp.json`", and the intro paragraph names the TS build. Everything
else — `--yolo` rationale, MCP catalog, orchestrator-worker guidance
— carries over verbatim (the TS build also ships the built-in `Agent`
tool the prompt references).

### 5.4. Resume

`loadSession: true` (verified) → `kimi-code-ts` joins the
`spliceACPResume` arm in both server-side switches. No driver change:
the ACP resume path is engine-neutral.

## 6. Known gaps (documented, not blocking)

1. **Mode/model picker does not hydrate.** `session/new` returns
   `configOptions`; ACPDriver parses only `availableModes`/
   `availableModels`, so no mode/model state event is synthesized and
   mobile/desktop hide the picker. Switching still works on the wire
   (`session/set_model` exists) but there is no UI for it. Follow-up:
   translate `configOptions` → the legacy state-event shape in
   ACPDriver (engine-neutral, would also future-proof gemini if it
   adopts the same ACP revision). To be tracked as a separate issue.
2. **Thinking effort switching** is exposed as a `configOptions`
   select (`thought_level` category) — same gap, same follow-up.
3. **Probe ambiguity** with the Python build (§5.1) — cosmetic,
   documented.
4. **`session/set_mode` semantics**: the TS `mode` select includes
   `plan`/`auto`/`yolo`, which overlaps with termipod's `--yolo`
   cmd-line posture. We leave mode untouched (default) and keep
   `--yolo` in the cmd; operators who want the ACP gate omit `--yolo`
   in a team-local template overlay, same as the kimi steward today.

## 7. Verification

- **Unit tests (this PR)**:
  - `families_test.go`: `kimi-code-ts` joins the presence list; new
    `TestKimiCodeTS_FamilyShape` pins bin/supports/mode-switch/
    prompt-image/pdf.
  - New `launch_m1_kimi_ts_mcp_test.go`: writer fresh-write,
    preserve-existing, malformed-fails-loud, stale-`termipod`-replaced,
    perms; dispatcher routes `kimi-code-ts` to `.kimi-code/mcp.json`
    and does not leak `.kimi/`, `.gemini/`, `.codex/`, or `.mcp.json`
    files.
  - `handlers_templates_kimi_ts_test.go`: embedded template shape,
    exact cmd `kimi --yolo acp`, negative checks (`--thinking`,
    `--mcp-config-file`, `-p `, `--output-format` absent).
  - `handlers_resume_engine_session_test.go`:
    `TestSessions_ResumeThreadsACPCursor_KimiCodeTS` mirrors the
    kimi-code case.
  - `template_test.go`: `kimi-code-ts → AGENTS.md` rows.
  - `scaffolds_templates_test.go`: engine/cmd row.
  - `idle_test.go`: family enumeration.
  - `driver_acp_auth_required_test.go`: `kimi-code-ts` remediation
    case.
- **On-host smoke (already done during investigation, kimi-code
  0.27.0)**: `initialize` + `session/new` handshake, flag table, MCP
  discovery mechanism, stream-json probe. Results embedded in the
  TL;DR above.
- **Post-merge on-host smoke (reviewer)**: spawn a kimi-code-ts
  steward on a host with `kimi login` completed; verify the
  `termipod` MCP server appears in `/mcp`, one turn round-trips, and
  AUTH_REQUIRED surfaces the `kimi login` remediation on a logged-out
  host.

## 8. Rollout

Single PR. Declaratively verifiable on CI (YAML loads, template audit,
unit tests). The family only becomes selectable where pickers
enumerate it (desktop picker updated here; mobile renders unknown
kinds generically until its own label case lands — cosmetic).

## 9. Risks

- **Upstream churn.** The TS line is pre-1.0 (0.27.0); flags or the
  `configOptions` shape may shift. Mitigation: the integration
  surface is deliberately minimal (`kimi acp` + project mcp.json);
  the family row comments record the verified version.
- **Bin collision** confuses operators running both builds (§5.1).
  Documented in row comments and ADR.
- **`session/load` reply shape** may be as minimal as the Python
  line's `{}` — the W7/W7c mode/model carryover is field-shape-driven
  and engine-neutral, so resume degrades the same way gemini/kimi
  already handle (no picker state until the configOptions wedge).

## 10. References

- [ADR-026](../decisions/026-kimi-code-engine.md) + its
  [plan §9.1](kimi-code-engine.md) — the Python-line integration and
  the HOLD this plan lifts.
- [ADR-013](../decisions/013-gemini-exec-per-turn.md) — the M1/ACP
  precedent this engine rides.
- [ADR-021](../decisions/021-acp-capability-surface.md) — ACP
  capability grammar used by the family row.
- Upstream: [`MoonshotAI/kimi-code`](https://github.com/MoonshotAI/kimi-code);
  [Kimi Code MCP docs](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/mcp.html)
  (two-level `mcp.json`, project-over-user precedence, field schema).
