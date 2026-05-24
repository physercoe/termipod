package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-030 W8 — apply_template_install.go unit tests.

func TestTemplateInstall_RegisteredAtInit(t *testing.T) {
	pk, ok := LookupProposeKind("template.install")
	if !ok {
		t.Fatal("template.install not registered at init()")
	}
	if pk.Validate == nil || pk.DryRun == nil || pk.Apply == nil {
		t.Errorf("missing functions")
	}
}

func TestTemplateInstall_Validate(t *testing.T) {
	pk, _ := LookupProposeKind("template.install")
	cases := []struct {
		name   string
		spec   string
		wantOK bool
		wantIn string
	}{
		{"happy", `{"category":"prompt","name":"foo.v1","blob_sha256":"abc"}`, true, ""},
		{"empty change_spec", ``, false, "change_spec required"},
		{"missing category", `{"name":"foo","blob_sha256":"abc"}`, false, "category"},
		{"missing name", `{"category":"prompt","blob_sha256":"abc"}`, false, "name"},
		{"missing blob_sha256", `{"category":"prompt","name":"foo"}`, false, "blob_sha256"},
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

// seedBlob writes a fake template body to the server's blob store
// and returns its sha256. Mirrors the on-disk shape mcpTemplatesPropose
// writes.
func seedBlob(t *testing.T, s *Server, body []byte) string {
	t.Helper()
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	path := s.blobPath(sha)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir blob dir: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write blob: %v", err)
	}
	return sha
}

func TestTemplateInstall_DryRun_PresentBlob(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("template.install")
	body := []byte("# example template\nkind: claude-code\n")
	sha := seedBlob(t, s, body)

	spec, _ := json.Marshal(map[string]any{
		"category": "prompt", "name": "example.v1", "blob_sha256": sha,
	})
	raw, err := pk.DryRun(context.Background(), s, nil, spec)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	var preview map[string]any
	_ = json.Unmarshal(raw, &preview)
	if preview["blob_present"] != true {
		t.Errorf("blob_present = %v; want true", preview["blob_present"])
	}
	if int(preview["blob_bytes"].(float64)) != len(body) {
		t.Errorf("blob_bytes = %v; want %d", preview["blob_bytes"], len(body))
	}
}

func TestTemplateInstall_DryRun_MissingBlob(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("template.install")
	spec, _ := json.Marshal(map[string]any{
		"category": "prompt", "name": "ghost.v1", "blob_sha256": "0xnope",
	})
	raw, err := pk.DryRun(context.Background(), s, nil, spec)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	var preview map[string]any
	_ = json.Unmarshal(raw, &preview)
	if preview["blob_present"] != false {
		t.Errorf("blob_present = %v; want false", preview["blob_present"])
	}
}

func TestTemplateInstall_Apply_HappyPath(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("template.install")
	body := []byte("kind: claude-code\nbackend:\n  cmd: echo via-propose\n")
	sha := seedBlob(t, s, body)
	spec, _ := json.Marshal(map[string]any{
		"category": "prompt", "name": "via-propose.v1", "blob_sha256": sha,
		"rationale":   "needed for X",
		"proposed_by": "a-worker",
	})
	ac := ProposeApplyContext{
		AttentionID: "att-tmpl-1", Team: defaultTeamID,
		AssignedTier: GovTierPrincipal, DeciderHandle: "@principal", Via: "propose",
	}
	executedRaw, err := pk.Apply(context.Background(), s, ac, nil, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(executedRaw, &executed)
	if executed["kind"] != "template_install" {
		t.Errorf("executed.kind = %v; want template_install", executed["kind"])
	}
	if int(executed["bytes"].(float64)) != len(body) {
		t.Errorf("executed.bytes = %v; want %d", executed["bytes"], len(body))
	}
	// File written on disk.
	if dst, ok := executed["path"].(string); ok {
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read installed file: %v", err)
		}
		if string(got) != string(body) {
			t.Errorf("installed file content mismatch")
		}
	} else {
		t.Error("executed missing path")
	}
	// Audit row.
	var meta string
	_ = s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'template.install' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, "prompt/via-propose.v1").Scan(&meta)
	for _, want := range []string{
		`"via":"propose"`,
		`"propose_id":"att-tmpl-1"`,
		`"category":"prompt"`,
		`"rationale":"needed for X"`,
		`"proposed_by":"a-worker"`,
	} {
		if !strings.Contains(meta, want) {
			t.Errorf("audit meta missing %s: %q", want, meta)
		}
	}
}

func TestTemplateInstall_Apply_AliasLegacyViaTag(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("template.install")
	body := []byte("kind: legacy-shape\n")
	sha := seedBlob(t, s, body)
	spec, _ := json.Marshal(map[string]any{
		"category": "agent", "name": "legacy-only.v1", "blob_sha256": sha,
	})
	ac := ProposeApplyContext{
		AttentionID: "att-tmpl-legacy", Team: defaultTeamID, Via: "alias_legacy",
	}
	if _, err := pk.Apply(context.Background(), s, ac, nil, spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var meta string
	_ = s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'template.install' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, "agent/legacy-only.v1").Scan(&meta)
	if !strings.Contains(meta, `"via":"alias_legacy"`) {
		t.Errorf("audit meta should carry via=alias_legacy; got %q", meta)
	}
}
