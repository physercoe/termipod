package envelope

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingWarner captures Warner invocations so tests can assert
// what audit rows would have been dispatched.
type recordingWarner struct {
	mu    sync.Mutex
	calls []warnerCall
}

type warnerCall struct {
	Action  string
	Summary string
	Meta    map[string]any
}

func (r *recordingWarner) fn() Warner {
	return func(action, summary string, meta map[string]any) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, warnerCall{
			Action: action, Summary: summary, Meta: meta,
		})
	}
}

func (r *recordingWarner) snapshot() []warnerCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]warnerCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func TestResolve_EmbeddedWhenNoOperatorOverride(t *testing.T) {
	// No path set → loader resolves to embedded. The embedded file
	// is byte-shipped in `hub.TemplatesFS` so this works in any CI
	// environment without an on-disk operator data root.
	r := &recordingWarner{}
	l := NewLoader(r.fn()).WithHubData(t.TempDir())
	tpl := l.Resolve()
	if tpl.Origin != OriginEmbedded {
		t.Errorf("origin = %q, want %q", tpl.Origin, OriginEmbedded)
	}
	if len(r.snapshot()) != 0 {
		t.Errorf("embedded fallback should not warn: %v", r.snapshot())
	}
}

func TestResolve_EmbeddedTemplateCompilesAndRenders(t *testing.T) {
	// Critical invariant: the embedded YAML must always parse +
	// validate + compile. CI runs this on every build; a malformed
	// `hub/templates/envelope/active.yaml` makes the loader panic
	// and CI catches it before the binary ships.
	l := NewLoader(nil).WithHubData(t.TempDir())
	tpl := l.Resolve()
	got := tpl.Render(Message{
		Kind:      "directive",
		FromRole:  "principal",
		Transport: "session",
		Text:      "embedded smoke",
	})
	for _, want := range []string{
		"[directive from the principal]",
		"embedded smoke",
		"Reply in this chat",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q: %q", want, got)
		}
	}
}

func TestResolve_OperatorOverrideTakesPrecedence(t *testing.T) {
	// Write a custom YAML at the operator path and assert the
	// loader prefers it over embedded. The custom Frame uses a
	// distinct marker so the assertion can't false-positive on
	// embedded content.
	dir := t.TempDir()
	override := filepath.Join(dir, "team", "templates", "envelope", "active.yaml")
	if err := os.MkdirAll(filepath.Dir(override), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const customYAML = `schema_version: 1
snapshot_date: 2026-05-25
frame: |
  CUSTOM-HEADER {{.Kind}} {{.Sender}}
  {{.Text}}
  {{.ReplyInstruction}}
roles:
  principal: "OPERATOR"
  system: "operator-system"
  peer_steward: "@{{.Handle}} steward"
  peer_worker: "@{{.Handle}} worker"
reply_instruction:
  a2a: "a2a-ack"
  attention_reply: "att-ack"
  none: "no-reply"
  chat: "chat-ack"
fallbacks:
  empty_kind: "msg"
  empty_handle: "anon"
`
	if err := os.WriteFile(override, []byte(customYAML), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := &recordingWarner{}
	l := NewLoader(r.fn()).WithHubData(dir)
	tpl := l.Resolve()
	if tpl.Origin != OriginOperator {
		t.Errorf("origin = %q, want %q", tpl.Origin, OriginOperator)
	}
	got := tpl.Render(Message{
		Kind:     "directive",
		FromRole: "principal",
		Text:     "x",
	})
	if !strings.Contains(got, "CUSTOM-HEADER") {
		t.Errorf("operator override not used: %q", got)
	}
	if !strings.Contains(got, "OPERATOR") {
		t.Errorf("operator role label not used: %q", got)
	}
	if len(r.snapshot()) != 0 {
		t.Errorf("happy-path operator override should not warn: %v",
			r.snapshot())
	}
}

func TestResolve_EnvOverridePathBeatsHubData(t *testing.T) {
	// TERMIPOD_ENVELOPE_TEMPLATE wins over the $HUB_DATA-derived
	// path. Tests + ops use this to point at an arbitrary file
	// without staging a full data root.
	dir := t.TempDir()
	envPath := filepath.Join(dir, "envelope-override.yaml")
	const envYAML = `schema_version: 1
snapshot_date: 2026-05-25
frame: "ENV-MARKER {{.Text}}"
roles:
  principal: "p"
  system: "s"
  peer_steward: "ps"
  peer_worker: "pw"
reply_instruction:
  a2a: "a"
  attention_reply: "ar"
  none: "n"
  chat: "c"
fallbacks:
  empty_kind: "m"
  empty_handle: "h"
`
	if err := os.WriteFile(envPath, []byte(envYAML), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(envOverridePath, envPath)

	// Even with a populated hubData root, env wins.
	hubData := t.TempDir()
	l := NewLoader(nil).WithHubData(hubData)
	tpl := l.Resolve()
	if tpl.Origin != OriginOperator {
		t.Errorf("origin = %q, want operator (env)", tpl.Origin)
	}
	got := tpl.Render(Message{Kind: "x", FromRole: "principal", Text: "y"})
	if !strings.Contains(got, "ENV-MARKER") {
		t.Errorf("env-override path not used: %q", got)
	}
}

func TestResolve_ParseErrorFallsThroughToEmbedded(t *testing.T) {
	// A garbage operator file must fall back to embedded AND emit
	// exactly one `envelope.config_error` audit row. The chip-side
	// equivalent (pricing's parse-error fallback) follows the same
	// pattern — operator gets a visible signal, prod render keeps
	// working.
	dir := t.TempDir()
	override := filepath.Join(dir, "team", "templates", "envelope", "active.yaml")
	if err := os.MkdirAll(filepath.Dir(override), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(override, []byte("not: [valid: yaml: garbage"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := &recordingWarner{}
	l := NewLoader(r.fn()).WithHubData(dir)
	tpl := l.Resolve()
	if tpl.Origin != OriginEmbedded {
		t.Errorf("origin = %q, want embedded (parse-error fallback)", tpl.Origin)
	}
	calls := r.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(calls), calls)
	}
	if calls[0].Action != "envelope.config_error" {
		t.Errorf("action = %q, want envelope.config_error", calls[0].Action)
	}
	if calls[0].Meta["kind"] != "parse_failed" {
		t.Errorf("meta.kind = %v, want parse_failed", calls[0].Meta["kind"])
	}
}

func TestResolve_ValidationFailureFallsThroughToEmbedded(t *testing.T) {
	// Schema-valid YAML but Validate fails (schema_version = 99).
	// Same audit + fallback contract as parse failure.
	dir := t.TempDir()
	override := filepath.Join(dir, "team", "templates", "envelope", "active.yaml")
	if err := os.MkdirAll(filepath.Dir(override), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const bad = `schema_version: 99
snapshot_date: 2026-05-25
frame: "x"
roles: {principal: p}
reply_instruction: {chat: c}
fallbacks: {empty_kind: m, empty_handle: h}
`
	if err := os.WriteFile(override, []byte(bad), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := &recordingWarner{}
	l := NewLoader(r.fn()).WithHubData(dir)
	tpl := l.Resolve()
	if tpl.Origin != OriginEmbedded {
		t.Errorf("origin = %q, want embedded", tpl.Origin)
	}
	calls := r.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(calls), calls)
	}
	if calls[0].Meta["kind"] != "parse_failed" {
		t.Errorf("validation failure also lands as parse_failed; got %v",
			calls[0].Meta["kind"])
	}
}

func TestResolve_CachesEmbeddedOnSecondCall(t *testing.T) {
	// The embedded tier should not re-parse YAML on every call.
	// Pin by identity: two calls in a row return the same *Templates.
	l := NewLoader(nil).WithHubData(t.TempDir())
	a := l.Resolve()
	b := l.Resolve()
	if a != b {
		t.Errorf("embedded Resolve should return identical *Templates on second call")
	}
}

func TestResolve_CachesOperatorByMtime(t *testing.T) {
	// Operator override at mtime T1 is cached. After mtime advances
	// (file rewritten), the next Resolve re-parses and returns a
	// fresh *Templates whose Render output reflects the new content.
	dir := t.TempDir()
	override := filepath.Join(dir, "team", "templates", "envelope", "active.yaml")
	if err := os.MkdirAll(filepath.Dir(override), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	v1 := `schema_version: 1
snapshot_date: 2026-05-25
frame: "V1 {{.Text}}"
roles: {principal: p, system: s, peer_steward: ps, peer_worker: pw}
reply_instruction: {a2a: a, attention_reply: ar, none: n, chat: c}
fallbacks: {empty_kind: m, empty_handle: h}
`
	if err := os.WriteFile(override, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	// Set a deterministic mtime so the second write's mtime is
	// strictly newer even when the filesystem only stores second-
	// granularity timestamps (ext4 default).
	past := time.Now().Add(-10 * time.Second)
	if err := os.Chtimes(override, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	l := NewLoader(nil).WithHubData(dir)
	tpl1 := l.Resolve()
	got1 := tpl1.Render(Message{Kind: "x", FromRole: "principal", Text: "hi"})
	if !strings.Contains(got1, "V1 hi") {
		t.Fatalf("v1 not rendered: %q", got1)
	}

	v2 := strings.Replace(v1, "V1", "V2", 1)
	if err := os.WriteFile(override, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	// Force a strictly-newer mtime regardless of FS granularity.
	now := time.Now()
	if err := os.Chtimes(override, now, now); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	tpl2 := l.Resolve()
	if tpl1 == tpl2 {
		t.Errorf("mtime change should cause re-parse and a fresh *Templates")
	}
	got2 := tpl2.Render(Message{Kind: "x", FromRole: "principal", Text: "hi"})
	if !strings.Contains(got2, "V2 hi") {
		t.Errorf("v2 not picked up after mtime tick: %q", got2)
	}
}

func TestResolve_PermissionDeniedWarnsAndFallsBack(t *testing.T) {
	// stat-failure that isn't NotExist warns under a different
	// `kind` (`stat_failed`) and still falls back. Skip on root —
	// chmod 000 bypasses there.
	if os.Geteuid() == 0 {
		t.Skip("skipping permission test as root")
	}
	dir := t.TempDir()
	// Create the directory tree, then chmod the parent to 0 so the
	// file itself becomes unstatable.
	parent := filepath.Join(dir, "team", "templates", "envelope")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	override := filepath.Join(parent, "active.yaml")
	if err := os.WriteFile(override, []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(parent, 0o755) // restore so cleanup can remove

	r := &recordingWarner{}
	l := NewLoader(r.fn()).WithHubData(dir)
	tpl := l.Resolve()
	if tpl.Origin != OriginEmbedded {
		t.Errorf("origin = %q, want embedded (stat-error fallback)", tpl.Origin)
	}
	calls := r.snapshot()
	if len(calls) == 0 {
		t.Fatalf("expected at least 1 warning, got none")
	}
	if calls[0].Meta["kind"] != "stat_failed" {
		t.Errorf("first call meta.kind = %v, want stat_failed",
			calls[0].Meta["kind"])
	}
}

func TestNewLoader_NilWarnerOK(t *testing.T) {
	// Production server passes a non-nil Warner, but tests + ops
	// scripts often pass nil. Validate the loader doesn't panic on
	// the silent-drop path.
	l := NewLoader(nil).WithHubData(t.TempDir())
	tpl := l.Resolve()
	if tpl == nil {
		t.Errorf("Resolve returned nil; loader must always yield a Templates")
	}
}
