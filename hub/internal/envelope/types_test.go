package envelope

import (
	"strings"
	"testing"
)

// minimalTemplates returns a small, valid Templates value suitable
// for Render tests. Mirrors the embedded default's shape but keeps
// the strings short so test assertions are self-evident.
func minimalTemplates(t *testing.T) *Templates {
	t.Helper()
	tpl := &Templates{
		SchemaVersion: 1,
		SnapshotDate:  "2026-05-25",
		Frame:         "[{{.Kind}} from {{.Sender}}]\n{{.Text}}\n\n{{.ReplyInstruction}}",
		Roles: map[string]string{
			"principal":    "the principal",
			"system":       "the system",
			"peer_steward": "@{{.Handle}} (a peer steward)",
			"peer_worker":  "@{{.Handle}} (a peer worker)",
			"default":      "@{{.Handle}}",
		},
		ReplyInstruction: map[string]string{
			"a2a":             `a2a-reply to {{.FromHandle}}`,
			"attention_reply": "attention-reply",
			"none":            "no reply",
			"chat":            "chat-reply",
		},
		Fallbacks: Fallbacks{
			EmptyKind:   "message",
			EmptyHandle: "an agent",
		},
	}
	if err := tpl.Validate(); err != nil {
		t.Fatalf("minimalTemplates: %v", err)
	}
	return tpl
}

func TestValidate_AcceptsMinimalShape(t *testing.T) {
	// Smoke — guards against a future refactor that tightens
	// Validate beyond what minimalTemplates can satisfy. If this
	// fails, the rest of the test file's setUp is broken.
	_ = minimalTemplates(t)
}

func TestValidate_RejectsUnknownSchemaVersion(t *testing.T) {
	tpl := minimalTemplates(t)
	tpl.SchemaVersion = 2
	if err := tpl.Validate(); err == nil {
		t.Errorf("schema_version=2 should be rejected")
	}
}

func TestValidate_RejectsEmptyFrame(t *testing.T) {
	tpl := minimalTemplates(t)
	tpl.Frame = ""
	if err := tpl.Validate(); err == nil {
		t.Errorf("empty frame should be rejected")
	}
}

func TestValidate_RejectsEmptyFallbacks(t *testing.T) {
	tpl := minimalTemplates(t)
	tpl.Fallbacks.EmptyKind = ""
	if err := tpl.Validate(); err == nil {
		t.Errorf("empty empty_kind should be rejected")
	}
	tpl = minimalTemplates(t)
	tpl.Fallbacks.EmptyHandle = ""
	if err := tpl.Validate(); err == nil {
		t.Errorf("empty empty_handle should be rejected")
	}
}

func TestValidate_RejectsMalformedTemplateString(t *testing.T) {
	// A typo'd action ({{.Kin) is a parse error from text/template;
	// the loader treats this as a full-Templates parse failure and
	// falls back to embedded. Pin the rejection so a refactor that
	// suppresses parse errors gets caught.
	tpl := minimalTemplates(t)
	tpl.Frame = "[{{.Kin from {{.Sender}}]"
	if err := tpl.Validate(); err == nil {
		t.Errorf("malformed frame should be rejected")
	}
	tpl = minimalTemplates(t)
	tpl.Roles["principal"] = "{{.Handle"
	if err := tpl.Validate(); err == nil {
		t.Errorf("malformed role template should be rejected")
	}
}

// RenderSender is the public sender-label resolver consumed by the
// mobile-facing `from_label` stamp (server/input_envelope.go). The
// cases below mirror the cascade RenderSender delegates to so the
// public surface is exercised independently of the full-frame Render
// path — a refactor that broke RenderSender alone would otherwise
// only surface via the mobile UI smoke.
func TestRenderSender(t *testing.T) {
	tpl := minimalTemplates(t)
	cases := []struct {
		name   string
		role   string
		handle string
		want   string
	}{
		{
			name: "principal-bare-no-handle",
			role: "principal",
			want: "the principal",
		},
		{
			name:   "peer_steward-includes-handle",
			role:   "peer_steward",
			handle: "research-steward",
			want:   "@research-steward (a peer steward)",
		},
		{
			name:   "peer_worker-includes-handle",
			role:   "peer_worker",
			handle: "coder-1",
			want:   "@coder-1 (a peer worker)",
		},
		{
			name: "system-bare-no-handle",
			role: "system",
			want: "the system",
		},
		{
			// Unknown role falls through to the "default" template
			// (bare handle), then to "@" + raw handle. Matches the
			// existing TestRender_UnknownRole* cases shape.
			name:   "unknown-role-handle-via-default",
			role:   "observer",
			handle: "spy-1",
			want:   "@spy-1",
		},
		{
			// Leading @ on the handle is stripped before being passed
			// to the role template — every operator-facing variable
			// is consistently shaped.
			name:   "handle-leading-at-is-stripped",
			role:   "peer_worker",
			handle: "@dup-prefix",
			want:   "@dup-prefix (a peer worker)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tpl.RenderSender(tc.role, tc.handle)
			if got != tc.want {
				t.Fatalf("RenderSender(%q, %q) = %q; want %q",
					tc.role, tc.handle, got, tc.want)
			}
		})
	}
}

func TestRender_PrincipalDirective(t *testing.T) {
	tpl := minimalTemplates(t)
	got := tpl.Render(Message{
		Kind:      "directive",
		FromRole:  "principal",
		Transport: "session",
		Text:      "survey the citation graph",
	})
	// Header + body + reply instruction lines on a chat transport.
	if !strings.Contains(got, "[directive from the principal]") {
		t.Errorf("header missing principal-directive marker: %q", got)
	}
	if !strings.Contains(got, "survey the citation graph") {
		t.Errorf("body text missing: %q", got)
	}
	if !strings.Contains(got, "chat-reply") {
		t.Errorf("chat reply instruction missing: %q", got)
	}
}

func TestRender_A2APeerSteward(t *testing.T) {
	tpl := minimalTemplates(t)
	got := tpl.Render(Message{
		Kind:       "directive",
		FromRole:   "peer_steward",
		FromHandle: "research-steward",
		Transport:  "a2a",
		Text:       "run the ablation",
	})
	if !strings.Contains(got, "@research-steward (a peer steward)") {
		t.Errorf("peer_steward sender description missing: %q", got)
	}
	// reply_via derives from (directive, a2a) → "a2a"; FromHandle
	// must be the bare form (no @).
	if !strings.Contains(got, "a2a-reply to research-steward") {
		t.Errorf("a2a reply instruction missing or wrong-shaped: %q", got)
	}
}

func TestRender_SystemNotification(t *testing.T) {
	tpl := minimalTemplates(t)
	got := tpl.Render(Message{
		Kind:      "notification",
		FromRole:  "system",
		Transport: "session",
		Text:      "Task 'X' completed.",
	})
	if !strings.Contains(got, "[notification from the system]") {
		t.Errorf("system-notification header missing: %q", got)
	}
	// DeriveReplyVia(notification, *) → "none" regardless of
	// transport. Notifications never reply.
	if !strings.Contains(got, "no reply") {
		t.Errorf("notification should route none reply_via: %q", got)
	}
}

func TestRender_EmptyKindUsesFallback(t *testing.T) {
	// A malformed legacy row could land with kind="" — render must
	// not blank the header bracket entirely. Falls back to the
	// configured `empty_kind` sentinel.
	tpl := minimalTemplates(t)
	got := tpl.Render(Message{
		Kind:     "",
		FromRole: "principal",
		Text:     "hello",
	})
	if !strings.Contains(got, "[message from the principal]") {
		t.Errorf("empty kind should fall back to %q: %q",
			tpl.Fallbacks.EmptyKind, got)
	}
}

func TestRender_UnknownRoleFallsThroughToDefault(t *testing.T) {
	tpl := minimalTemplates(t)
	got := tpl.Render(Message{
		Kind:       "directive",
		FromRole:   "future_role",
		FromHandle: "future-bot",
	})
	// Falls through to roles["default"]: "@{{.Handle}}".
	if !strings.Contains(got, "[directive from @future-bot]") {
		t.Errorf("unknown role should fall through to default template: %q", got)
	}
}

func TestRender_UnknownRoleNoDefaultFallsToBareHandle(t *testing.T) {
	tpl := minimalTemplates(t)
	delete(tpl.roleTmpls, "default")
	got := tpl.Render(Message{
		Kind:       "directive",
		FromRole:   "ghost",
		FromHandle: "spectre",
	})
	if !strings.Contains(got, "@spectre") {
		t.Errorf("bare-handle final fallback missing: %q", got)
	}
}

func TestRender_UnknownRoleNoHandleFallsToEmptyHandleFallback(t *testing.T) {
	// No matching role template AND no handle → falls through to
	// the EmptyHandle fallback ("an agent"). This is the worst-case
	// renderer path; the assertion just guarantees we never emit a
	// bare "@" with nothing after it.
	tpl := minimalTemplates(t)
	delete(tpl.roleTmpls, "default")
	got := tpl.Render(Message{
		Kind:     "directive",
		FromRole: "ghost",
	})
	if !strings.Contains(got, "an agent") {
		t.Errorf("empty handle fallback missing: %q", got)
	}
	if strings.Contains(got, "from @\n") {
		t.Errorf("rendered bare @ with empty handle: %q", got)
	}
}

func TestRender_HandleStripsLeadingAt(t *testing.T) {
	// Mobile sometimes sends handles with a leading @ on the wire;
	// the renderer normalises to the bare form before passing to
	// the template, so `@{{.Handle}}` doesn't produce `@@research`.
	tpl := minimalTemplates(t)
	got := tpl.Render(Message{
		Kind:       "directive",
		FromRole:   "peer_steward",
		FromHandle: "@research-steward",
	})
	if strings.Contains(got, "@@research-steward") {
		t.Errorf("leading @ should be normalised away: %q", got)
	}
	if !strings.Contains(got, "@research-steward (a peer steward)") {
		t.Errorf("normalised handle missing: %q", got)
	}
}

func TestRender_MissingReplyViaCollapsesLine(t *testing.T) {
	// A template missing the chat reply_instruction renders an
	// empty string at that slot. We assert the rendered output
	// still contains the body — i.e. an empty reply_instruction
	// can never blank the whole turn.
	tpl := minimalTemplates(t)
	delete(tpl.replyTmpls, "chat")
	got := tpl.Render(Message{
		Kind:     "directive",
		FromRole: "principal",
		Text:     "still here",
	})
	if !strings.Contains(got, "still here") {
		t.Errorf("body lost when reply_instruction missing: %q", got)
	}
}

func TestDeriveReplyVia(t *testing.T) {
	cases := []struct{ kind, transport, want string }{
		{"notification", "session", "none"},
		{"notification", "a2a", "none"},
		{"directive", "a2a", "a2a"},
		{"report", "a2a", "a2a"},
		{"question", "attention", "attention_reply"},
		{"directive", "session", "chat"},
		{"report", "", "chat"}, // unknown transport → chat default
	}
	for _, c := range cases {
		got := DeriveReplyVia(c.kind, c.transport)
		if got != c.want {
			t.Errorf("DeriveReplyVia(%q,%q) = %q, want %q",
				c.kind, c.transport, got, c.want)
		}
	}
}
