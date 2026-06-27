package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestFoldStripsBodiesWithoutChangingDigest pins #118 §3: the fold loaders
// strip the large display bodies (text/content/message/…) server-side, and
// doing so must NOT change the computed digest — the fold only reads small
// structured keys. The brute==incremental tests can't catch a wrongly-dropped
// field (both paths share the same projection and would break identically), so
// this compares the stripped-from-DB digest against the full-payload reference.
func TestFoldStripsBodiesWithoutChangingDigest(t *testing.T) {
	s, _ := newA2ATestServer(t)
	ctx := context.Background()

	v, memEvents := loadDigestVector(t)
	// Reference digest: the canonical vector folded with FULL in-memory payloads.
	wantD, _ := computeAgentDigest("a", v.TeamID, memEvents)

	agentID := seedAgentRow(t, s, defaultTeamID, "stripper", "claude-code")
	ew := evWForTeam(t, s, defaultTeamID)
	big := strings.Repeat("x", 50000) // simulate an accumulated-transcript body
	for _, e := range v.Events {
		p := map[string]any{}
		for k, val := range e.Payload {
			p[k] = val
		}
		// Heavy bodies that must be stripped. None is read by the fold, so a
		// correct strip leaves the digest identical to the reference above.
		p["text"] = big
		p["content"] = big
		p["message"] = big
		pj, _ := json.Marshal(p)
		if _, err := ew.Exec(
			`INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"e-"+itoaInt(int(e.Seq)), agentID, e.Seq, e.TS, e.Kind, e.Producer, string(pj),
		); err != nil {
			t.Fatalf("insert event seq=%d: %v", e.Seq, err)
		}
	}

	er, err := s.eventsReader(defaultTeamID)
	if err != nil {
		t.Fatalf("eventsReader: %v", err)
	}
	got, err := loadFoldEvents(ctx, er, agentID)
	if err != nil {
		t.Fatalf("loadFoldEvents: %v", err)
	}
	if len(got) != len(v.Events) {
		t.Fatalf("loaded %d events, want %d", len(got), len(v.Events))
	}

	// The bodies must actually be gone — guards against json_remove silently
	// no-op'ing (e.g. a JSON1 regression), which would defeat the optimization.
	for _, fe := range got {
		for _, k := range []string{"text", "content", "message"} {
			if _, ok := fe.Payload[k]; ok {
				t.Fatalf("seq=%d: %q body not stripped", fe.Seq, k)
			}
		}
	}

	gotD, _ := computeAgentDigest("a", v.TeamID, got)
	if !reflect.DeepEqual(digestJSON(gotD), digestJSON(wantD)) {
		t.Errorf("stripped digest != full-payload digest\n got: %+v\nwant: %+v",
			digestJSON(gotD), digestJSON(wantD))
	}
}
