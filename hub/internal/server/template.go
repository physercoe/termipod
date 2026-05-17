package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	hub "github.com/termipod/hub"
	"gopkg.in/yaml.v3"
)

// Template rendering for spawn specs. We interpolate a small, fixed set of
// variables so spawn YAML can reference state that only exists at spawn
// time — most notably the parent agent's journal and the principal handle
// requesting the spawn. Rendering is pre-YAML: the result is then persisted
// and handed to host-runner verbatim.
//
// Variable shape: {{name}}. We intentionally do not support expressions,
// filters, or control flow — spawn specs should stay declarative. If a var
// is not defined for a given spawn (e.g. {{journal}} with no parent), it
// expands to an empty string rather than leaving the placeholder behind;
// that way the rendered YAML is always parseable even when data is missing.
//
// Security: only data already visible to the caller feeds these variables.
// {{journal}} reads from the parent agent's markdown file — the parent is
// owned by the same team as the spawner, so no cross-team leak. We never
// expand tokens, passwords, or anything from flutter_secure_storage; those
// don't live on the hub.

// Names may contain dots so prompts can reference structured-looking
// identifiers like {{principal.handle}}. The hub does not implement true
// nested lookups — the dotted form is just a different key — but it lets
// templates read more naturally.
var tmplVarRe = regexp.MustCompile(`\{\{\s*([a-z_][a-z0-9_.]*)\s*\}\}`)

// buildSpawnVars assembles the variable map shared by renderSpawnSpec and
// resolveContextFiles so the same {{handle}}/{{principal.handle}} bindings
// expand to the same values in both the spec YAML and the prompt body.
//
// Two of the bindings — {{model}} and {{permission_flag}} — are sourced
// from the spec yaml itself rather than from Go-side defaults. That way
// adding a new model or permission mode is a YAML edit, never a code
// change. The shape we read is:
//
//	backend:
//	  model: claude-opus-4-7
//	  permission_modes:
//	    skip:   --dangerously-skip-permissions
//	    prompt: --permission-prompt-tool mcp__termipod__permission_prompt
//
// Missing entries collapse to empty strings so a partially-filled
// template still spawns without 400ing on placeholder expansion.
func (s *Server) buildSpawnVars(ctx context.Context, team string, in spawnIn, principal string) (map[string]string, error) {
	// Resolve `template: <name>` indirection before reading backend vars
	// so {{model}} and {{permission_flag}} see the same backend block
	// that renderSpawnSpec ultimately substitutes into. Without this
	// merge, a steward that sends `spawn_spec_yaml: "template: agents.coder"`
	// gets an empty {{model}} → the rendered cmd becomes
	// `claude --model --print …` → the claude CLI treats `--print` as
	// the model value → API returns 400 "passed --print" and the
	// worker dies on its first turn (v1.0.624 incident; the v1.0.620
	// W1 merge fixed the spec path but missed the var-extraction path).
	// Merge failures fall through to the unmerged spec so callers that
	// embed backend.model inline keep working; renderSpawnSpec re-runs
	// the same merge and surfaces any real error with full context.
	specForVars := in.SpawnSpec
	if merged, err := s.mergeTemplateReference(in.SpawnSpec); err == nil {
		specForVars = merged
	}
	model, permFlag := backendVarsFromSpec(specForVars, in.PermissionMode)

	resolvedPrincipal := firstNonEmpty(principal, "@principal")
	vars := map[string]string{
		"handle":           in.ChildHandle,
		"kind":             in.Kind,
		"team":             team,
		"now":              time.Now().UTC().Format(time.RFC3339),
		"principal":        resolvedPrincipal,
		"principal.handle": strings.TrimPrefix(resolvedPrincipal, "@"),
		"model":            model,
		"permission_flag":  permFlag,
		// MCP server namespace is a wire-protocol constant, owned by the
		// hub root package so server + hostrunner agree. Templates that
		// build claude flags like --permission-prompt-tool need it
		// embedded inside permission_modes values, which is why
		// expandVars below iterates to a fixed point.
		"mcp_namespace": hub.MCPServerName,
	}
	if in.ParentID != "" {
		parentHandle, journal, err := s.lookupParentContext(ctx, team, in.ParentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		vars["parent_handle"] = parentHandle
		vars["journal"] = journal
	}
	return vars, nil
}

// backendVarsFromSpec partial-parses the spawn spec to read backend.model
// and backend.permission_modes[mode]. Both keys are pure data — Go has no
// hardcoded fallback for them — so a template that doesn't declare them
// expands {{model}} / {{permission_flag}} to "". The yaml unmarshal is
// best-effort: if the spec isn't shaped like a backend block we return
// empty strings rather than failing, since renderSpawnSpec must stay
// usable for ad-hoc specs the operator types directly.
//
// Empty `mode` is rewritten to "skip" before the lookup. Earlier
// behaviour (empty mode → empty permFlag → claude denies Write/Edit/Bash
// in --print stream-json mode because there is neither a permission
// prompt tool nor --dangerously-skip-permissions) silently broke any
// caller that forgot to pass `permission_mode`: notably the
// `agents.spawn` MCP path stewards use, where the schema lacked the
// field entirely until v1.0.617. The mobile spawn sheet,
// general-steward bootstrap, and project-steward delegation all already
// default to "skip"; making the helper match removes the foot-gun
// without changing any explicit caller's behaviour.
func backendVarsFromSpec(spec, mode string) (model, permFlag string) {
	var head struct {
		Backend struct {
			Model           string            `yaml:"model"`
			PermissionModes map[string]string `yaml:"permission_modes"`
		} `yaml:"backend"`
	}
	if err := yaml.Unmarshal([]byte(spec), &head); err != nil {
		return "", ""
	}
	if mode == "" {
		mode = "skip"
	}
	permFlag = head.Backend.PermissionModes[mode]
	return head.Backend.Model, permFlag
}

// expandVars replaces every {{name}} occurrence in s with vars[name];
// missing keys collapse to the empty string so the output stays parseable.
//
// Substitution iterates to a fixed point (capped at expandVarsMaxPasses)
// so a value can reference another var: e.g. permission_flag's value
// embeds {{mcp_namespace}}, which only resolves after the first pass
// substitutes permission_flag itself. The cap prevents pathological
// recursion if a future var ever references itself; in practice two
// passes suffice for the templates we ship.
func expandVars(s string, vars map[string]string) string {
	const expandVarsMaxPasses = 5
	for i := 0; i < expandVarsMaxPasses; i++ {
		if !strings.Contains(s, "{{") {
			return s
		}
		next := tmplVarRe.ReplaceAllStringFunc(s, func(match string) string {
			m := tmplVarRe.FindStringSubmatch(match)
			if m == nil {
				return match
			}
			return vars[m[1]]
		})
		if next == s {
			return next
		}
		s = next
	}
	return s
}

// renderSpawnSpec expands {{var}} placeholders in spec using values derived
// from the Server state + the authenticated request. The returned string is
// ready to persist as spawn_spec_yaml.
//
// Context is used for DB reads (journal lookup); on any sub-lookup failure we
// substitute the empty string and keep going — partial data beats a spawn
// hard-failing because the parent happens to have no journal yet.
//
// Pre-{{var}}-expansion, if the spec contains a `template: <name>` key,
// W1 loads that template file and deep-merges its contents under the
// spec (spec values override template values; nested maps merge
// recursively). This lets a steward send `spawn_spec_yaml: "template:
// coder.v1"` as shorthand for "use this template's full backend
// config" — without this merge, the spec arrives at host-runner with
// `backend.cmd == ""` and falls through to the launcher placeholder
// (the v1.0.619 incident). See
// docs/discussions/validate-at-every-boundary.md §1.
func (s *Server) renderSpawnSpec(ctx context.Context, team string, in spawnIn, principal string) (string, error) {
	spec, err := s.mergeTemplateReference(in.SpawnSpec)
	if err != nil {
		return "", err
	}
	if !strings.Contains(spec, "{{") {
		return spec, nil
	}
	vars, err := s.buildSpawnVars(ctx, team, in, principal)
	if err != nil {
		return "", err
	}
	return expandVars(spec, vars), nil
}

// mergeTemplateReference: if the spec carries `template: <name>`, load
// that agent template and deep-merge it under the spec. Spec values
// always win over template values; nested maps merge recursively so
// the steward can override e.g. backend.model while keeping the
// template's backend.cmd. Missing template name is a 400. No
// `template:` key → spec returned unchanged.
func (s *Server) mergeTemplateReference(spec string) (string, error) {
	if spec == "" {
		return spec, nil
	}
	var head struct {
		Template string `yaml:"template"`
	}
	if err := yaml.Unmarshal([]byte(spec), &head); err != nil {
		// Spec doesn't parse as YAML — leave it for downstream to
		// reject with a more specific error.
		return spec, nil
	}
	if head.Template == "" {
		return spec, nil
	}
	tmplBody, err := s.readAgentTemplate(head.Template)
	if err != nil {
		return "", fmt.Errorf("template %q referenced by spawn spec not found: %w",
			head.Template, err)
	}
	// Decode both into untyped maps for deep merge.
	var tmplMap, specMap map[string]any
	if err := yaml.Unmarshal([]byte(tmplBody), &tmplMap); err != nil {
		return "", fmt.Errorf("template %q has invalid YAML: %w", head.Template, err)
	}
	if err := yaml.Unmarshal([]byte(spec), &specMap); err != nil {
		// Already tried above; this branch only fires if spec parsed
		// for `head` extraction but not as a full map (e.g. a list).
		return spec, nil
	}
	if specMap == nil {
		specMap = map[string]any{}
	}
	if tmplMap == nil {
		tmplMap = map[string]any{}
	}
	merged := deepMergeYAMLMaps(tmplMap, specMap)
	// Drop the `template:` indirection field from the merged output
	// so host-runner doesn't see it (the spec it persists is the
	// fully-resolved one).
	delete(merged, "template")
	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("re-encode merged spec: %w", err)
	}
	return string(out), nil
}

// deepMergeYAMLMaps merges `over` into `base`, returning a new map.
// For each key in `over`:
//   - if both `base[k]` and `over[k]` are maps, merge recursively
//   - otherwise, `over[k]` wins (including the case where `over[k]`
//     is nil — explicit clearing)
// Keys only in `base` are preserved. Lists are replaced, not merged
// (a spec's `fallback_modes: [M4]` overrides the template's, doesn't
// append).
func deepMergeYAMLMaps(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, vOver := range over {
		if vBase, ok := out[k]; ok {
			bm, baseIsMap := vBase.(map[string]any)
			om, overIsMap := vOver.(map[string]any)
			if baseIsMap && overIsMap {
				out[k] = deepMergeYAMLMaps(bm, om)
				continue
			}
		}
		out[k] = vOver
	}
	return out
}

// readAgentTemplate finds the template whose internal `template:`
// field matches `name` (e.g. `agents.coder`, `agents.steward.general`)
// and returns its body. The lookup is canonical-form only: callers
// reference templates by the internal id documented in
// `docs/reference/agent-template-naming.md`, not by file basename.
// This mirrors the host-runner template loader at
// `hub/internal/hostrunner/templates.go` which also keys on the
// internal `template:` field.
//
// Lookup order: user-overlaid templates at <dataRoot>/team/templates/
// agents/ win over bundled ones at hub.TemplatesFS:templates/agents/.
// Within each tier we scan all .yaml files and match on the internal
// `template:` field.
func (s *Server) readAgentTemplate(name string) (string, error) {
	if !safeAgentTemplateName(name) {
		return "", fmt.Errorf("invalid template name %q", name)
	}
	// User overlay first.
	overlayDir := filepath.Join(s.cfg.DataRoot, "team", "templates", "agents")
	if body, ok := scanOverlayForTemplate(overlayDir, name); ok {
		return body, nil
	}
	// Bundled fallback.
	if body, ok := scanEmbeddedForTemplate(name); ok {
		return body, nil
	}
	return "", fmt.Errorf("template %q not found in overlay (%s) or bundled templates", name, overlayDir)
}

func scanOverlayForTemplate(dir, name string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".yaml")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if templateMatches(data, base, name) {
			return string(data), true
		}
	}
	return "", false
}

func scanEmbeddedForTemplate(name string) (string, bool) {
	entries, err := fs.ReadDir(hub.TemplatesFS, "templates/agents")
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".yaml")
		data, err := fs.ReadFile(hub.TemplatesFS, "templates/agents/"+e.Name())
		if err != nil {
			continue
		}
		if templateMatches(data, base, name) {
			return string(data), true
		}
	}
	return "", false
}

// templateMatches checks whether the file body declares an internal
// `template:` field equal to `query`. The lookup is canonical-form
// only: agent templates MUST be referenced by their internal id
// (e.g. `agents.coder`), not by file basename — see
// `docs/reference/agent-template-naming.md` for why the `agents.`
// prefix is load-bearing. v1.0.620 briefly accepted both forms as a
// band-aid for an undocumented convention; v1.0.621 removed the
// dual-form lookup once the spec was formalised.
func templateMatches(body []byte, _, query string) bool {
	var head struct {
		Template string `yaml:"template"`
	}
	if err := yaml.Unmarshal(body, &head); err != nil {
		return false
	}
	return head.Template == query
}

// safeAgentTemplateName rejects names that could be exploited if a
// future code path ever does a filename-based lookup. Today's lookup
// is name-based (scans for internal `template:` field match) so path
// traversal is structurally impossible — but the predicate is cheap
// insurance.
func safeAgentTemplateName(n string) bool {
	if n == "" || strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") {
		return false
	}
	return true
}

// contextFileNameForKind returns the on-disk filename the given engine
// reads as its persona-memory file. Different engines have different
// conventions — Claude Code reads CLAUDE.md, Codex + Kimi read
// AGENTS.md (per the AGENTS.md cross-engine spec), Gemini CLI reads
// GEMINI.md. Hub's job is to land the prompt body under the name the
// engine will actually open; otherwise the materialized file is dead
// weight in the workdir and the persona / task body never reaches the
// running engine. Empty or unknown kind falls back to CLAUDE.md so a
// hand-rolled spec without a backend block keeps its prior behaviour.
func contextFileNameForKind(kind string) string {
	switch kind {
	case "codex", "kimi-code":
		return "AGENTS.md"
	case "gemini-cli":
		return "GEMINI.md"
	default:
		return "CLAUDE.md"
	}
}

// resolveContextFiles loads the prompt file referenced by a rendered spawn
// spec (`prompt: <filename>`), expands {{var}} placeholders in it using the
// same bindings as the spec, and inlines the result into the spec under
// the engine-specific memory filename (see contextFileNameForKind). The
// host-runner launcher writes context_files entries to the agent's
// workdir before launch — that's how every supported engine sees the
// steward persona without the hub having to copy files around itself.
//
// personaSeed is a user-supplied addendum (typed into the mobile bootstrap
// sheet) appended under a "Persona override" section so the agent picks
// up both the templated body and the operator's customization. Empty
// seed = no addendum.
//
// taskInstructions is the ADR-029 task body that the steward delegated
// to this worker. When non-empty it lands in a `## Task` section after
// any persona seed so the worker reads "what to do" the first time it
// opens its memory file — without this, the body_md the steward passed
// into `agents.spawn task: {…}` would silently die on the hub side and
// the steward would have to follow up with `a2a.invoke` to actually
// deliver the work. Empty string = no task section.
//
// Behaviour:
//   - No `prompt:` field, no override, no seed, no task → return unchanged.
//   - Spec already declares the engine's memory file under
//     `context_files` (e.g. `context_files.AGENTS.md` for a codex spawn)
//     → respect it. Explicit override beats the templated default. The
//     seed and task are ignored in this branch — explicit override
//     means the operator wrote the memory file by hand and shouldn't
//     get surprise concatenation.
//   - Otherwise: read the prompt body (from disk overlay → embedded FS),
//     expand vars, optionally append the seed, optionally append the
//     task, and emit a context_files block keyed by the engine's
//     memory filename.
//   - Prompt file not found on disk overlay or embedded FS → error.
func (s *Server) resolveContextFiles(rendered string, vars map[string]string, personaSeed, taskInstructions string) (string, error) {
	var head struct {
		Prompt       string            `yaml:"prompt"`
		ContextFiles map[string]string `yaml:"context_files"`
		Backend      struct {
			Kind string `yaml:"kind"`
		} `yaml:"backend"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &head); err != nil {
		// Spec isn't shaped like our header — leave it alone rather than
		// 400'ing a spawn that may parse fine on the host-runner side.
		return rendered, nil
	}
	fname := contextFileNameForKind(head.Backend.Kind)
	if _, hasOverride := head.ContextFiles[fname]; hasOverride {
		return rendered, nil
	}
	hasSeed := strings.TrimSpace(personaSeed) != ""
	hasTask := strings.TrimSpace(taskInstructions) != ""
	if head.Prompt == "" && !hasSeed && !hasTask {
		return rendered, nil
	}

	var body string
	if head.Prompt != "" {
		raw, err := s.readPromptTemplate(head.Prompt)
		if err != nil {
			return "", fmt.Errorf("read prompt %q: %w", head.Prompt, err)
		}
		body = expandVars(raw, vars)
	}
	if hasSeed {
		body = appendPersonaSeed(body, personaSeed)
	}
	if hasTask {
		body = appendTaskSection(body, taskInstructions)
	}

	extra, err := yaml.Marshal(map[string]any{
		"context_files": map[string]string{fname: body},
	})
	if err != nil {
		return "", err
	}
	sep := "\n"
	if strings.HasSuffix(rendered, "\n") || rendered == "" {
		sep = ""
	}
	return rendered + sep + string(extra), nil
}

// appendTaskSection concatenates the ADR-029 task instructions onto the
// templated agent-memory file (CLAUDE.md / AGENTS.md / GEMINI.md
// depending on engine — see contextFileNameForKind) under a dedicated
// `## Task` header. The header is distinct from `## Persona override`
// so the worker (and a human reading the materialized file) can tell
// "who I am" from "what to do". Called by resolveContextFiles only
// when the spawn carries a task linkage.
func appendTaskSection(body, instructions string) string {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return body
	}
	const header = "## Task"
	if body == "" {
		return header + "\n\n" + instructions + "\n"
	}
	sep := "\n\n"
	if strings.HasSuffix(body, "\n\n") {
		sep = ""
	} else if strings.HasSuffix(body, "\n") {
		sep = "\n"
	}
	return body + sep + header + "\n\n" + instructions + "\n"
}

// appendPersonaSeed concatenates the operator's seed onto the templated
// agent-memory file (CLAUDE.md / AGENTS.md / GEMINI.md depending on
// engine — see contextFileNameForKind) under a clearly-labeled section
// so the agent (and a human reading the materialized file) can tell the
// two apart.
func appendPersonaSeed(body, seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return body
	}
	const header = "## Persona override"
	if body == "" {
		return header + "\n\n" + seed + "\n"
	}
	sep := "\n\n"
	if strings.HasSuffix(body, "\n\n") {
		sep = ""
	} else if strings.HasSuffix(body, "\n") {
		sep = "\n"
	}
	return body + sep + header + "\n\n" + seed + "\n"
}

// readPromptTemplate prefers <dataRoot>/team/templates/prompts/<name> so
// user edits win, then falls back to the embedded built-in.
func (s *Server) readPromptTemplate(name string) (string, error) {
	if !safePromptName(name) {
		return "", fmt.Errorf("invalid prompt name %q", name)
	}
	path := filepath.Join(s.cfg.DataRoot, "team", "templates", "prompts", name)
	data, err := os.ReadFile(path)
	if err == nil {
		return string(data), nil
	}
	if !errors.Is(err, fs.ErrNotExist) && !os.IsNotExist(err) {
		return "", err
	}
	embedded, err := fs.ReadFile(hub.TemplatesFS, "templates/prompts/"+name)
	if err != nil {
		return "", err
	}
	return string(embedded), nil
}

func safePromptName(n string) bool {
	if n == "" || strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") {
		return false
	}
	return true
}

// lookupParentContext returns (handle, journalContent). Missing journal file
// is reported as (handle, "", nil) rather than an error — a parent without
// notes is common early in a task.
func (s *Server) lookupParentContext(ctx context.Context, team, parentID string) (string, string, error) {
	var handle string
	err := s.db.QueryRowContext(ctx,
		`SELECT handle FROM agents WHERE team_id = ? AND id = ?`, team, parentID).Scan(&handle)
	if err != nil {
		return "", "", err
	}
	path, err := s.journalPath(team, handle)
	if err != nil {
		return handle, "", nil
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return handle, "", nil
	}
	if err != nil {
		return handle, "", err
	}
	return handle, string(body), nil
}

// principalFromScope pulls the caller handle out of an auth token's scope
// JSON. Tokens issued with -handle carry scope.handle (e.g. "physercoe"); we
// prefer that so templates rendering {{principal}} point at a real human.
// Older tokens fall back to `@role` (e.g. `@principal`, `@host`).
func principalFromScope(scopeJSON string) string {
	if scopeJSON == "" {
		return "@principal"
	}
	var s struct {
		Role   string `json:"role"`
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal([]byte(scopeJSON), &s); err != nil {
		return "@principal"
	}
	if s.Handle != "" {
		return "@" + s.Handle
	}
	if s.Role != "" {
		return "@" + s.Role
	}
	return "@principal"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
