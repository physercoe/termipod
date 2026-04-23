package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// readAttentionStatus returns (status, approveCount) for an attention row.
// Handy probe for asserting that a sub-quorum approve only grew decisions
// without flipping the row to resolved.
func readAttentionStatus(t *testing.T, s *Server, id string) (string, int) {
	t.Helper()
	var status, decisions string
	if err := s.db.QueryRow(
		`SELECT status, decisions_json FROM attention_items WHERE id = ?`, id,
	).Scan(&status, &decisions); err != nil {
		t.Fatalf("read: %v", err)
	}
	var list []map[string]any
	_ = json.Unmarshal([]byte(decisions), &list)
	n := 0
	for _, d := range list {
		if s, _ := d["decision"].(string); s == "approve" {
			n++
		}
	}
	return status, n
}

func TestDecideAttention_DefaultQuorumOneApproveResolves(t *testing.T) {
	s, token := newA2ATestServer(t)
	// No policy file; QuorumFor("") returns 1.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := insertAttention(t, s, "" /*tier*/, now, []string{"@steward"})

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+id+"/decide",
		map[string]any{"decision": "approve", "by": "@reviewer-a"})
	if status != 200 {
		t.Fatalf("decide = %d body=%s", status, string(body))
	}
	var out attentionDecideOut
	_ = json.Unmarshal(body, &out)
	if !out.Resolved {
		t.Errorf("Resolved = false, want true at default quorum=1")
	}
	st, approves := readAttentionStatus(t, s, id)
	if st != "resolved" {
		t.Errorf("db status = %q, want resolved", st)
	}
	if approves != 1 {
		t.Errorf("approves = %d, want 1", approves)
	}
}

func TestDecideAttention_PolicyQuorumHoldsUntilThreshold(t *testing.T) {
	s, token := newA2ATestServer(t)
	writePolicy(t, s, s.cfg.DataRoot, `
tiers:
  spawn: moderate
quorum:
  moderate: 2
`)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := insertAttention(t, s, TierModerate, now, []string{"@a", "@b"})

	// First approve — under quorum, must stay open.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+id+"/decide",
		map[string]any{"decision": "approve", "by": "@a"})
	if status != 200 {
		t.Fatalf("first decide = %d body=%s", status, string(body))
	}
	var out attentionDecideOut
	_ = json.Unmarshal(body, &out)
	if out.Resolved {
		t.Errorf("Resolved = true after 1/2 approves, want false")
	}
	st, n := readAttentionStatus(t, s, id)
	if st != "open" {
		t.Errorf("after first approve: status = %q, want open", st)
	}
	if n != 1 {
		t.Errorf("approves after first = %d, want 1", n)
	}

	// Second approve — meets quorum, must resolve.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+id+"/decide",
		map[string]any{"decision": "approve", "by": "@b"})
	if status != 200 {
		t.Fatalf("second decide = %d body=%s", status, string(body))
	}
	_ = json.Unmarshal(body, &out)
	if !out.Resolved {
		t.Errorf("Resolved = false after 2/2 approves, want true")
	}
	st, n = readAttentionStatus(t, s, id)
	if st != "resolved" {
		t.Errorf("after second approve: status = %q, want resolved", st)
	}
	if n != 2 {
		t.Errorf("approves after second = %d, want 2", n)
	}
}

func TestDecideAttention_RejectVetoesRegardlessOfQuorum(t *testing.T) {
	s, token := newA2ATestServer(t)
	writePolicy(t, s, s.cfg.DataRoot, `
tiers:
  spawn: critical
quorum:
  critical: 3
`)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := insertAttention(t, s, TierCritical, now, []string{"@a"})

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+id+"/decide",
		map[string]any{"decision": "reject", "by": "@a", "reason": "blocker"})
	if status != 200 {
		t.Fatalf("reject = %d body=%s", status, string(body))
	}
	var out attentionDecideOut
	_ = json.Unmarshal(body, &out)
	if !out.Resolved {
		t.Errorf("reject under quorum=3 must still resolve; Resolved=false")
	}
	st, _ := readAttentionStatus(t, s, id)
	if st != "resolved" {
		t.Errorf("reject: db status = %q, want resolved", st)
	}
}
