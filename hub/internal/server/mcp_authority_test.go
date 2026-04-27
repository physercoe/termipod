package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMCPAuthority_RoundTrip verifies the consolidation: a spawned-agent
// MCP client posting tools/list to /mcp/<token> sees the rich-authority
// catalog (e.g. projects.list, agents.spawn, schedules.create) advertised
// alongside the in-process catalog. The hub used to require a second MCP
// daemon (hub-mcp-server) to expose those names — v1.0.297 wires them
// in-process via a chi-router transport, so one bridge entry in
// .mcp.json now reaches the union of both surfaces.
func TestMCPAuthority_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	token, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)

	// tools/list: a sample of authority names must appear.
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST",
		srv.URL+"/mcp/"+token, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var listOut struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &listOut); err != nil {
		t.Fatalf("decode tools/list: %v (%s)", err, raw)
	}
	have := make(map[string]bool, len(listOut.Result.Tools))
	for _, tt := range listOut.Result.Tools {
		have[tt.Name] = true
	}
	for _, want := range []string{
		"projects.list", "plans.create", "agents.spawn",
		"schedules.run", "audit.read", "a2a.invoke",
	} {
		if !have[want] {
			t.Errorf("authority tool %q missing from tools/list", want)
		}
	}

	// tools/call projects.list — exercises the chi-router transport
	// hitting GET /v1/teams/{team}/projects in-process. Returns an
	// empty list since no projects exist yet, but a 200 with valid
	// JSON content proves dispatch worked end-to-end.
	body, _ = json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "projects.list",
			"arguments": map[string]any{},
		},
	})
	req, _ = http.NewRequestWithContext(context.Background(), "POST",
		srv.URL+"/mcp/"+token, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var callOut struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &callOut); err != nil {
		t.Fatalf("decode tools/call: %v (%s)", err, raw)
	}
	if callOut.Error != nil {
		t.Fatalf("tools/call returned error: %+v (raw=%s)", callOut.Error, raw)
	}
	if callOut.Result == nil {
		t.Fatalf("tools/call missing result: %s", raw)
	}
}
