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
