package hostagent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		in        string
		wantKind  string
		wantBody  string
		wantMatch bool
	}{
		{`<<mcp:post_message {"text":"hi"}>>`, "post_message", `{"text":"hi"}`, true},
		{`<<mcp:request_approval {"tier":"critical"}>>`, "request_approval", `{"tier":"critical"}`, true},
		{`<<mcp:ping>>`, "ping", "", true},
		{"plain shell output", "", "", false},
		{`<<mcp:end>>`, "end", "", true},
		// Trailing CR should be tolerated (tmux pipe-pane often emits CRLF).
		{`<<mcp:ping>>` + "\r", "ping", "", true},
	}
	for _, c := range cases {
		kind, body, ok := ParseLine(c.in)
		if ok != c.wantMatch {
			t.Errorf("ParseLine(%q) match = %v, want %v", c.in, ok, c.wantMatch)
			continue
		}
		if kind != c.wantKind {
			t.Errorf("ParseLine(%q) kind = %q, want %q", c.in, kind, c.wantKind)
		}
		if string(body) != c.wantBody {
			t.Errorf("ParseLine(%q) body = %q, want %q", c.in, string(body), c.wantBody)
		}
	}
}

func TestScan(t *testing.T) {
	in := `build step 1
<<mcp:post_message {"text":"done"}>>
more output
<<mcp:ping>>
`
	var seen []string
	if err := Scan(strings.NewReader(in), func(kind string, body []byte) {
		seen = append(seen, kind+":"+string(body))
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{`post_message:{"text":"done"}`, "ping:"}
	if len(seen) != len(want) {
		t.Fatalf("got %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("idx %d: got %q want %q", i, seen[i], want[i])
		}
	}
}

// TestTailer_ForwardsPostMessage exercises the marker → hub HTTP path
// without touching tmux. We feed a parsed marker into handleMarker directly
// and assert the client issues a POST to the expected channel endpoint with
// a single text part.
func TestTailer_ForwardsPostMessage(t *testing.T) {
	var (
		mu      sync.Mutex
		gotPath string
		gotBody map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"evt_x","received_ts":"now"}`))
	}))
	defer srv.Close()

	tl := &Tailer{
		AgentID:   "agt_child",
		ProjectID: "proj_x",
		ChannelID: "chn_default",
		Client:    NewClient(srv.URL, "tok", "team_t"),
	}

	body := []byte(`{"channel_id":"chn_override","text":"hello from pane"}`)
	tl.handleMarker("post_message", body)

	mu.Lock()
	defer mu.Unlock()
	wantPath := "/v1/teams/team_t/projects/proj_x/channels/chn_override/events"
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q", gotPath, wantPath)
	}
	if gotBody["type"] != "message" {
		t.Errorf("type = %v, want message", gotBody["type"])
	}
	if gotBody["from_id"] != "agt_child" {
		t.Errorf("from_id = %v, want agt_child", gotBody["from_id"])
	}
	parts, _ := gotBody["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("parts len = %d, want 1", len(parts))
	}
	first, _ := parts[0].(map[string]any)
	if first["kind"] != "text" {
		t.Errorf("part kind = %v, want text", first["kind"])
	}
	if first["text"] != "hello from pane" {
		t.Errorf("text = %v, want hello from pane", first["text"])
	}
}

// TestTailer_AttachMarker verifies the full attach path: read → upload blob
// → emit attach event carrying a BlobRef. Uses a fake hub that records both
// the blob POST and the event POST in order.
func TestTailer_AttachMarker(t *testing.T) {
	var (
		mu       sync.Mutex
		blobHit  bool
		eventHit bool
		gotBody  map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/blobs"):
			blobHit = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"sha256":"deadbeef","size":5,"mime":"text/plain"}`))
		case strings.Contains(r.URL.Path, "/events"):
			eventHit = true
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &gotBody)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Write a small file the marker will reference.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	tl := &Tailer{
		AgentID:   "agt_a",
		ProjectID: "proj_x",
		ChannelID: "chn_bound",
		Client:    NewClient(srv.URL, "tok", "team_t"),
	}
	marker := []byte(`{"path":"` + filePath + `","mime":"text/plain","note":"see attached"}`)
	tl.handleMarker("attach", marker)

	mu.Lock()
	defer mu.Unlock()
	if !blobHit {
		t.Error("blob upload was never called")
	}
	if !eventHit {
		t.Fatal("event post was never called")
	}
	if gotBody["type"] != "attach" {
		t.Errorf("event type = %v, want attach", gotBody["type"])
	}
	parts, _ := gotBody["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("parts len = %d, want 2 (file + note text)", len(parts))
	}
	file, _ := parts[0].(map[string]any)
	if file["kind"] != "file" {
		t.Errorf("part[0].kind = %v, want file", file["kind"])
	}
	ref, _ := file["file"].(map[string]any)
	if ref["uri"] != "hub-blob://deadbeef" {
		t.Errorf("uri = %v, want hub-blob://deadbeef", ref["uri"])
	}
	note, _ := parts[1].(map[string]any)
	if note["text"] != "see attached" {
		t.Errorf("note text = %v, want see attached", note["text"])
	}
}

// TestTailer_DefaultChannelWhenBodyOmits covers the fallback where a marker
// body has no channel_id: the tailer must fall back to its bound ChannelID
// so tap scripts can drop the field for conciseness.
func TestTailer_DefaultChannelWhenBodyOmits(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tl := &Tailer{
		AgentID:   "agt_a",
		ProjectID: "proj_x",
		ChannelID: "chn_bound",
		Client:    NewClient(srv.URL, "tok", "team_t"),
	}
	tl.handleMarker("post_message", []byte(`{"text":"no channel"}`))

	want := "/v1/teams/team_t/projects/proj_x/channels/chn_bound/events"
	if gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
}

// TestParseSpec_ChannelBinding confirms that the YAML shape produced by
// scheduler / spawn CLI round-trips through ParseSpec, since that parse is
// what gates Tailer startup in launchOne.
func TestParseSpec_ChannelBinding(t *testing.T) {
	yaml := `kind: claude-code
project_id: proj_42
channel_id: chn_general
backend:
  cmd: "echo hi"
`
	spec, err := ParseSpec(yaml)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if spec.ProjectID != "proj_42" {
		t.Errorf("ProjectID = %q, want proj_42", spec.ProjectID)
	}
	if spec.ChannelID != "chn_general" {
		t.Errorf("ChannelID = %q, want chn_general", spec.ChannelID)
	}
	if spec.Backend.Cmd != "echo hi" {
		t.Errorf("Backend.Cmd = %q, want echo hi", spec.Backend.Cmd)
	}
}
