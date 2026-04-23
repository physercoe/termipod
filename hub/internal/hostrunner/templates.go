package hostrunner

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/termipod/hub/internal/hostrunner/a2a"
	"gopkg.in/yaml.v3"
)

// agentTemplate captures the slice of templates/agents/<kind>.yaml that the
// host-runner actually needs at launch + card-serving time. Broader fields
// (role, capabilities, prompt) are interpreted by the hub; the runner stays
// deliberately narrow so an unrelated template-schema addition doesn't need
// a host-runner update.
type agentTemplate struct {
	Kind       string
	Skills     []a2a.Skill
	BackendCmd string
}

// agentTemplates is a kind → template index. Construct once per runner via
// loadAgentTemplates and look up with Skills/BackendCmd. Unknown kinds return
// zero values; callers fall back accordingly. This keeps the loader total,
// which is the right shape for data sources that may legitimately be absent
// (fresh agent kind, template still in review).
type agentTemplates struct {
	byKind map[string]agentTemplate
}

// Skills returns the advertised A2A skills for the given template kind.
// nil is a valid answer (no skills declared).
func (a *agentTemplates) Skills(kind string) []a2a.Skill {
	if a == nil {
		return nil
	}
	return a.byKind[kind].Skills
}

// BackendCmd returns the shell command declared under backend.cmd in the
// template, or "" when the template has no entry for this kind. Callers
// that launch panes treat "" as "use the launcher default".
func (a *agentTemplates) BackendCmd(kind string) string {
	if a == nil {
		return ""
	}
	return a.byKind[kind].BackendCmd
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
	byKind := map[string]agentTemplate{}
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
		byKind[doc.Template] = agentTemplate{
			Kind:       doc.Template,
			Skills:     doc.Skills,
			BackendCmd: doc.Backend.Cmd,
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return &agentTemplates{byKind: byKind}, nil
}
