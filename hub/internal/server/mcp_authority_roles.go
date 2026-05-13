// mcp_authority_roles.go — operation-scope role manifest loader (ADR-016).
//
// The single MVP governance line at the hub MCP boundary: which
// `hub://*` MCP tools each role (steward, worker) may invoke. The
// manifest lives at `hub/config/roles.yaml` (embedded), with optional
// per-deployment override at `<DataRoot>/roles.yaml`.
//
// Role is set at spawn time (handlers_agents.go) by mapping the
// agent_kind through `kind_to_role`. The middleware in
// dispatchTool reads scope.Role and consults Allows() before
// dispatching the tool.
//
// Engine-internal subagents (claude-code Task, codex app-server
// children, gemini-cli subagent invocations) inherit the parent's
// role by construction and are NOT separately gated. ADR-016 D5.

package server

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed roles.yaml
var rolesEmbedFS []byte

// rolesFile is the on-disk YAML schema. Field tags match the YAML 1:1.
type rolesFile struct {
	KindToRole struct {
		Default        string            `yaml:"default"`
		PrefixSteward  string            `yaml:"prefix_steward"`
		Exact          map[string]string `yaml:"exact"`
	} `yaml:"kind_to_role"`
	Roles map[string]roleSpec `yaml:"roles"`
}

type roleSpec struct {
	AllowAll bool     `yaml:"allow_all"`
	Allow    []string `yaml:"allow"`
}

// Roles is the loaded, query-ready manifest. Construct via loadRoles.
// Pattern matching is deterministic: exact > prefix.* > *.suffix > "*".
type Roles struct {
	defaultRole   string
	prefixSteward string
	kindExact     map[string]string
	rolePolicies  map[string]rolePolicy
}

type rolePolicy struct {
	allowAll bool
	exact    map[string]struct{}
	prefixes []string // pattern was "foo.*"; stored as "foo."
	suffixes []string // pattern was "*.bar"; stored as ".bar"
	starAll  bool     // pattern was bare "*"
}

// rolesSingleton holds the active manifest. Init populates it; tests
// and the (future) Invalidate path replace it under rolesMu.
var (
	rolesMu        sync.RWMutex
	rolesSingleton *Roles
)

// initRoles loads the embedded manifest and merges any overlay at
// dataRoot/roles.yaml. Called from server New(). Overlay wins on
// per-key collision (we replace whole role policies, not merge them).
func initRoles(dataRoot string) error {
	r, err := loadRoles(dataRoot)
	if err != nil {
		return fmt.Errorf("load roles manifest: %w", err)
	}
	rolesMu.Lock()
	rolesSingleton = r
	rolesMu.Unlock()
	return nil
}

// activeRoles returns the loaded manifest. Returns nil if initRoles
// hasn't run — callers must handle that (the middleware fail-closes).
func activeRoles() *Roles {
	rolesMu.RLock()
	defer rolesMu.RUnlock()
	return rolesSingleton
}

// InvalidateRoles re-reads the manifest. Hook for future hot-reload
// (overlay-edit MCP tools). Currently unused at runtime but keeps the
// extension contract honest. Exported in case a future test wants it.
func InvalidateRoles(dataRoot string) error {
	return initRoles(dataRoot)
}

func loadRoles(dataRoot string) (*Roles, error) {
	// Start from the embedded default.
	var base rolesFile
	if err := yaml.Unmarshal(rolesEmbedFS, &base); err != nil {
		return nil, fmt.Errorf("parse embedded roles.yaml: %w", err)
	}

	// Merge overlay if present.
	if dataRoot != "" {
		overlayPath := filepath.Join(dataRoot, "roles.yaml")
		if b, err := os.ReadFile(overlayPath); err == nil {
			var overlay rolesFile
			if err := yaml.Unmarshal(b, &overlay); err != nil {
				return nil, fmt.Errorf("parse overlay %s: %w", overlayPath, err)
			}
			mergeRolesFile(&base, &overlay)
		}
	}

	return compileRoles(&base)
}

// mergeRolesFile applies overlay onto base. Overlay's kind_to_role and
// per-role policies replace base's whole-keys (no field-level merge).
func mergeRolesFile(base, overlay *rolesFile) {
	if overlay.KindToRole.Default != "" {
		base.KindToRole.Default = overlay.KindToRole.Default
	}
	if overlay.KindToRole.PrefixSteward != "" {
		base.KindToRole.PrefixSteward = overlay.KindToRole.PrefixSteward
	}
	if len(overlay.KindToRole.Exact) > 0 {
		if base.KindToRole.Exact == nil {
			base.KindToRole.Exact = map[string]string{}
		}
		for k, v := range overlay.KindToRole.Exact {
			base.KindToRole.Exact[k] = v
		}
	}
	if len(overlay.Roles) > 0 {
		if base.Roles == nil {
			base.Roles = map[string]roleSpec{}
		}
		for k, v := range overlay.Roles {
			base.Roles[k] = v
		}
	}
}

func compileRoles(f *rolesFile) (*Roles, error) {
	r := &Roles{
		defaultRole:   f.KindToRole.Default,
		prefixSteward: f.KindToRole.PrefixSteward,
		kindExact:     map[string]string{},
		rolePolicies:  map[string]rolePolicy{},
	}
	if r.defaultRole == "" {
		r.defaultRole = "worker"
	}
	for k, v := range f.KindToRole.Exact {
		r.kindExact[k] = v
	}
	for name, spec := range f.Roles {
		p := rolePolicy{
			allowAll: spec.AllowAll,
			exact:    map[string]struct{}{},
		}
		for _, pat := range spec.Allow {
			switch {
			case pat == "*":
				p.starAll = true
			case strings.HasSuffix(pat, ".*"):
				p.prefixes = append(p.prefixes, strings.TrimSuffix(pat, "*"))
			case strings.HasPrefix(pat, "*."):
				p.suffixes = append(p.suffixes, strings.TrimPrefix(pat, "*"))
			default:
				p.exact[pat] = struct{}{}
			}
		}
		r.rolePolicies[name] = p
	}
	return r, nil
}

// RoleFor maps an agent_kind to a role per the manifest. Exact
// entries override the prefix rule; everything else falls to default.
// Stable for the lifetime of one Roles snapshot.
func (r *Roles) RoleFor(kind string) string {
	if r == nil {
		return "worker"
	}
	if v, ok := r.kindExact[kind]; ok && v != "" {
		return v
	}
	if r.prefixSteward != "" && strings.HasPrefix(kind, r.prefixSteward) {
		return "steward"
	}
	return r.defaultRole
}

// RoleForSpec resolves a role using BOTH the agent_kind AND the
// spawn_spec_yaml. The spec is the load-bearing input because mobile
// historically conflated engine kind (claude-code/codex/gemini-cli)
// with persona kind (steward.research.v1/worker.coder.v1), sending
// the engine name as `kind` even for stewards — which made every
// steward spawned from mobile land with role=worker.
//
// Resolution order:
//  1. Kind-based match (RoleFor) — if the caller already passed a
//     recognised persona kind, trust it.
//  2. Spec-based fallback — parse the `default_role:` line from the
//     embedded YAML. Every steward template declares
//     `default_role: team.*` (coordinator or concierge); every worker
//     declares `worker.*`. So the spec carries the persona signal
//     even when the kind string doesn't.
//  3. Default — `r.defaultRole`.
//
// Empty spec falls straight through to RoleFor, preserving the
// existing path for callers that don't have a spec in hand.
func (r *Roles) RoleForSpec(kind, spawnSpecYaml string) string {
	if r == nil {
		return "worker"
	}
	// Try kind-based first so admin-supplied exact overrides still win.
	if v, ok := r.kindExact[kind]; ok && v != "" {
		return v
	}
	if r.prefixSteward != "" && strings.HasPrefix(kind, r.prefixSteward) {
		return "steward"
	}
	// Spec-based fallback. Tiny line scan — no need for a full YAML
	// parser when we only care about a single top-level key.
	if dr := parseDefaultRole(spawnSpecYaml); dr != "" {
		if strings.HasPrefix(dr, "team.") {
			return "steward"
		}
		// `worker.*` / `writer.*` / anything else explicitly declared
		// stays in the worker bucket. We only escalate to steward on
		// the `team.*` signal; nothing else carries that authority.
		if strings.HasPrefix(dr, "worker.") || strings.HasPrefix(dr, "writer.") {
			return "worker"
		}
	}
	return r.defaultRole
}

// parseDefaultRole extracts the top-level `default_role:` value from
// a spawn-spec YAML body. Returns "" when the key is absent or the
// body is empty. We avoid a full YAML decode because every spawn
// touches this path and the field is single-line, top-level, and
// trivially unambiguous.
//
// Tolerates: surrounding whitespace, single/double-quoted values,
// trailing comments after `#`. Stops scanning at the first non-
// indented occurrence so a nested `default_role:` under e.g.
// `mcp.something:` doesn't accidentally trump the top-level value.
func parseDefaultRole(yamlBody string) string {
	if yamlBody == "" {
		return ""
	}
	for _, line := range strings.Split(yamlBody, "\n") {
		// Top-level keys have no leading whitespace.
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		const key = "default_role:"
		if !strings.HasPrefix(line, key) {
			continue
		}
		val := strings.TrimSpace(line[len(key):])
		if i := strings.IndexByte(val, '#'); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		val = strings.Trim(val, `"'`)
		return val
	}
	return ""
}

// Allows reports whether the given role may invoke the given tool name
// per the manifest. Unknown role → deny. allow_all roles always
// allow. Patterns: exact, prefix ("foo.*"), suffix ("*.bar"), or "*".
func (r *Roles) Allows(role, tool string) bool {
	if r == nil {
		return false
	}
	p, ok := r.rolePolicies[role]
	if !ok {
		return false
	}
	if p.allowAll || p.starAll {
		return true
	}
	if _, ok := p.exact[tool]; ok {
		return true
	}
	for _, pre := range p.prefixes {
		if strings.HasPrefix(tool, pre) {
			return true
		}
	}
	for _, suf := range p.suffixes {
		if strings.HasSuffix(tool, suf) {
			return true
		}
	}
	return false
}

// authorizeMCPCall is the middleware called from dispatchTool. Returns
// nil to allow, or a JSON-RPC error to deny.
//
// Bypass cases (return nil):
//   - agentID == "" (principal/bootstrap token; not a per-agent call)
//   - role lookup yields a role known to the manifest with allow_all set
//   - tool is in the role's allow list
//
// Deny case: tool not in role's allow list. Code -32601 is "method
// not found" per JSON-RPC; we reuse it because the agent learns the
// tool effectively does not exist for them, which is the right
// model: the catalog filtering is the security boundary.
func (s *Server) authorizeMCPCall(ctx interface{}, agentID, scopeRole, tool string) *jrpcError {
	_ = ctx // not used today; reserved for future per-call audit.
	if agentID == "" {
		return nil // principal token bypass.
	}
	role := s.resolveAgentRole(agentID, scopeRole)
	r := activeRoles()
	if r == nil {
		// Manifest not loaded — fail-closed, but log the unexpected
		// state. In practice initRoles is called from server New(),
		// so this branch is reachable only in tests that bypass New.
		return &jrpcError{Code: -32601, Message: "operation-scope manifest not loaded"}
	}
	if r.Allows(role, tool) {
		return nil
	}
	return &jrpcError{Code: -32601, Message: "tool not permitted for role: " + role}
}

// authorizeA2ATarget enforces ADR-016 D4: workers may invoke
// a2a.invoke only against their parent steward. Stewards are
// unrestricted (return nil immediately). Caller threads the parsed
// `args` map from the tools/call params; we extract `handle` and
// resolve target_agent_id + caller's parent_agent_id from the DB.
//
// Returns nil on allow; jrpcError with message "a2a target not
// permitted: workers may only invoke parent steward" on deny.
//
// Edge cases:
//   - args missing or handle empty → defer to the tool implementation
//     (it surfaces a clearer "handle is required" error).
//   - target handle resolves to multiple agents (shouldn't happen with
//     UNIQUE(team_id, handle) on active rows, migration 0023) — we
//     pick the first match.
//   - caller has no parent_agent_id (top-level worker, unusual) →
//     deny conservatively. Workers should always have a parent.
func (s *Server) authorizeA2ATarget(agentID, scopeRole, team string, args map[string]any) *jrpcError {
	if scopeRole == "steward" {
		return nil
	}
	// Extract target handle. Skip the check if the tool will reject
	// the call anyway for missing args — let the implementation's
	// error surface speak for itself.
	handle, _ := args["handle"].(string)
	if handle == "" {
		return nil
	}

	// Resolve target agent_id by handle within the team.
	var targetID string
	_ = s.db.QueryRow(
		`SELECT id FROM agents WHERE team_id = ? AND handle = ? LIMIT 1`,
		team, handle).Scan(&targetID)
	if targetID == "" {
		// Target doesn't exist in this team's agent table; the tool
		// implementation will surface a "no A2A agent found" error
		// from the directory lookup. Don't double-error here.
		return nil
	}

	// Resolve caller's parent_agent_id (most recent spawn record;
	// handle re-spawn by ordering on spawned_at).
	var parentID sql.NullString
	_ = s.db.QueryRow(
		`SELECT parent_agent_id FROM agent_spawns
		 WHERE child_agent_id = ?
		 ORDER BY spawned_at DESC
		 LIMIT 1`,
		agentID).Scan(&parentID)

	if !parentID.Valid || parentID.String == "" || parentID.String != targetID {
		return &jrpcError{
			Code:    -32601,
			Message: "a2a target not permitted: workers may only invoke parent steward",
		}
	}
	return nil
}

// resolveAgentRole prefers the role stamped on scope_json at spawn
// time. For legacy tokens (role="agent" or empty) it falls back to
// looking up agent_kind from the agents table and re-deriving via
// Roles.RoleFor. The fallback path is the migration ramp — once all
// active tokens have been minted post-W1, this path is exercised
// only by hand-crafted test tokens.
func (s *Server) resolveAgentRole(agentID, scopeRole string) string {
	if scopeRole == "steward" || scopeRole == "worker" {
		return scopeRole
	}
	if s == nil || s.db == nil {
		return "worker"
	}
	var kind string
	_ = s.db.QueryRow(`SELECT kind FROM agents WHERE id = ?`, agentID).Scan(&kind)
	r := activeRoles()
	if r == nil {
		return "worker"
	}
	return r.RoleFor(kind)
}
