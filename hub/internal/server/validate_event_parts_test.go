package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/termipod/hub/internal/events"
)

func TestValidateEventParts_RejectsEmptyArray(t *testing.T) {
	if reason := validateEventParts(nil); reason == "" {
		t.Error("nil parts should be rejected")
	}
	if reason := validateEventParts([]events.Part{}); reason == "" {
		t.Error("empty parts slice should be rejected")
	}
}

func TestValidateEventParts_AcceptsTextPart(t *testing.T) {
	parts := []events.Part{{Kind: "text", Text: "Hello"}}
	if reason := validateEventParts(parts); reason != "" {
		t.Errorf("valid text part rejected: %q", reason)
	}
}

func TestValidateEventParts_RejectsEmptyTextPart(t *testing.T) {
	parts := []events.Part{{Kind: "text", Text: ""}}
	reason := validateEventParts(parts)
	if !strings.Contains(reason, "kind=text") {
		t.Errorf("empty text part should be rejected with structured error: %q", reason)
	}
}

func TestValidateEventParts_RejectsMissingKind(t *testing.T) {
	parts := []events.Part{{Text: "no kind"}}
	reason := validateEventParts(parts)
	if !strings.Contains(reason, "kind") {
		t.Errorf("part with no kind should be rejected: %q", reason)
	}
}

func TestValidateEventParts_FilePartRequiresURI(t *testing.T) {
	parts := []events.Part{{Kind: "file", File: &events.BlobRef{}}}
	reason := validateEventParts(parts)
	if !strings.Contains(reason, "file.uri") {
		t.Errorf("file part without URI should be rejected: %q", reason)
	}
}

func TestValidateEventParts_FilePartWithURIPasses(t *testing.T) {
	parts := []events.Part{{Kind: "file", File: &events.BlobRef{URI: "sha256:abc"}}}
	if reason := validateEventParts(parts); reason != "" {
		t.Errorf("file part with URI rejected: %q", reason)
	}
}

func TestValidateEventParts_DataPartRequiresPayload(t *testing.T) {
	parts := []events.Part{{Kind: "data"}}
	reason := validateEventParts(parts)
	if !strings.Contains(reason, "data") {
		t.Errorf("data part without payload should be rejected: %q", reason)
	}
}

func TestValidateEventParts_DataPartWithPayloadPasses(t *testing.T) {
	parts := []events.Part{{Kind: "data", Data: json.RawMessage(`{"k":1}`)}}
	if reason := validateEventParts(parts); reason != "" {
		t.Errorf("data part with payload rejected: %q", reason)
	}
}

func TestValidateEventParts_UnknownKindToleratedIfNamed(t *testing.T) {
	// Forward-compat: unknown kind is tolerated as long as the kind
	// field itself is present. New client kinds shouldn't be 422'd
	// just because the hub binary hasn't been updated yet.
	parts := []events.Part{{Kind: "custom-future-kind"}}
	if reason := validateEventParts(parts); reason != "" {
		t.Errorf("unknown kind with non-empty name should be tolerated: %q", reason)
	}
}

func TestValidateEventParts_FirstBadPartReportsIndex(t *testing.T) {
	parts := []events.Part{
		{Kind: "text", Text: "ok"},
		{Kind: "text", Text: ""},
		{Kind: "text", Text: "also-ok"},
	}
	reason := validateEventParts(parts)
	if !strings.Contains(reason, "parts[1]") {
		t.Errorf("error should name the bad index parts[1]: %q", reason)
	}
}
