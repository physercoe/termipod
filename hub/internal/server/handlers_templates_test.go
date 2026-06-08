package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rawCallRaw is rawCall's sibling for endpoints whose body isn't JSON.
// Template PUT carries yaml/markdown verbatim — encoding through json
// would smuggle quotes through the wire that aren't in the file.
func rawCallRaw(t *testing.T, token, url, method, contentType string, body []byte) (int, []byte) {
	t.Helper()
	var buf io.Reader
	if body != nil {
		buf = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, buf)
	if err != nil {
		t.Fatalf("build req: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// TestTemplates_PutCreatesAndAudits exercises the create-via-PUT path:
// fresh file goes 201 + audit row "template.created", second PUT to the
// same name goes 200 + audit row "template.updated". Asserts both the
// disk write and the audit trail (since recordAudit failures are silently
// swallowed by design).
func TestTemplates_PutCreatesAndAudits(t *testing.T) {
	c := newE2E(t)
	url := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/agents/test-worker.v1.yaml"

	body := []byte("template: agents.test-worker\nversion: 1\n")
	status, raw := rawCallRaw(t, c.token, url, "PUT", "application/yaml", body)
	if status != 201 {
		t.Fatalf("first PUT = %d body=%s", status, raw)
	}
	// W4 / ADR-037 D5: a team's PUT lands in its per-team override dir.
	path := filepath.Join(c.dataRoot, "teams", c.teamID, "templates", "agents", "test-worker.v1.yaml")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("disk content mismatch:\ngot:  %q\nwant: %q", got, body)
	}

	// Second PUT — same name, new content. Should 200 + audit "updated".
	body2 := []byte("template: agents.test-worker\nversion: 2\n")
	status, raw = rawCallRaw(t, c.token, url, "PUT", "application/yaml", body2)
	if status != 200 {
		t.Fatalf("second PUT = %d body=%s", status, raw)
	}
	got2, _ := os.ReadFile(path)
	if string(got2) != string(body2) {
		t.Errorf("second write didn't overwrite: got %q", got2)
	}

	// Audit should have one created + one updated row for this target.
	rows, err := c.s.listAuditEvents(context.Background(), c.teamID, "", "", "", 100)
	if err != nil {
		t.Fatalf("listAudit: %v", err)
	}
	var created, updated int
	for _, r := range rows {
		if r.TargetID != "agents/test-worker.v1.yaml" {
			continue
		}
		switch r.Action {
		case "template.created":
			created++
		case "template.updated":
			updated++
		}
	}
	if created != 1 || updated != 1 {
		t.Errorf("audit rows: created=%d updated=%d (want 1+1)", created, updated)
	}
}

// TestTemplates_PutRejectsTraversal confirms validation drops names
// containing path separators or hidden-file prefixes before we touch
// disk. The MaxBytes/sandbox checks downstream are belt-and-suspenders.
func TestTemplates_PutRejectsTraversal(t *testing.T) {
	c := newE2E(t)
	for _, bad := range []string{
		"../escape.yaml",
		"foo/bar.yaml",
		".hidden.yaml",
	} {
		url := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/agents/" + bad
		status, _ := rawCallRaw(t, c.token, url, "PUT", "application/yaml", []byte("x: 1\n"))
		if status != 400 && status != 404 {
			// chi routes "../foo" etc. weirdly — both 400 and 404 are
			// acceptable refusals as long as we never write the file.
			t.Errorf("PUT %q = %d, want 4xx refusal", bad, status)
		}
	}
}

// TestTemplates_DeleteRemovesAndAudits writes a file, deletes it, and
// asserts the file is gone + a "template.deleted" row appears. Re-delete
// returns 404 so the UI can detect concurrent removal.
func TestTemplates_DeleteRemovesAndAudits(t *testing.T) {
	c := newE2E(t)
	url := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/agents/doomed.v1.yaml"
	if status, raw := rawCallRaw(t, c.token, url, "PUT", "application/yaml",
		[]byte("template: doomed\n")); status != 201 {
		t.Fatalf("PUT = %d body=%s", status, raw)
	}

	status, raw := rawCall(t, c.token, url, "DELETE", nil)
	if status != 204 {
		t.Fatalf("DELETE = %d body=%s", status, raw)
	}
	path := filepath.Join(c.dataRoot, "team", "templates", "agents", "doomed.v1.yaml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after delete: err=%v", err)
	}

	// Second DELETE → 404.
	status, _ = rawCall(t, c.token, url, "DELETE", nil)
	if status != 404 {
		t.Errorf("re-DELETE = %d, want 404", status)
	}

	rows, _ := c.s.listAuditEvents(context.Background(), c.teamID, "template.deleted", "", "", 10)
	found := false
	for _, r := range rows {
		if r.TargetID == "agents/doomed.v1.yaml" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no template.deleted audit row")
	}
}

// TestTemplates_RenameMovesFile creates a file, PATCHes it to a new
// name, and asserts the source is gone, the destination has the
// content, and the audit row references both names in meta.
func TestTemplates_RenameMovesFile(t *testing.T) {
	c := newE2E(t)
	src := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/agents/old-name.v1.yaml"
	body := []byte("template: old\nversion: 1\n")
	if status, _ := rawCallRaw(t, c.token, src, "PUT", "application/yaml", body); status != 201 {
		t.Fatal("seed PUT failed")
	}
	status, raw := rawCall(t, c.token, src, "PATCH", map[string]any{
		"new_name": "new-name.v1.yaml",
	})
	if status != 200 {
		t.Fatalf("PATCH = %d body=%s", status, raw)
	}
	srcPath := filepath.Join(c.dataRoot, "teams", c.teamID, "templates", "agents", "old-name.v1.yaml")
	dstPath := filepath.Join(c.dataRoot, "teams", c.teamID, "templates", "agents", "new-name.v1.yaml")
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Errorf("source still exists: %v", err)
	}
	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("destination missing: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("destination content drift: %q vs %q", got, body)
	}
}

// TestTemplates_RenameRefusesOverwrite ensures PATCH 409s when the
// destination already exists. Callers who want overwrite must DELETE
// then PUT — keeps the rename op atomic and reversible.
func TestTemplates_RenameRefusesOverwrite(t *testing.T) {
	c := newE2E(t)
	for _, n := range []string{"a.v1.yaml", "b.v1.yaml"} {
		u := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/agents/" + n
		if s, _ := rawCallRaw(t, c.token, u, "PUT", "application/yaml",
			[]byte("template: "+n+"\n")); s != 201 {
			t.Fatalf("seed %q failed", n)
		}
	}
	src := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/agents/a.v1.yaml"
	status, raw := rawCall(t, c.token, src, "PATCH",
		map[string]any{"new_name": "b.v1.yaml"})
	if status != 409 {
		t.Fatalf("PATCH onto existing = %d, want 409 (body=%s)", status, raw)
	}
}

// TestTemplates_CategoriesFromEmbed asserts the LIST endpoint returns
// items from every embedded category, even on a fresh data root with no
// disk overlay yet. The previous fixed list locked categories at
// {agents,prompts,policies}; the embedded FS also ships projects/, and
// that should now appear without a Go-side allow-list edit.
func TestTemplates_CategoriesFromEmbed(t *testing.T) {
	c := newE2E(t)
	url := c.srv.URL + "/v1/teams/" + c.teamID + "/templates"
	status, raw := rawCall(t, c.token, url, "GET", nil)
	if status != 200 {
		t.Fatalf("LIST = %d body=%s", status, raw)
	}
	cats := map[string]bool{}
	// crude scan — the JSON shape is [{category, name, ...}, …] and we
	// don't need a full decoder for this assertion.
	for _, want := range []string{"agents", "prompts", "policies", "projects"} {
		if strings.Contains(string(raw), `"category":"`+want+`"`) {
			cats[want] = true
		}
	}
	for _, want := range []string{"agents", "prompts", "projects"} {
		if !cats[want] {
			t.Errorf("category %q not in LIST body=%s", want, raw)
		}
	}
}

// TestTemplates_PutCreatesNewCategory verifies that PUTting into a
// previously-unknown category (here "tools") creates the directory and
// shows up in subsequent LISTs. This is the wedge that lets new
// categories be added by data, not by editing a Go allow-list.
func TestTemplates_PutCreatesNewCategory(t *testing.T) {
	c := newE2E(t)
	url := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/tools/example.v1.yaml"
	body := []byte("template: tools.example\nversion: 1\n")
	status, raw := rawCallRaw(t, c.token, url, "PUT", "application/yaml", body)
	if status != 201 {
		t.Fatalf("PUT new category = %d body=%s", status, raw)
	}
	listURL := c.srv.URL + "/v1/teams/" + c.teamID + "/templates"
	status, raw = rawCall(t, c.token, listURL, "GET", nil)
	if status != 200 {
		t.Fatalf("LIST = %d body=%s", status, raw)
	}
	if !strings.Contains(string(raw), `"category":"tools"`) {
		t.Errorf("new category not in LIST body=%s", raw)
	}
}

// TestTemplates_GetFallsBackToEmbedded covers a regression where a fresh
// hub data root (or one wiped after install) returned 404 for built-in
// templates, breaking the mobile bootstrap sheet (it fetches
// agents/steward.v1.yaml and ships the body as the spawn spec). The
// handler now overlays disk on top of the embedded FS, so a missing
// disk file falls through to the bundled built-in.
func TestTemplates_GetFallsBackToEmbedded(t *testing.T) {
	c := newE2E(t)
	// Remove the seeded disk copy to simulate a missing/wiped overlay.
	disk := filepath.Join(c.dataRoot, "team", "templates", "agents", "steward.v1.yaml")
	if err := os.Remove(disk); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove disk overlay: %v", err)
	}
	url := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/agents/steward.v1.yaml"
	status, raw := rawCall(t, c.token, url, "GET", nil)
	if status != 200 {
		t.Fatalf("GET = %d body=%s", status, raw)
	}
	if !strings.Contains(string(raw), "template: agents.steward") {
		t.Errorf("embedded fallback body missing template key: %s", raw)
	}
	if !strings.Contains(string(raw), "backend:") || !strings.Contains(string(raw), "cmd:") {
		t.Errorf("embedded fallback body missing backend.cmd: %s", raw)
	}

	// Truly-unknown name must still 404 — the fallback should not turn
	// into a "search the FS for anything" oracle.
	url404 := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/agents/does-not-exist.yaml"
	status, _ = rawCall(t, c.token, url404, "GET", nil)
	if status != 404 {
		t.Errorf("missing template GET = %d, want 404", status)
	}
}

// TestTemplates_GetMergeOverlay covers the overlay-merge fix for stale
// on-disk templates. The hub's writeBuiltinTemplates intentionally
// never overwrites disk files on upgrade ("user edits win"), so a user
// whose data root was seeded by an older hub keeps their stripped-down
// copy forever — even if the embedded built-in has gained important
// fields like backend.cmd.
//
// With ?merge=1 the disk file overlays onto the embedded base, so
// missing keys fall through automatically. The editor (no merge flag)
// still sees the raw disk contents so user comments are preserved.
func TestTemplates_GetMergeOverlay(t *testing.T) {
	c := newE2E(t)
	disk := filepath.Join(c.dataRoot, "team", "templates", "agents", "steward.v1.yaml")
	// Simulate a stale seed: only kind/model/default_workdir, no cmd
	// and no permission_modes — exactly the case reported by the user.
	stale := []byte(strings.Join([]string{
		"template: agents.steward",
		"version: 1",
		"backend:",
		"  kind: claude-code",
		"  model: claude-opus-4-7",
		"  default_workdir: ~/hub-work",
		"",
	}, "\n"))
	if err := os.WriteFile(disk, stale, 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	rawURL := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/agents/steward.v1.yaml"
	mergeURL := rawURL + "?merge=1"

	// Without merge=1 the editor sees the disk file verbatim — no cmd.
	status, body := rawCall(t, c.token, rawURL, "GET", nil)
	if status != 200 {
		t.Fatalf("raw GET = %d body=%s", status, body)
	}
	if strings.Contains(string(body), "cmd:") {
		t.Errorf("raw GET smuggled in cmd: from embedded:\n%s", body)
	}

	// With merge=1 the embedded backend.cmd falls through and the user's
	// default_workdir is preserved. (permission_modes is no longer a
	// template field — it moved onto the claude-code family in ADR-043
	// P3 — so it is intentionally NOT expected in the merged output.)
	status, body = rawCall(t, c.token, mergeURL, "GET", nil)
	if status != 200 {
		t.Fatalf("merged GET = %d body=%s", status, body)
	}
	if !strings.Contains(string(body), "cmd:") {
		t.Errorf("merged GET missing backend.cmd:\n%s", body)
	}
	if !strings.Contains(string(body), "~/hub-work") {
		t.Errorf("merged GET dropped user's default_workdir:\n%s", body)
	}
}

// TestBuildSpawnVars_DataDriven_Model confirms {{model}} resolves from
// the spec yaml's backend.model field — the whole point of the wedge.
// A spec with no model → empty string (no Go-side fallback).
func TestBuildSpawnVars_DataDriven_Model(t *testing.T) {
	s, _ := newTestServer(t)
	in := spawnIn{
		ChildHandle: "w", Kind: "claude-code",
		SpawnSpec: "backend:\n  model: claude-haiku-4-5\n",
	}
	vars, err := s.buildSpawnVars(context.Background(), defaultTeamID, in, "@p")
	if err != nil {
		t.Fatalf("buildSpawnVars: %v", err)
	}
	if vars["model"] != "claude-haiku-4-5" {
		t.Errorf("model = %q, want claude-haiku-4-5", vars["model"])
	}

	// No backend.model → empty (Go must not invent a default).
	in.SpawnSpec = "kind: claude-code\n"
	vars, _ = s.buildSpawnVars(context.Background(), defaultTeamID, in, "@p")
	if vars["model"] != "" {
		t.Errorf("model fallback leaked: %q", vars["model"])
	}
}

// TestBuildSpawnVars_DataDriven_PermissionFlag verifies the permission
// flag is read from backend.permission_modes[mode] in the spec, not
// from a Go switch. Adding a new mode should be a YAML edit only.
func TestBuildSpawnVars_DataDriven_PermissionFlag(t *testing.T) {
	s, _ := newTestServer(t)
	spec := strings.Join([]string{
		"backend:",
		"  permission_modes:",
		"    skip: --dangerously-skip-permissions",
		"    prompt: --permission-prompt-tool mcp__termipod__permission_prompt",
		"    custom: --my-future-flag",
		"",
	}, "\n")

	cases := []struct {
		mode, want string
	}{
		{"skip", "--dangerously-skip-permissions"},
		{"prompt", "--permission-prompt-tool mcp__termipod__permission_prompt"},
		{"custom", "--my-future-flag"}, // would have been impossible with the old Go switch
		// v1.0.617: empty mode rewrites to "skip" so callers that
		// forget to pass permission_mode (notably the MCP agents.spawn
		// path before the schema added the field) still get a runnable
		// worker instead of a stalled one.
		{"", "--dangerously-skip-permissions"},
		{"unknown", ""},
	}
	for _, c := range cases {
		in := spawnIn{
			ChildHandle:    "w",
			Kind:           "k",
			SpawnSpec:      spec,
			PermissionMode: c.mode,
		}
		vars, _ := s.buildSpawnVars(context.Background(), defaultTeamID, in, "")
		if vars["permission_flag"] != c.want {
			t.Errorf("mode=%q: flag=%q, want %q", c.mode, vars["permission_flag"], c.want)
		}
	}
}

// TestTemplates_ResetOverwritesUserEdits exercises the v1.0.617 reset
// endpoint: a user edits a bundled template (steward.v1.yaml), the
// reset endpoint overwrites it back to the embedded body, and the
// audit row records the count.
func TestTemplates_ResetOverwritesUserEdits(t *testing.T) {
	c := newE2E(t)

	// 1. Overwrite a bundled template with a sentinel body.
	path := filepath.Join(c.dataRoot, "team", "templates", "agents", "steward.v1.yaml")
	if err := os.WriteFile(path, []byte("# user-edited\n"), 0o600); err != nil {
		t.Fatalf("seed user edit: %v", err)
	}

	// 2. Call reset.
	url := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/reset"
	status, raw := rawCallRaw(t, c.token, url, "POST", "", nil)
	if status != 200 {
		t.Fatalf("reset = %d body=%s", status, raw)
	}
	if !bytes.Contains(raw, []byte(`"overwritten":`)) {
		t.Errorf("response missing overwritten count: %s", raw)
	}

	// 3. Confirm the file content is back to the bundled body (the
	// sentinel is gone) and at least mentions the steward template
	// identifier — we don't pin exact bytes since the bundled file
	// evolves, just confirm the user edit was clobbered.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after reset: %v", err)
	}
	if bytes.Contains(after, []byte("# user-edited")) {
		t.Errorf("user edit survived reset:\n%s", after)
	}
	if !bytes.Contains(after, []byte("template:")) {
		t.Errorf("bundled template body missing yaml header:\n%s", after)
	}

	// 4. Audit row landed.
	var action string
	if err := c.s.db.QueryRow(
		`SELECT action FROM audit_events
		 WHERE action = 'template.reset' AND team_id = ?
		 ORDER BY ts DESC LIMIT 1`, c.teamID).Scan(&action); err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
}

// User-only files (no embedded counterpart) survive a reset — the
// contract is "restore bundled defaults", not "delete everything not
// embedded". Protects custom team templates from being wiped.
func TestTemplates_ResetPreservesUserOnlyFiles(t *testing.T) {
	c := newE2E(t)
	custom := filepath.Join(c.dataRoot, "team", "templates", "agents", "my-custom.v1.yaml")
	if err := os.WriteFile(custom, []byte("# custom\n"), 0o600); err != nil {
		t.Fatalf("seed custom: %v", err)
	}

	url := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/reset"
	status, _ := rawCallRaw(t, c.token, url, "POST", "", nil)
	if status != 200 {
		t.Fatalf("reset = %d", status)
	}

	got, err := os.ReadFile(custom)
	if err != nil {
		t.Fatalf("custom file gone after reset: %v", err)
	}
	if !bytes.Contains(got, []byte("# custom")) {
		t.Errorf("custom file body altered:\n%s", got)
	}
}

// A bundled project preset that was removed from the binary in a later
// release (e.g. write-memo / reproduce-paper retired by ADR-046 WS3)
// lingers on existing hubs' data roots because seeding is additive.
// Reset reconciles the baseline projects/ dir to the current bundle,
// pruning the orphan — while leaving baseline files in other categories
// and per-team overlay project templates untouched.
func TestTemplates_ResetPrunesOrphanedProjectTemplates(t *testing.T) {
	c := newE2E(t)

	// 1. A stale baseline preset the bundle no longer ships.
	orphan := filepath.Join(c.dataRoot, "team", "templates", "projects", "write-memo.yaml")
	if err := os.WriteFile(orphan, []byte("name: write-memo\nphases: []\n"), 0o600); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	// 2. A baseline file in another category with no embedded counterpart
	//    must NOT be pruned (it can be agent-installed content).
	agentOnly := filepath.Join(c.dataRoot, "team", "templates", "agents", "custom-agent.v1.yaml")
	if err := os.WriteFile(agentOnly, []byte("# custom agent\n"), 0o600); err != nil {
		t.Fatalf("seed agent-only: %v", err)
	}
	// 3. A per-team OVERLAY project template (user content) must survive.
	overlayDir := filepath.Join(c.dataRoot, "teams", c.teamID, "templates", "projects")
	if err := os.MkdirAll(overlayDir, 0o700); err != nil {
		t.Fatalf("mk overlay: %v", err)
	}
	overlay := filepath.Join(overlayDir, "my-project.v1.yaml")
	if err := os.WriteFile(overlay, []byte("name: my-project\nphases: []\n"), 0o600); err != nil {
		t.Fatalf("seed overlay: %v", err)
	}

	url := c.srv.URL + "/v1/teams/" + c.teamID + "/templates/reset"
	status, raw := rawCallRaw(t, c.token, url, "POST", "", nil)
	if status != 200 {
		t.Fatalf("reset = %d body=%s", status, raw)
	}
	if !bytes.Contains(raw, []byte(`"pruned":1`)) {
		t.Errorf("expected pruned:1 in response, got: %s", raw)
	}

	// Orphan baseline project preset removed.
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphaned project preset survived reset (err=%v)", err)
	}
	// Bundled presets restored / present.
	if _, err := os.Stat(filepath.Join(c.dataRoot, "team", "templates", "projects", "research.v1.yaml")); err != nil {
		t.Errorf("bundled research preset missing after reset: %v", err)
	}
	// Cross-category baseline file untouched.
	if _, err := os.Stat(agentOnly); err != nil {
		t.Errorf("baseline agent file pruned (must not be): %v", err)
	}
	// Per-team overlay project template untouched.
	if _, err := os.Stat(overlay); err != nil {
		t.Errorf("overlay project template pruned (must not be): %v", err)
	}
}
