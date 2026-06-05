package server

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	hub "github.com/termipod/hub"
	"gopkg.in/yaml.v3"
)

// agentTemplateVersionSuffix matches the trailing `.v<N>` segment of
// a bundled agent-template filename so the audit can derive the
// basename (e.g. `coder.v1` → `coder`, `steward.general.v1` →
// `steward.general`). The corresponding internal `template:` field
// must be `agents.<basename>` per
// `docs/reference/agent-template-naming.md`.
var agentTemplateVersionSuffix = regexp.MustCompile(`\.v\d+$`)

// W10b: bundled-template audit. Called from server.New on every hub
// start. Walks every `templates/agents/*.yaml` in the embedded FS,
// decodes the file, and verifies that the load-bearing fields the
// spawn pipeline depends on are present. Returns an aggregated error
// naming every broken template + the field that's missing; refuse to
// start so the operator notices at deploy time rather than at first
// steward-spawn (the v1.0.619 incident shape).
//
// What "load-bearing" means today:
//
//   - `template:` — file must declare an internal name. The hub-side
//     template merge (W1) and the host-runner template index (W2) both
//     key off this field.
//   - `backend.cmd` — required for any spawn that resolves to this
//     template. An empty cmd means "no engine to launch" and falls
//     through to layers that pre-bundle would have run interactive
//     bash; post-bundle (W4 + W7 + W8) those layers refuse, but a
//     spawn for this template still fails — better to catch the
//     broken template at hub start.
//
// User-overlaid templates at <dataRoot>/team/templates/agents/ are
// NOT audited here: operators are expected to verify their own
// overrides, and hard-failing on a stale user overlay could make a
// production hub unbootable. The CI lint (W10c) would catch this for
// templates that land in main; the runtime audit's scope is the
// shipped-binary's bundled set.
func auditBundledAgentTemplates() error {
	type brokenTemplate struct {
		Path   string
		Reason string
	}
	var broken []brokenTemplate

	walkErr := fs.WalkDir(hub.TemplatesFS, "templates/agents",
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
				return nil
			}
			data, err := fs.ReadFile(hub.TemplatesFS, path)
			if err != nil {
				broken = append(broken, brokenTemplate{
					Path:   path,
					Reason: fmt.Sprintf("read: %v", err),
				})
				return nil
			}
			reason := validateBundledAgentTemplate(data)
			if reason != "" {
				broken = append(broken, brokenTemplate{Path: path, Reason: reason})
				return nil
			}
			// Filename↔internal-id match check. Per
			// docs/reference/agent-template-naming.md the file
			// `<basename>.v<N>.yaml` must declare
			// `template: agents.<basename>`.
			if mismatch := validateAgentTemplateNameMatch(path, data); mismatch != "" {
				broken = append(broken, brokenTemplate{Path: path, Reason: mismatch})
			}
			return nil
		})
	if walkErr != nil {
		return fmt.Errorf("walk bundled templates: %w", walkErr)
	}
	if len(broken) == 0 {
		return nil
	}
	// Stable order so error messages diff cleanly across runs.
	sort.Slice(broken, func(i, j int) bool {
		return broken[i].Path < broken[j].Path
	})
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d bundled agent template(s) failed validation:", len(broken)))
	for _, x := range broken {
		b.WriteString("\n  - ")
		b.WriteString(x.Path)
		b.WriteString(": ")
		b.WriteString(x.Reason)
	}
	return fmt.Errorf("%s", b.String())
}

// validateBundledAgentTemplate returns "" when the template is OK or
// a short human reason when it's not. Decoupled from the audit walker
// so unit tests can exercise the validator in isolation.
func validateBundledAgentTemplate(data []byte) string {
	var doc struct {
		Template string `yaml:"template"`
		Backend  struct {
			Cmd string `yaml:"cmd"`
		} `yaml:"backend"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Sprintf("YAML parse: %v", err)
	}
	if strings.TrimSpace(doc.Template) == "" {
		return "missing top-level `template:` field"
	}
	if strings.TrimSpace(doc.Backend.Cmd) == "" {
		return "missing or empty `backend.cmd` field"
	}
	return ""
}

// boundSpawnVarNames is the canonical allowlist of var names the
// spawn pipeline binds in `buildSpawnVars`. Used by the bundled-asset
// var-reference audit below to catch any `{{var}}` reference in a
// shipped template/prompt that would silently expand to the empty
// string at render time — the v1.0.625 incident class: prompts used
// `{{parent.handle}}` (the bound key was `parent_handle`) and
// `{{project_id}}` (unbound at all). Both rendered to "" without any
// surfaced error, and the steward.research.v1 examples produced
// malformed `project_id: ` lines in worker spawns.
//
// Keep in sync with `buildSpawnVars`. Conditional entries — those
// only bound when the spawning agent has a parent — are listed in
// boundSpawnVarNamesConditional so the audit can permit them in
// worker prompts (which are always parented) while still flagging
// e.g. a steward prompt that references `{{parent.handle}}`.
var boundSpawnVarNames = map[string]bool{
	"handle":           true,
	"kind":             true,
	"team":             true,
	"now":              true,
	"principal":        true,
	"principal.handle": true,
	"model":            true,
	"permission_flag":  true,
	"mcp_namespace":    true,
	"project_id":       true,
}

// boundSpawnVarNamesConditional are vars only set when the spawn has
// a parent (in.ParentID != ""). Bundled worker prompts may reference
// these because workers are always parented; bundled steward
// prompts MUST NOT (general/research/infra stewards are top-level
// and parent_handle / journal would render empty).
var boundSpawnVarNamesConditional = map[string]bool{
	"parent_handle": true,
	"parent.handle": true,
	"journal":       true,
}

// W10d: bundled-asset var-reference audit. Called from server.New
// alongside auditBundledAgentTemplates. Walks every
// `templates/agents/*.yaml` and `templates/prompts/*` in the
// embedded FS, extracts every `{{var}}` reference, and refuses to
// start if any reference points at a name not in
// boundSpawnVarNames (or boundSpawnVarNamesConditional for prompts
// known to always run in a parented context).
//
// Why a runtime audit and not just a unit test: the unit test runs
// in CI but a contributor authoring an overlay or a new bundled
// prompt locally without running tests would otherwise ship the
// silent-empty-expansion bug. The audit fires on every hub start.
// User-overlaid prompts are not audited for the same reason as the
// template audit — overlays are operator-owned and shouldn't make
// production unbootable.
func auditBundledTemplateVarRefs() error {
	type brokenRef struct {
		Path    string
		Var     string
		Allowed string // hint string for the error message
	}
	var broken []brokenRef

	scan := func(root string, allowConditional bool) error {
		return fs.WalkDir(hub.TemplatesFS, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			data, err := fs.ReadFile(hub.TemplatesFS, path)
			if err != nil {
				return nil // surfaced by the other audit
			}
			refs := extractTmplVarRefs(data)
			conditional := allowConditional || promptAlwaysParented(path)
			for _, name := range refs {
				if boundSpawnVarNames[name] {
					continue
				}
				if conditional && boundSpawnVarNamesConditional[name] {
					continue
				}
				broken = append(broken, brokenRef{
					Path:    path,
					Var:     name,
					Allowed: bindingHint(name),
				})
			}
			return nil
		})
	}
	if err := scan("templates/agents", false); err != nil {
		return fmt.Errorf("walk bundled agent templates: %w", err)
	}
	if err := scan("templates/prompts", false); err != nil {
		return fmt.Errorf("walk bundled prompts: %w", err)
	}
	if len(broken) == 0 {
		return nil
	}
	sort.Slice(broken, func(i, j int) bool {
		if broken[i].Path != broken[j].Path {
			return broken[i].Path < broken[j].Path
		}
		return broken[i].Var < broken[j].Var
	})
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"%d bundled template var-reference(s) unbound at render time:",
		len(broken)))
	for _, x := range broken {
		b.WriteString("\n  - ")
		b.WriteString(x.Path)
		b.WriteString(": {{")
		b.WriteString(x.Var)
		b.WriteString("}}")
		if x.Allowed != "" {
			b.WriteString(" — ")
			b.WriteString(x.Allowed)
		}
	}
	return fmt.Errorf("%s", b.String())
}

// promptAlwaysParented returns true for bundled prompts that are
// only ever materialized for workers (which always have a parent
// steward). Those prompts may reference {{parent.handle}} /
// {{parent_handle}} / {{journal}}.
//
// Maintenance: when adding a new worker template, register its
// prompt basename here. Steward prompts (general/research/infra/etc)
// MUST NOT be added — they're spawned without a parent.
func promptAlwaysParented(path string) bool {
	base := filepath.Base(path)
	switch base {
	case "coder.v1.md",
		"critic.v1.md",
		"lit-reviewer.v1.md",
		"ml-worker.v1.md",
		"paper-writer.v1.md",
		"briefing.v1.md",
		"worker.v1.md",
		"worker_report.v1.md":
		return true
	}
	return false
}

// bindingHint returns a short suggestion for a contributor staring
// at the audit error about which bound name they likely meant.
func bindingHint(name string) string {
	switch name {
	case "parent.handle":
		return "did you mean to mark this prompt as worker-only? (it's bound only when ParentID is set)"
	case "project_id":
		return "bound from in.ProjectID; ensure the spawn carries project_id"
	}
	return "not bound by buildSpawnVars; see template.go boundSpawnVarNames"
}

// extractTmplVarRefs returns every distinct {{name}} reference in
// data, deduplicated. Uses the same regex as expandVars so the audit
// catches exactly the names the renderer would try to substitute.
func extractTmplVarRefs(data []byte) []string {
	matches := tmplVarRe.FindAllSubmatch(data, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		name := string(m[1])
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// validateAgentTemplateNameMatch enforces the file-to-internal-id
// match documented in `docs/reference/agent-template-naming.md`:
//
//	hub/templates/agents/<basename>.v<N>.yaml
//	          ⇡⇡⇡⇡⇡⇡⇡⇡⇡
//	internal `template:` MUST equal `agents.<basename>`
//
// The basename is the filename without the trailing `.v<N>.yaml`.
// Returns "" when the names match, a structured message otherwise.
// The check is intentionally separate from
// validateBundledAgentTemplate so it can be invoked only for files
// under templates/agents/ (project templates have a different
// convention — no `agents.` prefix; see template-yaml-schema.md).
func validateAgentTemplateNameMatch(path string, data []byte) string {
	var doc struct {
		Template string `yaml:"template"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		// Already reported by validateBundledAgentTemplate; skip.
		return ""
	}
	filename := filepath.Base(path)
	noYAML := strings.TrimSuffix(filename, ".yaml")
	basename := agentTemplateVersionSuffix.ReplaceAllString(noYAML, "")
	if basename == noYAML {
		// File didn't end in `.v<N>` — the naming spec requires it,
		// but tolerate (other validators will flag missing fields).
		// Return "" so we don't double-report.
		return ""
	}
	expected := "agents." + basename
	if doc.Template != expected {
		return fmt.Sprintf(
			"`template:` field %q does not match filename basename — expected %q "+
				"(see docs/reference/agent-template-naming.md)",
			doc.Template, expected)
	}
	return ""
}
