package hostrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/termipod/hub/internal/agentfamilies"
)

// AgentCap is the per-family probe result. When Installed is false, Version
// and Supports are empty — the hub UI renders "not installed" in that case.
type AgentCap struct {
	Installed bool     `json:"installed"`
	Version   string   `json:"version,omitempty"`
	Supports  []string `json:"supports,omitempty"`
}

// Capabilities is the payload written to hosts.capabilities_json via
// PUT /v1/teams/{team}/hosts/{host}/capabilities.
type Capabilities struct {
	Agents   map[string]AgentCap `json:"agents"`
	ProbedAt string              `json:"probed_at"`
}

// ProbeCapabilities runs exec.LookPath for each known family and, if
// present, invokes its version command with a 2s per-binary timeout.
// The family list comes from agentfamilies (embedded YAML) — the probe
// is intentionally schema-driven so adding a CLI never lands as a Go
// edit. The outer ctx bounds the whole sweep; individual slow
// binaries do not stall the others beyond their own timeout.
func ProbeCapabilities(ctx context.Context) Capabilities {
	fams, _ := agentfamilies.All()
	out := Capabilities{
		Agents:   make(map[string]AgentCap, len(fams)),
		ProbedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, a := range fams {
		path, err := exec.LookPath(a.Bin)
		if err != nil || path == "" {
			out.Agents[a.Family] = AgentCap{Installed: false}
			continue
		}
		ac := AgentCap{Installed: true, Supports: append([]string(nil), a.Supports...)}
		if a.VersionFlag != "" {
			if v, ok := runVersion(ctx, path, a.VersionFlag); ok {
				ac.Version = v
			}
		}
		out.Agents[a.Family] = ac
	}
	return out
}

// runVersion executes `bin flag` with a 2s timeout and returns the trimmed
// first line. A non-zero exit or empty output yields ok=false; the caller
// treats that as "present but unknown version" rather than missing.
func runVersion(ctx context.Context, bin, flag string) (string, bool) {
	sub, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := exec.CommandContext(sub, bin, flag).Output()
	if err != nil {
		return "", false
	}
	return parseVersion(string(b)), true
}

// parseVersion normalises the first non-empty line of a --version output.
// Exported for the parse-only unit test path where spawning a binary is
// awkward (e.g. CI without the real CLIs installed).
func parseVersion(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// Hash returns a stable hex digest of the capabilities payload, ignoring
// ProbedAt so timestamp churn alone does not force a PUT. Keys are sorted
// to make json.Marshal deterministic regardless of map iteration order.
func (c Capabilities) Hash() string {
	type pair struct {
		K string   `json:"k"`
		V AgentCap `json:"v"`
	}
	keys := make([]string, 0, len(c.Agents))
	for k := range c.Agents {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	flat := make([]pair, 0, len(keys))
	for _, k := range keys {
		v := c.Agents[k]
		if v.Supports != nil {
			sup := append([]string(nil), v.Supports...)
			sort.Strings(sup)
			v.Supports = sup
		}
		flat = append(flat, pair{k, v})
	}
	b, _ := json.Marshal(flat)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
