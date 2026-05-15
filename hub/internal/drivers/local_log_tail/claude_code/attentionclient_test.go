package claudecode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// hubMock is a tiny fake hub that serves /v1/teams/<team>/attention
// + /v1/teams/<team>/attention/{id}. POST stores the row + auto-
// generates an id; GET returns the current state. Tests drive
// resolution by calling .resolve(id, decision, reason).
type hubMock struct {
	mu       sync.Mutex
	rows     map[string]map[string]any
	counter  int64
	failNext int32
}

func newHubMock() *hubMock {
	return &hubMock{rows: map[string]map[string]any{}}
}

func (h *hubMock) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/teams/", func(w http.ResponseWriter, r *http.Request) {
		// Routing: /v1/teams/<team>/attention   POST/GET
		//          /v1/teams/<team>/attention/<id>  GET
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		// parts: ["v1", "teams", "<team>", "attention", ?<id>]
		if len(parts) < 4 || parts[0] != "v1" || parts[1] != "teams" || parts[3] != "attention" {
			http.NotFound(w, r)
			return
		}
		if atomic.LoadInt32(&h.failNext) > 0 {
			atomic.AddInt32(&h.failNext, -1)
			http.Error(w, "synthetic 500", http.StatusInternalServerError)
			return
		}
		switch {
		case len(parts) == 4 && r.Method == http.MethodPost:
			h.handleCreate(w, r)
		case len(parts) == 5 && r.Method == http.MethodGet:
			h.handleGet(w, r, parts[4])
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

func (h *hubMock) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.counter++
	id := strings.ReplaceAll(time.Now().Format("20060102T150405.000000"), ".", "_")
	id = id + "_" + itoa(h.counter)
	row := map[string]any{
		"id":         id,
		"status":     "open",
		"decisions":  json.RawMessage("[]"),
		"created_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	for k, v := range body {
		row[k] = v
	}
	h.rows[id] = row
	_ = json.NewEncoder(w).Encode(row)
}

func (h *hubMock) handleGet(w http.ResponseWriter, _ *http.Request, id string) {
	h.mu.Lock()
	row, ok := h.rows[id]
	h.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(row)
}

func (h *hubMock) resolve(id, decision, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	row, ok := h.rows[id]
	if !ok {
		return
	}
	row["status"] = "resolved"
	row["resolved_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	row["decisions"] = json.RawMessage(`[{"decision":"` + decision + `","reason":"` + reason + `"}]`)
	h.rows[id] = row
}

func (h *hubMock) lastID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var maxID string
	var maxC int64
	for id, row := range h.rows {
		_ = row
		if id > maxID && parseCounter(id) >= maxC {
			maxID = id
			maxC = parseCounter(id)
		}
	}
	return maxID
}

func parseCounter(id string) int64 {
	parts := strings.Split(id, "_")
	if len(parts) == 0 {
		return 0
	}
	last := parts[len(parts)-1]
	var n int64
	for _, c := range last {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func newTestClient(t *testing.T) (*HubAttentionClient, *hubMock, func()) {
	t.Helper()
	hub := newHubMock()
	srv := httptest.NewServer(hub.handler())
	c := &HubAttentionClient{
		HubURL:      srv.URL,
		Team:        "t1",
		Token:       "tok",
		AgentHandle: "@worker",
		HTTP:        srv.Client(),
		PollInitial: 25 * time.Millisecond,
		PollMax:     100 * time.Millisecond,
	}
	return c, hub, srv.Close
}

func TestAttention_Park_ApproveFlow(t *testing.T) {
	c, hub, cleanup := newTestClient(t)
	defer cleanup()

	resultCh := make(chan struct {
		r   *ParkResult
		err error
	}, 1)
	go func() {
		r, err := c.Park(context.Background(), ParkRequest{
			Kind: "permission_prompt", Summary: "compact?", Severity: "minor",
			PendingPayload: map[string]any{"dialog_type": "compaction"},
		}, 5*time.Second)
		resultCh <- struct {
			r   *ParkResult
			err error
		}{r, err}
	}()

	// Wait for the insert to land.
	deadline := time.Now().Add(2 * time.Second)
	var id string
	for time.Now().Before(deadline) {
		if id = hub.lastID(); id != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("insert never landed on hub")
	}

	hub.resolve(id, "approve", "lgtm")

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("Park: %v", r.err)
		}
		if r.r.Decision != "approve" {
			t.Errorf("decision = %q, want approve", r.r.Decision)
		}
		if r.r.Reason != "lgtm" {
			t.Errorf("reason = %q", r.r.Reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Park did not return after resolve")
	}
}

func TestAttention_Park_RejectFlow(t *testing.T) {
	c, hub, cleanup := newTestClient(t)
	defer cleanup()

	resultCh := make(chan *ParkResult, 1)
	go func() {
		r, _ := c.Park(context.Background(), ParkRequest{Summary: "x"}, 5*time.Second)
		resultCh <- r
	}()
	deadline := time.Now().Add(2 * time.Second)
	var id string
	for time.Now().Before(deadline) {
		if id = hub.lastID(); id != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	hub.resolve(id, "reject", "no thanks")
	select {
	case r := <-resultCh:
		if r.Decision != "reject" {
			t.Errorf("decision = %q, want reject", r.Decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Park did not return")
	}
}

func TestAttention_Park_Timeout(t *testing.T) {
	c, _, cleanup := newTestClient(t)
	defer cleanup()

	start := time.Now()
	_, err := c.Park(context.Background(), ParkRequest{Summary: "stuck"}, 200*time.Millisecond)
	if err == nil {
		t.Fatal("Park returned nil error on timeout")
	}
	if err != ErrParkTimeout {
		t.Errorf("err = %v, want ErrParkTimeout", err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("Park returned in %v before timeout window", elapsed)
	}
}

func TestAttention_Park_ContextCancel(t *testing.T) {
	c, _, cleanup := newTestClient(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := c.Park(ctx, ParkRequest{Summary: "ctx test"}, 5*time.Second)
	if err == nil {
		t.Fatal("expected error on ctx cancel")
	}
	if err == ErrParkTimeout {
		t.Errorf("got ErrParkTimeout; want generic cancel")
	}
}

func TestAttention_Park_ValidatesConfig(t *testing.T) {
	tests := map[string]*HubAttentionClient{
		"missing HubURL": {Team: "t", Token: "tok"},
		"missing Team":   {HubURL: "http://x", Token: "tok"},
		"missing Token":  {HubURL: "http://x", Team: "t"},
	}
	for name, c := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := c.Park(context.Background(), ParkRequest{Summary: "x"}, 1*time.Second)
			if err == nil {
				t.Error("Park returned nil error on invalid config")
			}
		})
	}
}

func TestAttention_Park_HubInsertFailure(t *testing.T) {
	c, hub, cleanup := newTestClient(t)
	defer cleanup()
	atomic.StoreInt32(&hub.failNext, 1)

	_, err := c.Park(context.Background(), ParkRequest{Summary: "x"}, 1*time.Second)
	if err == nil {
		t.Fatal("Park returned nil error on hub 500")
	}
}

