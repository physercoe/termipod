package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
)

// restoreDefaultRegistry resets the package-level agentfamilies default
// after a test that may have swapped it through Server.New. Tests run
// in the same process and share package state; without this, leftover
// custom families from one test bleed into the next test's view of
// agentfamilies.ByName.
func restoreDefaultRegistry(t *testing.T) {
	t.Helper()
	agentfamilies.SetDefault(agentfamilies.New(""))
}

// listAgentFamiliesPayload is the JSON shape returned by GET /agent-families.
// Mirrors the handler output so the test can assert on typed fields without
// hand-rolling map[string]any indexing.
type listAgentFamiliesPayload struct {
	Families []struct {
		Family   string   `json:"family"`
		Bin      string   `json:"bin"`
		Supports []string `json:"supports"`
		Source   string   `json:"source"`
	} `json:"families"`
}

// TestAgentFamilies_PutAddsCustom_ListShowsIt is the load-bearing test
// for the wedge: PUT a brand-new family, then GET the list and confirm
// the new family is there with source=custom. Also verifies the file
// landed under <DataRoot>/agent_families/.
func TestAgentFamilies_PutAddsCustom_ListShowsIt(t *testing.T) {
	c := newE2E(t)
	defer restoreDefaultRegistry(t)

	url := c.srv.URL + "/v1/teams/" + c.teamID + "/agent-families/kimi"
	body := []byte("family: kimi\nbin: kimi\nversion_flag: --version\nsupports: [M2, M4]\n")

	status, raw := rawCallRaw(t, c.token, url, "PUT", "application/yaml", body)
	if status != 201 {
		t.Fatalf("PUT = %d body=%s", status, raw)
	}

	path := filepath.Join(c.dataRoot, "agent_families", "kimi.yaml")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("disk content mismatch:\n got: %q\nwant: %q", got, body)
	}

	listURL := c.srv.URL + "/v1/teams/" + c.teamID + "/agent-families"
	status, raw = rawCallRaw(t, c.token, listURL, "GET", "", nil)
	if status != 200 {
		t.Fatalf("GET list = %d body=%s", status, raw)
	}
	var list listAgentFamiliesPayload
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found bool
	for _, f := range list.Families {
		if f.Family == "kimi" {
			found = true
			if f.Source != "custom" {
				t.Errorf("kimi source = %q; want custom", f.Source)
			}
			if f.Bin != "kimi" {
				t.Errorf("kimi bin = %q; want kimi", f.Bin)
			}
		}
	}
	if !found {
		t.Fatalf("kimi missing from list response: %s", raw)
	}
}

// TestAgentFamilies_PutOverridesEmbedded_FlipsSourceTag verifies an
// override of an embedded family appears with source=override and the
// overlay's fields shadow the embedded ones.
func TestAgentFamilies_PutOverridesEmbedded_FlipsSourceTag(t *testing.T) {
	c := newE2E(t)
	defer restoreDefaultRegistry(t)

	url := c.srv.URL + "/v1/teams/" + c.teamID + "/agent-families/claude-code"
	body := []byte("family: claude-code\nbin: claude-fork\nversion_flag: --version\nsupports: [M4]\n")
	status, raw := rawCallRaw(t, c.token, url, "PUT", "application/yaml", body)
	if status != 201 {
		t.Fatalf("PUT = %d body=%s", status, raw)
	}

	getURL := url
	status, raw = rawCallRaw(t, c.token, getURL, "GET", "", nil)
	if status != 200 {
		t.Fatalf("GET = %d body=%s", status, raw)
	}
	var got struct {
		Family   string   `json:"family"`
		Bin      string   `json:"bin"`
		Supports []string `json:"supports"`
		Source   string   `json:"source"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Source != "override" {
		t.Errorf("source = %q; want override", got.Source)
	}
	if got.Bin != "claude-fork" {
		t.Errorf("bin = %q; want claude-fork (overlay should shadow embedded)", got.Bin)
	}
	if len(got.Supports) != 1 || got.Supports[0] != "M4" {
		t.Errorf("supports = %v; want [M4]", got.Supports)
	}
}

// TestAgentFamilies_PutInvalidYAML_Rejected pins down the strict-parse
// gate: typo in a key name (e.g. "verison_flag") must 400 rather than
// silently dropping the field.
func TestAgentFamilies_PutInvalidYAML_Rejected(t *testing.T) {
	c := newE2E(t)
	defer restoreDefaultRegistry(t)

	cases := []struct {
		name string
		body string
	}{
		{"unknown-key", "family: kimi\nbin: kimi\nverison_flag: --version\nsupports: [M2]\n"},
		{"missing-bin", "family: kimi\nsupports: [M2]\n"},
		{"empty-supports", "family: kimi\nbin: kimi\nsupports: []\n"},
		{"unknown-mode", "family: kimi\nbin: kimi\nsupports: [M9]\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := c.srv.URL + "/v1/teams/" + c.teamID + "/agent-families/kimi"
			status, raw := rawCallRaw(t, c.token, url, "PUT", "application/yaml", []byte(tc.body))
			if status != 400 {
				t.Fatalf("status = %d (want 400) body=%s", status, raw)
			}
		})
	}
}

// TestAgentFamilies_PutBodyURLDriftRejected ensures the URL family path
// component must agree with the body's family field — drift produces
// ghost files where /kimi.yaml has family:something-else inside.
func TestAgentFamilies_PutBodyURLDriftRejected(t *testing.T) {
	c := newE2E(t)
	defer restoreDefaultRegistry(t)

	url := c.srv.URL + "/v1/teams/" + c.teamID + "/agent-families/kimi"
	body := []byte("family: not-kimi\nbin: kimi\nsupports: [M2]\n")
	status, raw := rawCallRaw(t, c.token, url, "PUT", "application/yaml", body)
	if status != 400 {
		t.Fatalf("status = %d (want 400) body=%s", status, raw)
	}
	if !strings.Contains(string(raw), "must match URL path") {
		t.Errorf("error did not surface drift hint: %s", raw)
	}
}

// TestAgentFamilies_DeleteCustom_RemovesFile + reload-from-list test.
func TestAgentFamilies_DeleteCustom_RemovesFile(t *testing.T) {
	c := newE2E(t)
	defer restoreDefaultRegistry(t)

	url := c.srv.URL + "/v1/teams/" + c.teamID + "/agent-families/kimi"
	body := []byte("family: kimi\nbin: kimi\nsupports: [M2]\n")
	if status, raw := rawCallRaw(t, c.token, url, "PUT", "application/yaml", body); status != 201 {
		t.Fatalf("seed PUT = %d body=%s", status, raw)
	}

	status, raw := rawCallRaw(t, c.token, url, "DELETE", "", nil)
	if status != 204 {
		t.Fatalf("DELETE = %d body=%s", status, raw)
	}
	if _, err := os.Stat(filepath.Join(c.dataRoot, "agent_families", "kimi.yaml")); !os.IsNotExist(err) {
		t.Errorf("file still present after DELETE: %v", err)
	}

	// Second DELETE — file gone, family unknown → 404.
	status, _ = rawCallRaw(t, c.token, url, "DELETE", "", nil)
	if status != 404 {
		t.Errorf("second DELETE = %d; want 404", status)
	}
}

// TestAgentFamilies_DeleteEmbedded_Conflicts: deleting an embedded-only
// family is rejected with 409. There's no override file to remove, and
// disabling embedded entries via DELETE would silently shrink the
// closed set everywhere.
func TestAgentFamilies_DeleteEmbedded_Conflicts(t *testing.T) {
	c := newE2E(t)
	defer restoreDefaultRegistry(t)

	url := c.srv.URL + "/v1/teams/" + c.teamID + "/agent-families/claude-code"
	status, raw := rawCallRaw(t, c.token, url, "DELETE", "", nil)
	if status != 409 {
		t.Fatalf("DELETE = %d body=%s; want 409", status, raw)
	}
}

// TestAgentFamilies_HotReload_PutThenSpawnSeesIt is the integration
// shape that proves the overall wedge: after PUT lands a family,
// agentfamilies.ByName immediately resolves it (cache invalidated).
// This is the path that lets a Kimi spawn succeed on the next request
// rather than after a hub restart.
func TestAgentFamilies_HotReload_PutThenSpawnSeesIt(t *testing.T) {
	c := newE2E(t)
	defer restoreDefaultRegistry(t)

	url := c.srv.URL + "/v1/teams/" + c.teamID + "/agent-families/kimi"
	body := []byte("family: kimi\nbin: kimi\nsupports: [M2, M4]\n" +
		"incompatibilities:\n  - mode: M2\n    billing: subscription\n    reason: \"vendor\"\n")
	if status, raw := rawCallRaw(t, c.token, url, "PUT", "application/yaml", body); status != 201 {
		t.Fatalf("PUT = %d body=%s", status, raw)
	}

	fam, ok := c.s.agentFamilies.ByName("kimi")
	if !ok {
		t.Fatal("kimi not visible to ByName immediately after PUT")
	}
	if fam.Bin != "kimi" {
		t.Errorf("bin = %q; want kimi", fam.Bin)
	}
	if len(fam.Incompatibilities) != 1 {
		t.Fatalf("incompatibilities len = %d; want 1", len(fam.Incompatibilities))
	}

	// Audit row should reflect the create.
	rows, err := c.s.listAuditEvents(context.Background(), c.teamID, "", "", 50)
	if err != nil {
		t.Fatalf("listAudit: %v", err)
	}
	var sawCreate bool
	for _, e := range rows {
		if e.Action == "agent_family.created" && e.TargetID == "kimi" {
			sawCreate = true
		}
	}
	if !sawCreate {
		t.Error("expected agent_family.created audit row")
	}
}

// TestAgentFamilies_RejectsBadName covers the URL component guard. We
// don't want callers escaping the overlay directory via path traversal,
// so any name not matching the regex 400s before the file system sees it.
func TestAgentFamilies_RejectsBadName(t *testing.T) {
	c := newE2E(t)
	defer restoreDefaultRegistry(t)

	for _, bad := range []string{"../etc", "Bad", "foo/bar", "..", "."} {
		url := c.srv.URL + "/v1/teams/" + c.teamID + "/agent-families/" + bad
		body := []byte("family: x\nbin: x\nsupports: [M2]\n")
		status, _ := rawCallRaw(t, c.token, url, "PUT", "application/yaml", body)
		if status == 200 || status == 201 {
			t.Errorf("PUT %q accepted (status=%d); should be rejected", bad, status)
		}
	}
}
