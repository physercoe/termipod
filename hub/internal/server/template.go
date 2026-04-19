package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"time"
)

// Template rendering for spawn specs. We interpolate a small, fixed set of
// variables so spawn YAML can reference state that only exists at spawn
// time — most notably the parent agent's journal and the principal handle
// requesting the spawn. Rendering is pre-YAML: the result is then persisted
// and handed to host-agent verbatim.
//
// Variable shape: {{name}}. We intentionally do not support expressions,
// filters, or control flow — spawn specs should stay declarative. If a var
// is not defined for a given spawn (e.g. {{journal}} with no parent), it
// expands to an empty string rather than leaving the placeholder behind;
// that way the rendered YAML is always parseable even when data is missing.
//
// Security: only data already visible to the caller feeds these variables.
// {{journal}} reads from the parent agent's markdown file — the parent is
// owned by the same team as the spawner, so no cross-team leak. We never
// expand tokens, passwords, or anything from flutter_secure_storage; those
// don't live on the hub.

var tmplVarRe = regexp.MustCompile(`\{\{\s*([a-z_][a-z0-9_]*)\s*\}\}`)

// renderSpawnSpec expands {{var}} placeholders in spec using values derived
// from the Server state + the authenticated request. The returned string is
// ready to persist as spawn_spec_yaml.
//
// Context is used for DB reads (journal lookup); on any sub-lookup failure we
// substitute the empty string and keep going — partial data beats a spawn
// hard-failing because the parent happens to have no journal yet.
func (s *Server) renderSpawnSpec(ctx context.Context, team string, in spawnIn, principal string) (string, error) {
	if !strings.Contains(in.SpawnSpec, "{{") {
		return in.SpawnSpec, nil
	}
	vars := map[string]string{
		"handle":    in.ChildHandle,
		"kind":      in.Kind,
		"team":      team,
		"now":       time.Now().UTC().Format(time.RFC3339),
		"principal": firstNonEmpty(principal, "@principal"),
	}
	if in.ParentID != "" {
		parentHandle, journal, err := s.lookupParentContext(ctx, team, in.ParentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		vars["parent_handle"] = parentHandle
		vars["journal"] = journal
	}
	return tmplVarRe.ReplaceAllStringFunc(in.SpawnSpec, func(match string) string {
		m := tmplVarRe.FindStringSubmatch(match)
		if m == nil {
			return match
		}
		return vars[m[1]] // missing → ""
	}), nil
}

// lookupParentContext returns (handle, journalContent). Missing journal file
// is reported as (handle, "", nil) rather than an error — a parent without
// notes is common early in a task.
func (s *Server) lookupParentContext(ctx context.Context, team, parentID string) (string, string, error) {
	var handle string
	err := s.db.QueryRowContext(ctx,
		`SELECT handle FROM agents WHERE team_id = ? AND id = ?`, team, parentID).Scan(&handle)
	if err != nil {
		return "", "", err
	}
	path, err := s.journalPath(team, handle)
	if err != nil {
		return handle, "", nil
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return handle, "", nil
	}
	if err != nil {
		return handle, "", err
	}
	return handle, string(body), nil
}

// principalFromScope pulls the role out of an auth token's scope JSON.
// Tokens used by the hub CLI are tagged role:"principal"; host-agent tokens
// are role:"host". We surface the role as `@role` so templates that drop
// {{principal}} into an assignee list render a valid handle.
func principalFromScope(scopeJSON string) string {
	if scopeJSON == "" {
		return "@principal"
	}
	var s struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal([]byte(scopeJSON), &s); err != nil || s.Role == "" {
		return "@principal"
	}
	return "@" + s.Role
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
