package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ADR-046 / WS4 — apply_project_create.go unit + integration tests. The
// governed `project.create` kind is the steward's path to create a project:
// the change_spec carries the full inline spec; approval materializes the
// project via the same createProjectCore the REST path uses.

func TestProjectCreate_RegisteredAtInit(t *testing.T) {
	pk, ok := LookupProposeKind("project.create")
	if !ok {
		t.Fatal("project.create not registered at init()")
	}
	if pk.Validate == nil || pk.DryRun == nil || pk.Apply == nil || pk.Rollback == nil {
		t.Errorf("missing functions: validate=%v dry=%v apply=%v rollback=%v",
			pk.Validate != nil, pk.DryRun != nil, pk.Apply != nil, pk.Rollback != nil)
	}
}

func TestProjectCreate_Validate(t *testing.T) {
	pk, _ := LookupProposeKind("project.create")
	cases := []struct {
		name   string
		spec   string
		wantOK bool
		wantIn string
	}{
		{"happy", `{"name":"my project"}`, true, ""},
		{"missing change_spec", ``, false, "change_spec required"},
		{"missing name", `{"config_yaml":"phases: [x]\n"}`, false, "name required"},
		{"bad json", `{`, false, "change_spec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pk.Validate(context.Background(), nil, nil, json.RawMessage(tc.spec))
			if tc.wantOK {
				if err != nil {
					t.Errorf("validate: %v; want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want error; got nil")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("err %q should contain %q", err.Error(), tc.wantIn)
			}
		})
	}
}

func TestProjectCreate_DryRun_PreviewNoRow(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	pk, _ := LookupProposeKind("project.create")
	spec, _ := json.Marshal(map[string]any{
		"name":                  "preview-only",
		"kind":                  "goal",
		"config_yaml":           inlineSpecYAML,
		"on_create_template_id": "agents.steward.code-migration",
	})
	raw, err := pk.DryRun(context.Background(), srv, nil, spec)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	var preview map[string]any
	_ = json.Unmarshal(raw, &preview)
	if preview["name"] != "preview-only" {
		t.Errorf("preview.name = %v; want preview-only", preview["name"])
	}
	if preview["has_config_yaml"] != true {
		t.Errorf("preview.has_config_yaml = %v; want true", preview["has_config_yaml"])
	}
	phases, _ := preview["phases"].([]any)
	if len(phases) != 2 {
		t.Errorf("preview.phases = %v; want 2 (alpha, beta)", preview["phases"])
	}
	// Dry run mints NO row — the team's project list stays empty.
	rr := rcDo(t, srv, tok, http.MethodGet, "/v1/teams/"+team+"/projects", nil)
	var list []projectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &list)
	if len(list) != 0 {
		t.Fatalf("dry run created %d projects; want 0", len(list))
	}
}

func TestProjectCreate_Apply_MaterializesAllPhases(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	pk, _ := LookupProposeKind("project.create")
	spec, _ := json.Marshal(map[string]any{
		"name":                  "materialized",
		"kind":                  "goal",
		"goal":                  "do the work",
		"config_yaml":           inlineSpecYAML,
		"on_create_template_id": "agents.steward.code-migration",
	})
	ac := ProposeApplyContext{
		AttentionID: "att-pc-1", Team: team,
		AssignedTier: GovTierPrincipal, DeciderHandle: "@principal", Via: "propose",
	}
	executedRaw, err := pk.Apply(context.Background(), srv, ac, nil, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(executedRaw, &executed)
	if executed["kind"] != "project_create" {
		t.Errorf("executed.kind = %v; want project_create", executed["kind"])
	}
	projectID, _ := executed["project_id"].(string)
	if projectID == "" {
		t.Fatal("executed missing project_id")
	}

	// Early-bind: BOTH phases hydrate at create (the same invariant the REST
	// path tests pin), proving Apply runs the shared materialization path.
	if got := listCriteria(t, srv, team, tok, projectID, "alpha"); len(got) != 2 {
		t.Fatalf("alpha criteria=%d want 2", len(got))
	}
	if got := listCriteria(t, srv, team, tok, projectID, "beta"); len(got) != 1 {
		t.Fatalf("beta criteria=%d want 1 (future phase hydrates at create)", len(got))
	}
	if got := listDeliverables(t, srv, team, tok, projectID, "beta"); len(got) != 1 {
		t.Fatalf("beta deliverables=%d want 1", len(got))
	}

	// Steward bound but NOT spawned: the column carries the binding; read
	// reports steward_started=false (no live steward).
	rr := rcDo(t, srv, tok, http.MethodGet, "/v1/teams/"+team+"/projects/"+projectID, nil)
	var p projectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.OnCreateTemplateID != "agents.steward.code-migration" {
		t.Errorf("on_create_template_id = %q; want agents.steward.code-migration", p.OnCreateTemplateID)
	}
	if p.StewardStarted {
		t.Error("steward_started = true; want false (create binds, does not spawn)")
	}
	if p.Goal != "do the work" {
		t.Errorf("goal = %q; want 'do the work'", p.Goal)
	}

	// Audit row carries the propose lineage.
	var meta string
	if err := srv.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'project.create.proposed' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, projectID).Scan(&meta); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{`"via":"propose"`, `"propose_id":"att-pc-1"`, `"by_tier":"principal"`} {
		if !strings.Contains(meta, want) {
			t.Errorf("audit meta missing %s: %q", want, meta)
		}
	}
}

func TestProjectCreate_Apply_RejectsBadSpec(t *testing.T) {
	srv, _, team, _ := newProjectTemplateTestServer(t)
	pk, _ := LookupProposeKind("project.create")
	// A typed-parameter spec with a required param left unset → createProjectCore
	// rejects it; Apply surfaces that as an error (the approval does NOT
	// silently create a broken project).
	const reqParamSpec = `phases: [only]
parameters:
  must_set:
    type: string
    required: true
phase_specs:
  only:
    criteria:
      - id: c
        kind: text
        body: {text: x}
`
	spec, _ := json.Marshal(map[string]any{
		"name": "bad", "kind": "goal", "config_yaml": reqParamSpec,
	})
	ac := ProposeApplyContext{AttentionID: "att-pc-bad", Team: team, Via: "propose"}
	if _, err := pk.Apply(context.Background(), srv, ac, nil, spec); err == nil {
		t.Fatal("Apply with a missing required parameter should error; got nil")
	}
}

func TestProjectCreate_Apply_MissingTeam(t *testing.T) {
	srv, _, _, _ := newProjectTemplateTestServer(t)
	pk, _ := LookupProposeKind("project.create")
	spec, _ := json.Marshal(map[string]any{"name": "x"})
	if _, err := pk.Apply(context.Background(), srv, ProposeApplyContext{}, nil, spec); err == nil {
		t.Fatal("Apply with empty team should error; got nil")
	}
}

func TestProjectCreate_Rollback_ArchivesProject(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	pk, _ := LookupProposeKind("project.create")
	spec, _ := json.Marshal(map[string]any{
		"name": "to-rollback", "kind": "goal", "config_yaml": inlineSpecYAML,
	})
	ac := ProposeApplyContext{AttentionID: "att-pc-rb", Team: team, Via: "propose"}
	executedRaw, err := pk.Apply(context.Background(), srv, ac, nil, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	rbRaw, err := pk.Rollback(context.Background(), srv, ac, nil, executedRaw)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	var rb map[string]any
	_ = json.Unmarshal(rbRaw, &rb)
	if rb["archived"] != true {
		t.Errorf("rollback archived = %v; want true", rb["archived"])
	}

	var executed map[string]any
	_ = json.Unmarshal(executedRaw, &executed)
	projectID := executed["project_id"].(string)
	rr := rcDo(t, srv, tok, http.MethodGet, "/v1/teams/"+team+"/projects/"+projectID, nil)
	var p projectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.Status != "archived" {
		t.Errorf("project status = %q after rollback; want archived", p.Status)
	}
}
