package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: posting a message via MCP appends a JSONL line whose row
// matches what landed in SQLite, and a fresh DB reconstructed from that
// log contains the same event.
func TestReconstructDB_RoundTrip(t *testing.T) {
	s, dataRoot := newTestServer(t)
	channelID, agentID := seedChannelAndAgent(t, s, "", "")

	// send_message — hits the MCP path that writes both DB + JSONL.
	args, _ := json.Marshal(map[string]any{
		"channel_id": channelID,
		"text":       "hello for replay",
	})
	if _, jerr := s.mcpPostMessage(context.Background(), agentID, args); jerr != nil {
		t.Fatalf("send_message: %+v", jerr)
	}

	// At least one JSONL file must exist now.
	files, err := listJSONLFiles(filepath.Join(dataRoot, "event_log"))
	if err != nil {
		t.Fatalf("list jsonl: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no JSONL files written")
	}
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	if !strings.Contains(string(body), "hello for replay") {
		t.Errorf("jsonl missing event text: %s", body)
	}

	// Reconstruct into a fresh DB and confirm the row landed.
	target := filepath.Join(t.TempDir(), "rebuilt.db")
	nFiles, inserted, skipped, err := ReconstructDB(context.Background(), dataRoot, target)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if nFiles == 0 || inserted == 0 {
		t.Errorf("reconstruct counts: files=%d inserted=%d skipped=%d", nFiles, inserted, skipped)
	}

	// Open the rebuilt DB directly (bypassing New so we don't start a
	// scheduler / escalator on a throwaway file) and count events.
	db, err := OpenDB(target)
	if err != nil {
		t.Fatalf("open rebuilt: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE channel_id = ?`, channelID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != inserted {
		t.Errorf("rebuilt DB has %d events, reconstruct reported %d inserted", n, inserted)
	}
}

// No JSONL dir → clear error, not a silent no-op. An operator running
// reconstruct-db against the wrong path should see it immediately.
func TestReconstructDB_EmptyLog(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "rebuilt.db")
	_, _, _, err := ReconstructDB(context.Background(), dir, target)
	if err == nil || !strings.Contains(err.Error(), "no JSONL files") {
		t.Errorf("want 'no JSONL files' error, got %v", err)
	}
}

// Re-running reconstruct against the same target DB is idempotent —
// second pass must skip every row via ON CONFLICT.
func TestReconstructDB_Idempotent(t *testing.T) {
	s, dataRoot := newTestServer(t)
	channelID, agentID := seedChannelAndAgent(t, s, "", "")
	args, _ := json.Marshal(map[string]any{
		"channel_id": channelID,
		"text":       "idempotent",
	})
	if _, jerr := s.mcpPostMessage(context.Background(), agentID, args); jerr != nil {
		t.Fatalf("send: %+v", jerr)
	}
	target := filepath.Join(t.TempDir(), "rebuilt.db")
	_, firstIns, _, err := ReconstructDB(context.Background(), dataRoot, target)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if firstIns == 0 {
		t.Fatal("first pass inserted nothing")
	}
	_, secondIns, secondSkip, err := ReconstructDB(context.Background(), dataRoot, target)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if secondIns != 0 || secondSkip != firstIns {
		t.Errorf("want ins=0 skip=%d, got ins=%d skip=%d", firstIns, secondIns, secondSkip)
	}
}
