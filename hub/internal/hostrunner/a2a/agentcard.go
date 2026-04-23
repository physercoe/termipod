// Package a2a implements the host-runner side of the A2A protocol
// (agent-to-agent). Per blueprint §5.4, each host-runner is an A2A terminus
// and exposes one endpoint per live agent, with an agent-card at
// /a2a/<agent-id>/.well-known/agent.json.
//
// This package covers P3.2: serving agent-cards. Task dispatch, hub
// directory publish, and cross-host relay are separate wedges.
package a2a

import (
	"errors"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentCard is the A2A v0.3 agent-card envelope. Only fields we actually
// populate are modeled; unknown fields are tolerated by clients.
// Spec: https://a2a-protocol.org/latest/specification/
type AgentCard struct {
	ProtocolVersion    string       `json:"protocolVersion"`
	Name               string       `json:"name"`
	Description        string       `json:"description,omitempty"`
	URL                string       `json:"url"`
	Version            string       `json:"version"`
	Capabilities       Capabilities `json:"capabilities"`
	DefaultInputModes  []string     `json:"defaultInputModes"`
	DefaultOutputModes []string     `json:"defaultOutputModes"`
	Skills             []Skill      `json:"skills"`
}

type Capabilities struct {
	Streaming bool `json:"streaming"`
}

type Skill struct {
	ID          string   `json:"id" yaml:"id"`
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

const ProtocolVersion = "0.3.0"

// agentTemplateDoc is the minimal shape of a templates/agents/*.yaml
// file that we need here — just enough to extract the advertised
// skills. The full template (backend, capabilities, channels) is
// interpreted by other subsystems; we intentionally ignore those
// fields so a schema addition there doesn't break this loader.
type agentTemplateDoc struct {
	Template string  `yaml:"template"`
	Skills   []Skill `yaml:"skills"`
}

// SkillsLoader resolves a template kind (e.g. "agents.steward") into
// the skill set it advertises on its A2A card. Returning nil is valid
// and means the agent serves a card with no skills, which is the
// correct answer for a template that doesn't opt in.
type SkillsLoader func(kind string) []Skill

// LoadSkillsFromFS returns a SkillsLoader that walks the given fs.FS
// looking for templates/agents/*.yaml entries and returns the skills
// list of the entry whose `template:` field matches the caller's kind.
// The loader parses the FS once at construction time and caches the
// kind → skills map; host-runners build this at startup off of
// hub.TemplatesFS, so the lookup is O(1) per A2A card request.
//
// Unknown kinds return nil — the caller's fallback policy (e.g. empty
// skills) applies. This is deliberately not an error so a new agent
// kind without a matching template still serves a valid card.
func LoadSkillsFromFS(root fs.FS, dir string) (SkillsLoader, error) {
	byKind := map[string][]Skill{}
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
			// Skip unparseable files rather than fail the whole
			// walk — a single broken template should not take A2A
			// card serving offline for every other agent.
			return nil
		}
		if doc.Template == "" {
			return nil
		}
		byKind[doc.Template] = doc.Skills
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return func(kind string) []Skill {
		return byKind[kind]
	}, nil
}
