package hostrunner

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/termipod/hub/internal/hostrunner/a2a"
	"gopkg.in/yaml.v3"
)

// agentTemplate captures the slice of templates/agents/*.yaml that the
// host-runner actually needs at launch + card-serving time. Broader fields
// (role, capabilities, prompt) are interpreted by the hub; the runner stays
// deliberately narrow so an unrelated template-schema addition doesn't need
// a host-runner update.
//
// Name is the value of the file's top-level `template:` field (e.g.
// `agents.coder`, `agents.steward.general`). This is NOT the engine
// kind (`claude-code`, `codex`); callers that have an engine kind must
// resolve to a template name first (via the hub spawn record's
// `agents.kind` column, which post-ADR-025 carries the template id).
type agentTemplate struct {
	Name       string
	Skills     []a2a.Skill
	BackendCmd string
}

// agentTemplates is a template-name → template index. Construct once
// per runner via loadAgentTemplates and look up with Skills/BackendCmd.
// Unknown names return zero values; callers fall back accordingly.
// This keeps the loader total, which is the right shape for data
// sources that may legitimately be absent (fresh template, still in
// review).
//
// W2 cleanup: the field was previously named `byKind`, which was
// misleading because the map is keyed by the internal `template:`
// field of each YAML file (a dotted name like `agents.coder`), NOT
// by engine kind. The v1.0.619 incident's fallback at runner.go
// looked up `BackendCmd(sp.Kind)` where sp.Kind was the engine kind
// `claude-code` — guaranteed to return "" because no template's
// internal name is `claude-code`. The rename makes the contract
// explicit; the fallback path is now also defused upstream by W1's
// hub-side template merge.
type agentTemplates struct {
	byTemplateName map[string]agentTemplate
}

// Skills returns the advertised A2A skills for the given template
// name (e.g. `agents.coder`). nil is a valid answer (no skills
// declared).
func (a *agentTemplates) Skills(templateName string) []a2a.Skill {
	if a == nil {
		return nil
	}
	return a.byTemplateName[templateName].Skills
}

// BackendCmd returns the shell command declared under backend.cmd in
// the template with the given internal `template:` name (e.g.
// `agents.coder`), or "" when no such template exists. Callers that
// launch panes treat "" as "use the launcher default" — post-W7,
// "use the launcher default" itself becomes "refuse to launch."
func (a *agentTemplates) BackendCmd(templateName string) string {
	if a == nil {
		return ""
	}
	return a.byTemplateName[templateName].BackendCmd
}

// agentTemplateDoc is the minimal YAML shape we decode. Any additional
// fields in the template file (capabilities, prompt, channels, …) are
// ignored here and interpreted by the hub.
type agentTemplateDoc struct {
	Template string      `yaml:"template"`
	Skills   []a2a.Skill `yaml:"skills"`
	Backend  struct {
		Cmd string `yaml:"cmd"`
	} `yaml:"backend"`
}

// loadAgentTemplates walks root/<dir>/*.yaml once at startup and indexes each
// file by its `template:` key. Files that fail to parse are skipped so a
// single broken template cannot take the whole launch path offline.
func loadAgentTemplates(root fs.FS, dir string) (*agentTemplates, error) {
	byTemplateName := map[string]agentTemplate{}
	walkErr := fs.WalkDir(root, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		var doc agentTemplateDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil
		}
		if doc.Template == "" {
			return nil
		}
		byTemplateName[doc.Template] = agentTemplate{
			Name:       doc.Template,
			Skills:     doc.Skills,
			BackendCmd: doc.Backend.Cmd,
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return &agentTemplates{byTemplateName: byTemplateName}, nil
}
