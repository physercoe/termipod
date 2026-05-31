package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// provisionTeam POSTs /v1/admin/teams as the operator and returns the
// decoded response.
func provisionTeam(t *testing.T, s *Server, operatorTok, teamID, name string) provisionTeamOut {
	t.Helper()
	st, body := doReq(t, s, operatorTok, http.MethodPost, "/v1/admin/teams",
		map[string]any{"team_id": teamID, "name": name, "handle": "director"})
	if st != http.StatusCreated {
		t.Fatalf("provision %s: status=%d body=%s", teamID, st, body)
	}
	var out provisionTeamOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode provision response: %v", err)
	}
	return out
}

// TestProvisionTeam_OnboardingContract is the W3 acceptance test: an
// operator provisions a team, and the returned owner token can reach
// only its own team (W1) and not the fleet (W2).
func TestProvisionTeam_OnboardingContract(t *testing.T) {
	s, operatorTok := newA2ATestServer(t) // Init token is an operator

	out := provisionTeam(t, s, operatorTok, "acme", "Acme Corp")
	if out.TeamID != "acme" || out.Name != "Acme Corp" || out.OwnerToken == "" {
		t.Fatalf("unexpected provision response: %+v", out)
	}

	// The team row exists and the owner token is scoped to it.
	var kind, scope string
	if err := s.db.QueryRow(
		`SELECT kind, scope_json FROM auth_tokens WHERE id = ?`, out.OwnerTokenID).
		Scan(&kind, &scope); err != nil {
		t.Fatalf("query owner token: %v", err)
	}
	if kind != "owner" {
		t.Errorf("provisioned token kind = %q, want owner", kind)
	}

	ownerTok := out.OwnerToken

	// W1: the new owner reaches its own team.
	if st, body := doReq(t, s, ownerTok, http.MethodGet, "/v1/teams/acme/projects", nil); st != http.StatusOK {
		t.Errorf("new owner at own team: status=%d body=%s, want 200", st, body)
	}
	// W1: the new owner cannot reach another team (default).
	if st, _ := doReq(t, s, ownerTok, http.MethodGet, "/v1/teams/default/projects", nil); st != http.StatusForbidden {
		t.Errorf("new owner at default team: status=%d, want 403", st)
	}
	// W2: the new owner cannot reach the fleet.
	if st, _ := doReq(t, s, ownerTok, http.MethodGet, "/v1/admin/hosts", nil); st != http.StatusForbidden {
		t.Errorf("new owner at /v1/admin/hosts: status=%d, want 403", st)
	}
	// W3: the new owner cannot provision sibling teams (requireOperator).
	if st, _ := doReq(t, s, ownerTok, http.MethodPost, "/v1/admin/teams",
		map[string]any{"team_id": "sibling"}); st != http.StatusForbidden {
		t.Errorf("new owner provisioning a sibling: status=%d, want 403", st)
	}
}

func TestProvisionTeam_DuplicateReturns409(t *testing.T) {
	s, operatorTok := newA2ATestServer(t)
	_ = provisionTeam(t, s, operatorTok, "dup", "")
	// `default` already exists from Init; re-provisioning any live team 409s.
	for _, id := range []string{"dup", "default"} {
		if st, body := doReq(t, s, operatorTok, http.MethodPost, "/v1/admin/teams",
			map[string]any{"team_id": id}); st != http.StatusConflict {
			t.Errorf("re-provision %s: status=%d body=%s, want 409", id, st, body)
		}
	}
}

func TestProvisionTeam_InvalidIDReturns400(t *testing.T) {
	s, operatorTok := newA2ATestServer(t)
	for _, bad := range []string{"", "Has Space", "UPPER", "trailing-", "-leading", "a/b"} {
		if st, body := doReq(t, s, operatorTok, http.MethodPost, "/v1/admin/teams",
			map[string]any{"team_id": bad}); st != http.StatusBadRequest {
			t.Errorf("provision %q: status=%d body=%s, want 400", bad, st, body)
		}
	}
}

func TestProvisionTeam_RequiresOperator(t *testing.T) {
	s, _ := newA2ATestServer(t)
	ownerTok := mintTeamToken(t, s, "owner", defaultTeamID)
	if st, body := doReq(t, s, ownerTok, http.MethodPost, "/v1/admin/teams",
		map[string]any{"team_id": "nope"}); st != http.StatusForbidden {
		t.Errorf("owner provisioning a team: status=%d body=%s, want 403", st, body)
	}
}

func TestAdminListTeams(t *testing.T) {
	s, operatorTok := newA2ATestServer(t)
	_ = provisionTeam(t, s, operatorTok, "team-x", "X")

	st, body := doReq(t, s, operatorTok, http.MethodGet, "/v1/admin/teams", nil)
	if st != http.StatusOK {
		t.Fatalf("list teams: status=%d body=%s", st, body)
	}
	var teams []teamOut
	if err := json.Unmarshal(body, &teams); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := map[string]bool{}
	for _, tm := range teams {
		seen[tm.ID] = true
	}
	if !seen["default"] || !seen["team-x"] {
		t.Errorf("expected default + team-x in %+v", teams)
	}
}
