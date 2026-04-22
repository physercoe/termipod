// Mode resolution glue for the spawn path (blueprint §5.3.2, P1.4 integ).
//
// The hub parses the narrow subset of spawn_spec_yaml that declares a
// driving mode (+ optional fallbacks), overlays any spawnIn override,
// looks up the target host's capabilities + billing declarations, and
// calls modes.Resolve. The result lands in agents.driving_mode so host-
// runner can pick the right driver when it launches the pane.
//
// Non-goal here: the resolver is advisory when no mode info is supplied
// anywhere — we fall through to "" rather than rejecting spawns that
// predate mode declarations. That's the migration-friendly default.
package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/termipod/hub/internal/modes"
	"gopkg.in/yaml.v3"
)

// spawnModeYAML is the mode-only slice of SpawnSpec we parse on the hub.
// Keeping it local to the server package avoids a hub→host-runner import;
// host-runner's SpawnSpec stays the canonical shape for launcher use.
type spawnModeYAML struct {
	DrivingMode   string   `yaml:"driving_mode"`
	FallbackModes []string `yaml:"fallback_modes"`
}

func parseSpawnModeYAML(text string) spawnModeYAML {
	var s spawnModeYAML
	if text == "" {
		return s
	}
	_ = yaml.Unmarshal([]byte(text), &s) // lenient: unknown keys OK, errors ignored
	return s
}

// hostCapsJSON is the slice of hosts.capabilities_json we read for
// resolution. billing_declarations is an optional extension — if the
// host embeds a per-agent billing decl there, we honor it; otherwise
// billing is BillingUnknown and the resolver treats it permissively.
type hostCapsJSON struct {
	Agents map[string]struct {
		Installed bool     `json:"installed"`
		Supports  []string `json:"supports"`
	} `json:"agents"`
	BillingDeclarations map[string]string `json:"billing_declarations,omitempty"`
}

// loadHostCaps reads hosts.capabilities_json for the named host. Missing
// rows or unparseable JSON yield an empty caps struct — resolution falls
// back to "default-M4" behavior rather than failing spawns on a new host
// whose probe hasn't reported yet.
func (s *Server) loadHostCaps(ctx context.Context, hostID string) hostCapsJSON {
	var empty hostCapsJSON
	if hostID == "" {
		return empty
	}
	var body string
	err := s.db.QueryRowContext(ctx,
		`SELECT capabilities_json FROM hosts WHERE id = ?`, hostID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return empty
	}
	var out hostCapsJSON
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return empty
	}
	return out
}

// resolveSpawnMode returns the concrete driving mode to persist on the
// new agent row, or ("", nil) when no mode info is supplied anywhere —
// in that case host-runner stays on its current M4 default until a
// template or spawn request opts in. A non-nil error means the mode
// info we have is inconsistent with host capabilities; the caller turns
// that into a 400.
func (s *Server) resolveSpawnMode(ctx context.Context, in spawnIn) (string, error) {
	y := parseSpawnModeYAML(in.SpawnSpec)

	// Opt-in guard: if no mode appears in YAML, override, or fallback
	// list, skip resolution entirely — a missing column is legal.
	if in.Mode == "" && y.DrivingMode == "" && len(y.FallbackModes) == 0 {
		return "", nil
	}

	caps := s.loadHostCaps(ctx, in.HostID)
	kindCaps := caps.Agents[in.Kind]
	billing := modes.Billing(caps.BillingDeclarations[in.Kind])

	// Permissive fallback on an unprobed host: if the host row has no
	// capabilities yet, assume the agent is installed and trust the
	// caller's mode declaration. The host-runner probe will tighten this
	// on the next heartbeat; rejecting spawns at this stage would be
	// worse than letting the launcher fail fast with a real error.
	installed := kindCaps.Installed
	supports := kindCaps.Supports
	if len(caps.Agents) == 0 {
		installed = true
		supports = []string{"M1", "M2", "M4"}
	}

	res, err := modes.Resolve(modes.Input{
		AgentKind:     in.Kind,
		Requested:     y.DrivingMode,
		FallbackModes: y.FallbackModes,
		Override:      in.Mode,
		Billing:       billing,
		HostInstalled: installed,
		HostSupports:  supports,
	})
	if err != nil {
		return "", fmt.Errorf("mode resolution failed: %w", err)
	}
	return res.Mode, nil
}
