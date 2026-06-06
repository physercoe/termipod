package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// blobBytesForSha reads back the bytes the externalizer stored, via the blobs
// row, so the test asserts losslessness against what's actually on disk.
func blobBytesForSha(t *testing.T, c *e2eCtx, sha string) []byte {
	t.Helper()
	var path string
	if err := c.s.db.QueryRow(`SELECT scope_path FROM blobs WHERE sha256 = ?`, sha).Scan(&path); err != nil {
		t.Fatalf("blob row for %s: %v", sha, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read blob file: %v", err)
	}
	return b
}

func TestExternalize_SmallPayloadUnchanged(t *testing.T) {
	c := newE2E(t)
	in := `{"kind":"text","payload":{"text":"hello"}}`
	if got := c.s.externalizeLargePayload(context.Background(), in); got != in {
		t.Fatalf("small payload changed:\n in=%s\nout=%s", in, got)
	}
}

func TestExternalize_LargeLeafBecomesBlobRef(t *testing.T) {
	c := newE2E(t)
	big := strings.Repeat("A", payloadExternalizeThreshold+1)
	in, _ := json.Marshal(map[string]any{
		"name":           "attach",
		"content_base64": big,
		"small":          "keep me inline",
	})

	out := c.s.externalizeLargePayload(context.Background(), string(in))
	if out == string(in) {
		t.Fatal("expected externalization, payload unchanged")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	ref, _ := m["content_base64"].(string)
	if !strings.HasPrefix(ref, blobRefPrefix) {
		t.Fatalf("content_base64 not a blob ref: %q", ref)
	}
	if m["small"] != "keep me inline" {
		t.Fatalf("small field disturbed: %v", m["small"])
	}
	if m["name"] != "attach" {
		t.Fatalf("name field disturbed: %v", m["name"])
	}
	// Losslessness: the stored blob bytes equal the original string verbatim.
	sha := strings.TrimPrefix(ref, blobRefPrefix)
	if got := blobBytesForSha(t, c, sha); string(got) != big {
		t.Fatalf("blob bytes != original (len got=%d want=%d)", len(got), len(big))
	}
}

func TestExternalize_PreservesLargeInteger(t *testing.T) {
	c := newE2E(t)
	// 2^53+1 cannot be represented exactly as float64; a naive any round-trip
	// would corrupt it. UseNumber must keep it exact.
	const bigInt = "9007199254740993"
	big := strings.Repeat("Z", payloadExternalizeThreshold+1)
	in := `{"content":"` + big + `","input_tokens":` + bigInt + `}`

	out := c.s.externalizeLargePayload(context.Background(), in)
	if !strings.Contains(out, bigInt) {
		t.Fatalf("large integer corrupted; want %s in:\n%s", bigInt, out)
	}
}

func TestExternalize_NestedAndArray(t *testing.T) {
	c := newE2E(t)
	big := strings.Repeat("Q", payloadExternalizeThreshold+1)
	in, _ := json.Marshal(map[string]any{
		"outer": map[string]any{"inner": big},
		"list":  []any{"small", big},
	})
	out := c.s.externalizeLargePayload(context.Background(), string(in))

	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	inner := m["outer"].(map[string]any)["inner"].(string)
	if !strings.HasPrefix(inner, blobRefPrefix) {
		t.Fatalf("nested leaf not externalized: %q", inner)
	}
	list := m["list"].([]any)
	if list[0] != "small" {
		t.Fatalf("small array element disturbed: %v", list[0])
	}
	if !strings.HasPrefix(list[1].(string), blobRefPrefix) {
		t.Fatalf("array leaf not externalized: %v", list[1])
	}
}

func TestExternalize_NonJSONLeftVerbatim(t *testing.T) {
	c := newE2E(t)
	in := "not json " + strings.Repeat("x", payloadExternalizeThreshold+1)
	if got := c.s.externalizeLargePayload(context.Background(), in); got != in {
		t.Fatal("non-JSON oversized payload should be left verbatim")
	}
}

// End-to-end: a POST /events with a multi-MB field lands a SMALL row (ref, not
// bytes), proving the ingest wiring, not just the function.
func TestExternalize_ThroughIngest(t *testing.T) {
	c := newE2E(t)
	agent := NewID()
	if _, err := c.s.db.Exec(
		`INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		 VALUES (?, ?, 'ext-1', 'claude-code', 'running', ?)`,
		agent, c.teamID, NowUTC()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	big := strings.Repeat("B", 2*1024*1024) // 2 MiB inline base64-ish field
	status, _ := c.call("POST", "/v1/teams/"+c.teamID+"/agents/"+agent+"/events",
		map[string]any{
			"kind":     "tool_call",
			"producer": "agent",
			"payload":  map[string]any{"name": "attach", "content_base64": big},
		})
	if status != 201 {
		t.Fatalf("post event status=%d want 201", status)
	}

	var stored string
	if err := c.s.db.QueryRow(
		`SELECT payload_json FROM agent_events WHERE agent_id = ?`, agent).Scan(&stored); err != nil {
		t.Fatalf("read stored row: %v", err)
	}
	if len(stored) > 4096 {
		t.Fatalf("stored payload not externalized: %d bytes", len(stored))
	}
	if !strings.Contains(stored, blobRefPrefix) {
		t.Fatalf("stored payload missing blob ref: %s", stored)
	}
}
