// Package a2a implements the host-runner side of the A2A protocol
// (agent-to-agent). Per blueprint §5.4, each host-runner is an A2A terminus
// and exposes one endpoint per live agent, with an agent-card at
// /a2a/<agent-id>/.well-known/agent.json.
//
// This package covers P3.2: serving agent-cards. Task dispatch, hub
// directory publish, and cross-host relay are separate wedges.
package a2a

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
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

const ProtocolVersion = "0.3.0"

// SkillsForHandle returns the MVP skill set for an agent, keyed off its
// handle. Temporary until agent templates carry an explicit skills list.
// Unknown handles get no skills — the card still serves, but is a no-op
// for A2A callers.
func SkillsForHandle(handle string) []Skill {
	switch {
	case matches(handle, "steward"):
		return []Skill{
			{ID: "plan", Name: "plan", Description: "Decompose a project goal into plan steps", Tags: []string{"planning"}},
			{ID: "brief", Name: "brief", Description: "Write a briefing document summarizing run outputs", Tags: []string{"writing"}},
		}
	case matches(handle, "ml-worker"), matches(handle, "worker"):
		return []Skill{
			{ID: "train", Name: "train", Description: "Run a training config and log metrics to trackio", Tags: []string{"ml", "training"}},
		}
	case matches(handle, "briefing"):
		return []Skill{
			{ID: "brief", Name: "brief", Description: "Write a briefing document summarizing run outputs", Tags: []string{"writing"}},
		}
	default:
		return nil
	}
}

func matches(handle, prefix string) bool {
	if len(handle) < len(prefix) {
		return false
	}
	return handle[:len(prefix)] == prefix
}
