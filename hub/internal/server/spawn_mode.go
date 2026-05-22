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
	"strings"

	"github.com/termipod/hub/internal/agentfamilies"
	"github.com/termipod/hub/internal/modes"
	"gopkg.in/yaml.v3"
)

// ModeUnsupportedError is returned by resolveSpawnMode when a spawn
// *explicitly* requests a driving mode the target engine family does
// not support (ADR-035 W2). A family's declared `supports` list is a
// static fact about the engine binary — antigravity 1.0.1 has no ACP
// (M1) and no --output-format (M2), so it declares supports:[M4]. That
// floor holds independent of host probing: even on an unprobed host
// (where loadHostCaps falls back to a permissive [M1,M2,M4]), an M1/M2
// request for an M4-only engine must fail fast at the boundary rather
// than resolve to a mode the engine can't speak and then hang at
// launch. handleSpawn renders this as a 422 carrying Hint().
type ModeUnsupportedError struct {
	Family    string
	Mode      string
	Supported []string
}

func (e *ModeUnsupportedError) Error() string {
	return fmt.Sprintf("engine %q does not support driving mode %s (supports: %s)",
		e.Family, e.Mode, strings.Join(e.Supported, ", "))
}

// Hint is the structured recovery envelope handleSpawn surfaces on the
// 422. It tells the caller which modes the engine actually speaks so an
// agent can retry without guessing.
func (e *ModeUnsupportedError) Hint() Hint {
	return Hint{
		HintText: fmt.Sprintf(
			"%s runs only in %s. Omit driving_mode (it auto-resolves) or request one of: %s.",
			e.Family, strings.Join(e.Supported, "/"), strings.Join(e.Supported, ", ")),
		SeeDoc: "docs/spine/protocols.md",
	}
}

// modeSupported reports whether `mode` (already normalized) appears in a
// family's declared supports list, comparing case-insensitively so a
// hand-edited overlay using lower-case still matches.
func modeSupported(supports []string, mode string) bool {
	for _, s := range supports {
		if normalizeMode(s) == mode {
			return true
		}
	}
	return false
}

// normalizeMode upper-cases and validates a driving-mode token, mirroring
// modes.normalize (which is unexported). Returns "" for anything that
// isn't M1/M2/M4 so the family-floor guard ignores blank/garbage values.
func normalizeMode(m string) string {
	switch strings.TrimSpace(strings.ToUpper(m)) {
	case "M1":
		return "M1"
	case "M2":
		return "M2"
	case "M4":
		return "M4"
	}
	return ""
}

// spawnModeYAML is the mode-only slice of SpawnSpec we parse on the hub.
// Keeping it local to the server package avoids a hub→host-runner import;
// host-runner's SpawnSpec stays the canonical shape for launcher use.
//
// backend.kind names the agent *family* (claude-code/codex/gemini-cli) the
// template's binary belongs to. Resolution looks the family up in the
// host's probed capabilities; without this we'd be looking up the spawn's
// `kind` which is sometimes a template id (e.g. steward.general.v1) and
// not a probe-reported family name — which then never matches.
type spawnModeYAML struct {
	DrivingMode   string   `yaml:"driving_mode"`
	FallbackModes []string `yaml:"fallback_modes"`
	Backend       struct {
		Kind string `yaml:"kind"`
	} `yaml:"backend"`
	// ProjectID binds the spawned agent to a project per ADR-025 W2.
	// Templates and mobile both write this under `project_id:` in the
	// rendered spawn YAML; the hub persists it on the `agents` row.
	ProjectID string `yaml:"project_id"`
}

func parseSpawnModeYAML(text string) spawnModeYAML {
	var s spawnModeYAML
	if text == "" {
		return s
	}
	_ = yaml.Unmarshal([]byte(text), &s) // lenient: unknown keys OK, errors ignored
	return s
}

// parsedBackendCmd extracts backend.cmd from a rendered spawn spec
// for the W4 fail-fast gate. Lenient parse — unknown keys, errors,
// and missing backend block all yield "". Callers treat "" as
// "spec is missing the load-bearing field and must be rejected."
func parsedBackendCmd(text string) string {
	if text == "" {
		return ""
	}
	var head struct {
		Backend struct {
			Cmd string `yaml:"cmd"`
		} `yaml:"backend"`
	}
	if err := yaml.Unmarshal([]byte(text), &head); err != nil {
		return ""
	}
	return head.Backend.Cmd
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
	// Family for the host-caps lookup. Most spawn paths (mobile-driven)
	// pass the family directly via in.Kind, so backend.kind is empty in
	// the YAML and we fall back to in.Kind. Internal singletons like
	// steward.general (handlers_general_steward.go) pass a template id
	// in in.Kind — backend.kind from the parsed template is the only
	// thing that resolves to a real family there.
	family := in.Kind
	if y.Backend.Kind != "" {
		family = y.Backend.Kind
	}
	kindCaps := caps.Agents[family]
	billing := modes.Billing(caps.BillingDeclarations[family])

	// Family-level mode floor (ADR-035 W2). An explicitly requested mode
	// (override or template driving_mode) that the engine family does not
	// declare in `supports` is rejected here — before the host-caps path,
	// which is permissive on an unprobed host and would otherwise let an
	// M1/M2 antigravity spawn coerce through and hang at launch. Only an
	// explicit request trips this; fallback-only specs still resolve
	// normally so a template can list M4 as a fallback for any engine.
	if fam, ok := agentfamilies.ByName(family); ok && len(fam.Supports) > 0 {
		requested := normalizeMode(in.Mode)
		if requested == "" {
			requested = normalizeMode(y.DrivingMode)
		}
		if requested != "" && !modeSupported(fam.Supports, requested) {
			return "", &ModeUnsupportedError{
				Family:    family,
				Mode:      requested,
				Supported: fam.Supports,
			}
		}
	}

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

	// Pull declarative billing/mode incompatibilities for this family
	// from the agent_families.yaml registry. Unknown family ⇒ empty
	// list ⇒ no billing rejections (consistent with the resolver's
	// permissive default for unknown billing).
	var incompat []modes.Incompat
	if fam, ok := agentfamilies.ByName(family); ok {
		for _, ic := range fam.Incompatibilities {
			incompat = append(incompat, modes.Incompat{
				Mode:    ic.Mode,
				Billing: modes.Billing(ic.Billing),
				Reason:  ic.Reason,
			})
		}
	}

	res, err := modes.Resolve(modes.Input{
		AgentKind:         family,
		Requested:         y.DrivingMode,
		FallbackModes:     y.FallbackModes,
		Override:          in.Mode,
		Billing:           billing,
		HostInstalled:     installed,
		HostSupports:      supports,
		Incompatibilities: incompat,
	})
	if err != nil {
		return "", fmt.Errorf("mode resolution failed: %w", err)
	}
	return res.Mode, nil
}
