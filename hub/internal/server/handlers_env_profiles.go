package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
)

// handlers_env_profiles.go — team-scoped environment profiles (plan
// docs/plans/env-profiles-and-session-teleport.md, wedge E1). A profile bundles
// {setup_script + plain env_vars + secret_refs + network_policy} that a spawn
// attaches so an agent starts in a prepared environment. Metadata only
// (blueprint §4): env_vars + setup_script are hub-visible; secret_refs are
// *references* into the team's zero-knowledge vault (never values). Exposed over
// REST here; the shared store methods below back both REST and (future) MCP.
// Table: env_profiles.
//
// E1 scope: the entity + CRUD. secret_refs and network_policy are stored and
// round-tripped but not yet consumed — secret host-key envelopes land in E3,
// egress enforcement in E4. Spawn consumption (env_profile_id → materialized
// spec fields + host-runner merge/exec) is the E1b slice.

// envVarNameRe is the portable POSIX environment-variable name: a letter or
// underscore followed by letters/digits/underscores. We reject anything else at
// the boundary so a malformed key can't reach a shell export downstream.
var envVarNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// netPolicyModes is the closed set of network-policy modes. Declarative in E1
// (stored + returned); enforcement lands in E4 with the egress-proxy work.
var netPolicyModes = map[string]bool{"open": true, "allowlist": true, "offline": true}

// secretRef is a pointer into the team vault — {key, vault_item}, never a value.
type secretRef struct {
	Key       string `json:"key"`
	VaultItem string `json:"vault_item"`
}

// networkPolicy is declarative in E1: {mode, allowlist}. The allowlist is only
// meaningful when mode == "allowlist".
type networkPolicy struct {
	Mode      string   `json:"mode"`
	Allowlist []string `json:"allowlist,omitempty"`
}

// envProfileBody is the mutable projection — the fields a create/update sets.
// Embedded into envProfileOut so the wire shape is flat.
type envProfileBody struct {
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	SetupScript        string            `json:"setup_script"`
	SetupFailurePolicy string            `json:"setup_failure_policy"` // fail|continue
	EnvVars            map[string]string `json:"env_vars"`
	SecretRefs         []secretRef       `json:"secret_refs"`
	NetworkPolicy      networkPolicy     `json:"network_policy"`
}

type envProfileOut struct {
	ID     string `json:"id"`
	TeamID string `json:"team_id"`
	envProfileBody
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ---- validation / normalization -------------------------------------------

func normalizeFailurePolicy(p string) string {
	if p == "continue" {
		return "continue"
	}
	return "fail" // default fail-closed — a broken env must not silently run
}

func normalizeNetworkPolicy(np networkPolicy) networkPolicy {
	if !netPolicyModes[np.Mode] {
		np.Mode = "open"
	}
	if np.Allowlist == nil {
		np.Allowlist = []string{}
	}
	return np
}

// validateEnvProfile checks the boundary invariants a create/update must hold.
// Returns a client-safe message on the first violation, "" when clean.
func validateEnvProfile(b *envProfileBody) string {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return "name required"
	}
	if b.EnvVars == nil {
		b.EnvVars = map[string]string{}
	}
	for k := range b.EnvVars {
		if !envVarNameRe.MatchString(k) {
			return "invalid env var name: " + k
		}
	}
	if b.SecretRefs == nil {
		b.SecretRefs = []secretRef{}
	}
	for _, sr := range b.SecretRefs {
		if !envVarNameRe.MatchString(sr.Key) {
			return "invalid secret ref key: " + sr.Key
		}
		if strings.TrimSpace(sr.VaultItem) == "" {
			return "secret ref missing vault_item: " + sr.Key
		}
	}
	b.SetupFailurePolicy = normalizeFailurePolicy(b.SetupFailurePolicy)
	// An unknown non-empty mode is a caller error, not something to
	// normalize: normalizeNetworkPolicy maps it to "open" — the most
	// permissive mode — so a typo'd "allowlist" would silently store a
	// fail-OPEN policy that E4's enforcement would then honor. Reject at
	// the boundary; empty still defaults to "open" (declared default).
	if b.NetworkPolicy.Mode != "" && !netPolicyModes[b.NetworkPolicy.Mode] {
		return "invalid network policy mode: " + b.NetworkPolicy.Mode
	}
	b.NetworkPolicy = normalizeNetworkPolicy(b.NetworkPolicy)
	return ""
}

// ---- JSON column helpers ---------------------------------------------------

func envVarsJSON(m map[string]string) string {
	if m == nil {
		m = map[string]string{}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func parseEnvVars(s string) map[string]string {
	out := map[string]string{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	if out == nil {
		out = map[string]string{}
	}
	return out
}

func secretRefsJSON(rs []secretRef) string {
	if rs == nil {
		rs = []secretRef{}
	}
	b, _ := json.Marshal(rs)
	return string(b)
}

func parseSecretRefs(s string) []secretRef {
	out := []secretRef{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	if out == nil {
		out = []secretRef{}
	}
	return out
}

func netPolicyJSON(np networkPolicy) string {
	np = normalizeNetworkPolicy(np)
	b, _ := json.Marshal(np)
	return string(b)
}

func parseNetPolicy(s string) networkPolicy {
	var np networkPolicy
	if s != "" {
		_ = json.Unmarshal([]byte(s), &np)
	}
	return normalizeNetworkPolicy(np)
}

// ---- shared store methods (used by REST + future MCP) ----------------------

const envProfileCols = `id, team_id, name, description, setup_script,
	setup_failure_policy, env_vars_json, secret_refs_json, network_policy_json,
	created_at, updated_at`

func scanEnvProfile(row interface{ Scan(...any) error }) (envProfileOut, error) {
	var p envProfileOut
	var envVars, secretRefs, netPolicy string
	err := row.Scan(&p.ID, &p.TeamID, &p.Name, &p.Description, &p.SetupScript,
		&p.SetupFailurePolicy, &envVars, &secretRefs, &netPolicy,
		&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	p.EnvVars = parseEnvVars(envVars)
	p.SecretRefs = parseSecretRefs(secretRefs)
	p.NetworkPolicy = parseNetPolicy(netPolicy)
	return p, nil
}

func (s *Server) createEnvProfile(ctx context.Context, team string, b envProfileBody) (envProfileOut, error) {
	id := NewID()
	now := NowUTC()
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO env_profiles (`+envProfileCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		id, team, b.Name, b.Description, b.SetupScript, normalizeFailurePolicy(b.SetupFailurePolicy),
		envVarsJSON(b.EnvVars), secretRefsJSON(b.SecretRefs), netPolicyJSON(b.NetworkPolicy),
		now, now)
	if err != nil {
		return envProfileOut{}, err
	}
	return s.getEnvProfileByID(ctx, team, id)
}

func (s *Server) getEnvProfileByID(ctx context.Context, team, id string) (envProfileOut, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+envProfileCols+` FROM env_profiles WHERE team_id = ? AND id = ?`, team, id)
	return scanEnvProfile(row)
}

func (s *Server) listEnvProfiles(ctx context.Context, team string) ([]envProfileOut, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+envProfileCols+` FROM env_profiles WHERE team_id = ? ORDER BY name ASC`, team)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []envProfileOut{}
	for rows.Next() {
		p, err := scanEnvProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// patchEnvProfile applies a partial JSON patch onto the existing row: keys
// present override, absent keys keep their stored value (plain struct-decode
// onto the loaded body, same semantics as patchReference).
func (s *Server) patchEnvProfile(ctx context.Context, team, id string, patch json.RawMessage) (envProfileOut, string, error) {
	cur, err := s.getEnvProfileByID(ctx, team, id)
	if err != nil {
		return envProfileOut{}, "", err
	}
	// encoding/json MERGES into a non-nil map rather than replacing it, so a
	// patch that sends env_vars would be unable to drop a key. PATCH semantics
	// are replace-a-provided-field-wholesale: if the patch carries env_vars,
	// null the stored map first so the decode replaces it cleanly. (Slices —
	// secret_refs, allowlist — are already replaced, not merged, by json.)
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(patch, &probe); err != nil {
		return envProfileOut{}, "invalid json", nil //nolint:nilerr — client error, not a DB error
	}
	if _, ok := probe["env_vars"]; ok {
		cur.EnvVars = nil
	}
	if err := json.Unmarshal(patch, &cur.envProfileBody); err != nil {
		return envProfileOut{}, "invalid json", nil //nolint:nilerr — client error, not a DB error
	}
	b := cur.envProfileBody
	if msg := validateEnvProfile(&b); msg != "" {
		return envProfileOut{}, msg, nil
	}
	_, err = s.writeDB.ExecContext(ctx, `
		UPDATE env_profiles SET
			name = ?, description = ?, setup_script = ?, setup_failure_policy = ?,
			env_vars_json = ?, secret_refs_json = ?, network_policy_json = ?, updated_at = ?
		WHERE team_id = ? AND id = ?`,
		b.Name, b.Description, b.SetupScript, b.SetupFailurePolicy,
		envVarsJSON(b.EnvVars), secretRefsJSON(b.SecretRefs), netPolicyJSON(b.NetworkPolicy),
		NowUTC(), team, id)
	if err != nil {
		return envProfileOut{}, "", err
	}
	out, err := s.getEnvProfileByID(ctx, team, id)
	return out, "", err
}

func (s *Server) deleteEnvProfile(ctx context.Context, team, id string) (bool, error) {
	res, err := s.writeDB.ExecContext(ctx, `DELETE FROM env_profiles WHERE team_id = ? AND id = ?`, team, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ---- REST handlers ---------------------------------------------------------

func (s *Server) handleListEnvProfiles(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	out, err := s.listEnvProfiles(r.Context(), team)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateEnvProfile(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var b envProfileBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := validateEnvProfile(&b); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	out, err := s.createEnvProfile(r.Context(), team, b)
	if err != nil {
		s.writeDBErr(w, err) // UNIQUE(team,name) → 409 via mapDBError
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleGetEnvProfile(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "profile")
	out, err := s.getEnvProfileByID(r.Context(), team, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "env profile not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateEnvProfile(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "profile")
	patch, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInlineDocBytes))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	out, msg, err := s.patchEnvProfile(r.Context(), team, id, patch)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "env profile not found")
		return
	}
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteEnvProfile(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "profile")
	ok, err := s.deleteEnvProfile(r.Context(), team, id)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "env profile not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}
