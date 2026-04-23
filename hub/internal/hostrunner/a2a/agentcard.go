// Package a2a implements the host-runner side of the A2A protocol
// (agent-to-agent). Per blueprint §5.4, each host-runner is an A2A terminus
// and exposes one endpoint per live agent, with an agent-card at
// /a2a/<agent-id>/.well-known/agent.json.
//
// This package handles HTTP serving of agent-cards and the JSON-RPC task
// endpoints. Data — the skill list, for instance — is supplied by the
// host-runner via Server.Source so the a2a package stays free of
// domain-specific template parsing.
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

// Skill is an A2A card skill entry. yaml tags are populated so the
// host-runner's template loader can unmarshal directly into this type.
type Skill struct {
	ID          string   `json:"id" yaml:"id"`
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

const ProtocolVersion = "0.3.0"
