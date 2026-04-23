package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestListProjects_IsTemplateFilter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	token, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := NowUTC()
	if _, err := s.db.Exec(
		`INSERT INTO projects (id, team_id, name, created_at, kind, is_template)
		 VALUES (?, ?, ?, ?, 'goal', 0)`,
		"proj-concrete", defaultTeamID, "concrete", now); err != nil {
		t.Fatalf("seed concrete: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO projects (id, team_id, name, created_at, kind, is_template)
		 VALUES (?, ?, ?, ?, 'goal', 1)`,
		"proj-template", defaultTeamID, "ablation-sweep", now); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	cases := []struct {
		query    string
		wantIDs  []string
		wantCode int
	}{
		{"", []string{"proj-concrete", "proj-template"}, http.StatusOK},
		{"?is_template=true", []string{"proj-template"}, http.StatusOK},
		{"?is_template=1", []string{"proj-template"}, http.StatusOK},
		{"?is_template=false", []string{"proj-concrete"}, http.StatusOK},
		{"?is_template=0", []string{"proj-concrete"}, http.StatusOK},
		{"?is_template=nope", nil, http.StatusBadRequest},
	}

	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/teams/"+defaultTeamID+"/projects"+c.query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		s.router.ServeHTTP(rr, req)
		if rr.Code != c.wantCode {
			t.Errorf("query=%q: status=%d want=%d body=%s",
				c.query, rr.Code, c.wantCode, rr.Body.String())
			continue
		}
		if c.wantCode != http.StatusOK {
			continue
		}
		var out []projectOut
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Errorf("query=%q: decode: %v", c.query, err)
			continue
		}
		got := make(map[string]bool, len(out))
		for _, p := range out {
			got[p.ID] = true
		}
		for _, want := range c.wantIDs {
			if !got[want] {
				t.Errorf("query=%q: missing id %s in result", c.query, want)
			}
		}
		if len(out) != len(c.wantIDs) {
			t.Errorf("query=%q: got %d rows want %d", c.query, len(out), len(c.wantIDs))
		}
	}
}
