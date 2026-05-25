// Package envelope renders ADR-032 message envelopes into the
// engine-facing prose turn (header, body, reply instruction) from an
// operator-editable YAML template. The template lives at
// `<HUB_DATA>/team/templates/envelope/active.yaml` (mtime hot-reload)
// with the embedded default in `hub/templates/envelope/active.yaml`
// shipped via `//go:embed all:templates` on `hub.TemplatesFS`.
//
// Three-tier resolution, mirrors `hub/internal/pricing/`:
//
//   - Operator on-disk override under
//     `$HUB_DATA/team/templates/envelope/active.yaml` — also reachable
//     through the existing `/v1/teams/{team}/templates` REST surface
//     and the mobile TemplateEditorScreen.
//   - Embedded default at `templates/envelope/active.yaml` on
//     `hub.TemplatesFS`. Same bytes as the seeded operator file at
//     first install (the init.go walker seeds it). Two reasons we
//     re-read embedded as the loader's own fallback: (a) operator
//     might delete the seeded file, (b) a corrupted operator file
//     mid-deploy must not brick envelope rendering.
//   - Per-key graceful degradation: a parse-OK template that's
//     missing one role label or one reply_instruction key falls
//     through to the embedded version's value for that key alone.
//     The rest of the operator's customisation survives.
//
// Consumers obtain `*Templates` via a Loader and call `Render(env)`.
// The compiled `text/template` instances are built once in
// `Validate()` so `Render` is allocation-light per call.
package envelope

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"text/template"
)

// Templates is the parsed + compiled envelope template set. The zero
// value is unusable; callers always get one back from a Loader's
// `Resolve()` (loader.go).
type Templates struct {
	// SchemaVersion is the YAML schema version. Currently 1; bump
	// when the shape changes incompatibly. The loader rejects
	// unknown versions and falls through to the embedded default.
	SchemaVersion int `yaml:"schema_version"`

	// SnapshotDate is the YYYY-MM-DD the content was last reviewed
	// by the operator. Surfaced on audit warnings so an operator
	// spots config that has drifted from the rest of the deploy.
	SnapshotDate string `yaml:"snapshot_date"`

	// Frame is the text/template source for the complete engine
	// turn. Variables: .Kind .Sender .Text .ReplyInstruction.
	Frame string `yaml:"frame"`

	// Roles maps a role string (principal / system / peer_steward /
	// peer_worker — plus "default" for unrecognised roles) to a
	// text/template source. Variables: .Handle (raw, no @).
	Roles map[string]string `yaml:"roles"`

	// ReplyInstruction maps a reply_via string (a2a /
	// attention_reply / none / chat) to a text/template source.
	// Variables: .FromHandle (raw, no @).
	ReplyInstruction map[string]string `yaml:"reply_instruction"`

	// Fallbacks carries the per-field empty-input sentinels. Both
	// must be non-empty post-Validate.
	Fallbacks Fallbacks `yaml:"fallbacks"`

	// Origin labels the tier this Templates came from. Set by the
	// loader; not serialised.
	Origin Origin `yaml:"-"`

	// Compiled templates — built once in `compile()` so Render
	// doesn't re-parse per call. Not serialised.
	frameTmpl *template.Template            `yaml:"-"`
	roleTmpls map[string]*template.Template `yaml:"-"`
	replyTmpls map[string]*template.Template `yaml:"-"`
}

// Fallbacks carries the per-field empty-input sentinels.
type Fallbacks struct {
	EmptyKind   string `yaml:"empty_kind"`
	EmptyHandle string `yaml:"empty_handle"`
}

// Origin labels which tier the active Templates came from.
type Origin string

const (
	// OriginOperator means the templates came from the on-disk
	// path under <HUB_DATA>/team/templates/envelope/active.yaml.
	OriginOperator Origin = "operator"

	// OriginEmbedded means the embedded default was used (no
	// override file, file missing, or operator override failed
	// validation and the loader fell back).
	OriginEmbedded Origin = "embedded"
)

// Closed-enum keys the templates SHOULD provide. A template missing
// any of these still parses (`Validate` allows it) but the renderer
// falls through to the embedded default's value for the missing key.
// Centralised here so the contract is in one place.
var (
	requiredRoles = []string{
		"principal", "system", "peer_steward", "peer_worker",
		// "default" is intentionally NOT required — the role-map
		// lookup falls through to "default" for unknown roles, but a
		// template that omits both the role AND default just emits
		// the bare handle ("@h") via the renderer's own fallback.
	}
	requiredReplyVias = []string{
		"a2a", "attention_reply", "none", "chat",
	}
)

// Validate enforces minimum invariants and compiles the template
// strings. Called by the loader after YAML decode. Mutates the
// receiver to attach compiled templates.
//
// A returned error means the templates cannot be used at all — the
// loader falls through to the embedded default.
func (t *Templates) Validate() error {
	if t == nil {
		return errors.New("envelope: nil templates")
	}
	if t.SchemaVersion != 1 {
		return fmt.Errorf("envelope: unsupported schema version %d (want 1)", t.SchemaVersion)
	}
	if strings.TrimSpace(t.Frame) == "" {
		return errors.New("envelope: frame template is empty")
	}
	if t.Fallbacks.EmptyKind == "" {
		return errors.New("envelope: fallbacks.empty_kind is required")
	}
	if t.Fallbacks.EmptyHandle == "" {
		return errors.New("envelope: fallbacks.empty_handle is required")
	}
	if err := t.compile(); err != nil {
		return err
	}
	return nil
}

// compile parses every template string into a `*text/template.Template`.
// Each gets `Option("missingkey=zero")` so a typo'd variable becomes
// empty string rather than an exec error — degrading prose is
// strictly better than blocking the turn on a config bug.
//
// Per-template parse errors are returned eagerly; the loader treats
// them as a full-Templates parse failure and falls back to embedded.
func (t *Templates) compile() error {
	frame, err := template.New("frame").
		Option("missingkey=zero").
		Parse(t.Frame)
	if err != nil {
		return fmt.Errorf("envelope: frame template parse: %w", err)
	}
	t.frameTmpl = frame

	t.roleTmpls = make(map[string]*template.Template, len(t.Roles))
	for k, src := range t.Roles {
		tmpl, err := template.New("role:" + k).
			Option("missingkey=zero").
			Parse(src)
		if err != nil {
			return fmt.Errorf("envelope: role %q template parse: %w", k, err)
		}
		t.roleTmpls[k] = tmpl
	}

	t.replyTmpls = make(map[string]*template.Template, len(t.ReplyInstruction))
	for k, src := range t.ReplyInstruction {
		tmpl, err := template.New("reply:" + k).
			Option("missingkey=zero").
			Parse(src)
		if err != nil {
			return fmt.Errorf("envelope: reply_instruction %q template parse: %w", k, err)
		}
		t.replyTmpls[k] = tmpl
	}
	return nil
}

// Message is the input to Render. Mirrors `MessageEnvelope` on the
// server side so the renderer doesn't import the server package;
// callers project their envelope into this struct.
type Message struct {
	Kind       string
	FromRole   string
	FromHandle string
	Transport  string
	Text       string
}

// Render renders [m] into the engine-facing prose turn. Pure: no
// side effects, no logging — the loader's Warner surfaces any
// problems that bubbled up from template parsing. Compiled
// templates are non-nil post-Validate, so this never panics on a
// loader-produced *Templates.
//
// Unknown role / reply_via fall back to the "default" entry if
// present, then to the receiver's empty-handle / empty-kind
// fallbacks, then to the bare handle / kind. The cascade keeps the
// rendered turn always-renderable even when the template is missing
// a key entirely.
func (t *Templates) Render(m Message) string {
	kind := m.Kind
	if kind == "" {
		kind = t.Fallbacks.EmptyKind
	}
	sender := t.renderSender(m.FromRole, m.FromHandle)
	replyVia := DeriveReplyVia(m.Kind, m.Transport)
	replyInstruction := t.renderReplyInstruction(replyVia, m.FromHandle)

	var buf bytes.Buffer
	_ = t.frameTmpl.Execute(&buf, frameVars{
		Kind:             kind,
		Sender:           sender,
		Text:             m.Text,
		ReplyInstruction: replyInstruction,
	})
	return buf.String()
}

type frameVars struct {
	Kind             string
	Sender           string
	Text             string
	ReplyInstruction string
}

type roleVars struct {
	Handle string
}

type replyVars struct {
	FromHandle string
}

// RenderSender resolves an envelope role → human-readable sender
// description using the operator's role template. Public surface
// because mobile-facing payload stamping (see
// `handlers_agent_input.go`'s `from_label` field) needs the same
// resolution the engine-facing prose uses, so that a YAML edit to
// `roles.principal` reaches BOTH the engine and the mobile
// transcript header. Without this, the mobile feed renders the
// from-line from a parallel hardcoded Dart map and stays stale on
// every YAML edit — the bug surfaced on the v1.0.708 smoke.
//
// Same cascade as the internal renderSender it delegates to:
//
//   1. roles[role]              (explicit per-role template)
//   2. roles["default"]         (operator's catch-all)
//   3. "@<handle>" or empty_handle if handle is empty
//
// The handle is normalised to its bare form (no leading @) before
// being passed into the template — every operator-facing variable
// is consistently shaped.
func (t *Templates) RenderSender(role, handle string) string {
	return t.renderSender(role, handle)
}

// renderSender resolves a role → sender description. Cascade:
//   1. roles[role]              (explicit per-role template)
//   2. roles["default"]         (operator's catch-all)
//   3. "@<handle>" or empty_handle if handle is empty
//
// The handle is normalised to its bare form (no leading @) before
// being passed into the template — every operator-facing variable
// is consistently shaped.
func (t *Templates) renderSender(role, handle string) string {
	rawHandle := strings.TrimPrefix(handle, "@")
	vars := roleVars{Handle: rawHandle}
	if tmpl, ok := t.roleTmpls[role]; ok {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, vars); err == nil {
			return buf.String()
		}
	}
	if tmpl, ok := t.roleTmpls["default"]; ok {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, vars); err == nil {
			return buf.String()
		}
	}
	if rawHandle == "" {
		return t.Fallbacks.EmptyHandle
	}
	return "@" + rawHandle
}

// renderReplyInstruction resolves reply_via → instruction text.
// Unknown reply_via or template-exec failure → empty string (the
// frame's `{{.ReplyInstruction}}\n\n` line then collapses to a
// trailing blank line; that's fine — better than a hardcoded English
// fallback the operator can't tune away).
func (t *Templates) renderReplyInstruction(replyVia, fromHandle string) string {
	tmpl, ok := t.replyTmpls[replyVia]
	if !ok {
		return ""
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, replyVars{
		FromHandle: strings.TrimPrefix(fromHandle, "@"),
	}); err != nil {
		return ""
	}
	return buf.String()
}

// DeriveReplyVia computes the reply channel from the envelope's
// (kind, transport) pair per ADR-032 D-5. Mirrors the host-runner's
// `deriveReplyVia` so callers don't need to re-derive — same
// function, importable on both sides of the hub/host-runner split
// without cross-package coupling.
//
// Notifications never route a reply. A2A messages reply over A2A.
// Attention messages reply via their attention contract. Everything
// else replies in the agent's own chat (the default).
func DeriveReplyVia(kind, transport string) string {
	if kind == "notification" {
		return "none"
	}
	switch transport {
	case "a2a":
		return "a2a"
	case "attention":
		return "attention_reply"
	default:
		return "chat"
	}
}
