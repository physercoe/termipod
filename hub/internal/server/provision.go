package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/termipod/hub/internal/auth"
)

// teamIDRe constrains a team id to a conservative URL-safe slug: it
// appears in /v1/teams/{team}/… request paths and (W5) on disk as a
// workdir segment, so it must avoid path-separators, whitespace, and
// case ambiguity. DNS-label shape — lowercase alphanumerics and
// internal hyphens only, no leading/trailing hyphen, 1–64 chars.
var teamIDRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// ErrTeamExists is returned by ProvisionTeam when the team id is taken —
// provisioning must not silently re-mint an owner for a live team. The
// REST layer maps this to 409.
var ErrTeamExists = errors.New("team already exists")

// ErrInvalidTeamID is returned by ProvisionTeam when the team id is not
// a valid slug. The REST layer maps this to 400.
var ErrInvalidTeamID = errors.New("invalid team id")

// ProvisionTeam creates a new team and mints its first `owner` token —
// the team's principal/director (ADR-037 D3). It is the shared core
// behind the operator-gated POST /v1/admin/teams and the `hub-server
// team create` CLI, so a team is onboarded as exactly (team_id,
// owner_token). The owner token is scoped to the new team; with the W1
// path-team gate it can reach only that team, and being a per-team
// owner (not operator) it cannot reach /v1/admin/* (W2).
//
// Returns the one-time plaintext owner token plus its id and creation
// timestamp. The team id must be a slug (teamIDRe) and must not already
// exist. Templates are NOT seeded per-team: built-ins are global
// (ADR-037 D5), so a fresh team can spawn from them immediately; W4 adds
// per-team overrides.
func ProvisionTeam(ctx context.Context, db *sql.DB, teamID, name, handle string) (ownerToken, tokenID, createdAt string, err error) {
	if !teamIDRe.MatchString(teamID) {
		return "", "", "", fmt.Errorf("%w %q: must match %s", ErrInvalidTeamID, teamID, teamIDRe.String())
	}
	if name == "" {
		name = teamID
	}
	// Existence check: provisioning is create-only, never re-provision.
	var existing string
	switch err := db.QueryRowContext(ctx,
		`SELECT id FROM teams WHERE id = ?`, teamID).Scan(&existing); {
	case err == nil:
		return "", "", "", ErrTeamExists
	case !errors.Is(err, sql.ErrNoRows):
		return "", "", "", err
	}

	now := NowUTC()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		teamID, name, now); err != nil {
		return "", "", "", fmt.Errorf("insert team: %w", err)
	}

	plain := auth.NewToken()
	tokenID = NewID()
	scopeMap := map[string]any{"team": teamID, "role": "principal"}
	if handle != "" {
		scopeMap["handle"] = handle
	}
	scope, _ := json.Marshal(scopeMap)
	if err := auth.InsertToken(ctx, db, "owner", string(scope), plain, tokenID, now); err != nil {
		return "", "", "", fmt.Errorf("insert owner token: %w", err)
	}
	return plain, tokenID, now, nil
}
