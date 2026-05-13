package hubmcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestHub spins up an httptest server that captures incoming paths and
// lets each test script its own responses. Returning just the client keeps
// caller code short — t.Cleanup keeps the server alive for the test.
func newTestHub(t *testing.T, handler http.HandlerFunc) *hubClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return newHubClient(srv.URL, "test-token", "team-alpha")
}

// TestToolsList_RoundTrip: sending a well-formed tools/list request must
// yield a result containing every tool name from buildTools(). This guards
// against a refactor that forgets to add a new tool to the dispatch table.
func TestToolsList_RoundTrip(t *testing.T) {
	// No hub calls happen for tools/list, but dispatch still needs a client
	// instance; we point it at an unreachable URL to assert that fact.
	c := newHubClient("http://127.0.0.1:1", "", "team-alpha")
	tools := buildTools()

	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response for an id'd request")
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error *jsonrpcError `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if len(resp.Result.Tools) != len(tools) {
		t.Fatalf("got %d tools, want %d", len(resp.Result.Tools), len(tools))
	}
	// Sanity: a couple of well-known names should always be present.
	names := map[string]bool{}
	for _, t := range resp.Result.Tools {
		names[t.Name] = true
	}
	for _, want := range []string{"projects.list", "plans.create", "audit.read", "policy.read"} {
		if !names[want] {
			t.Errorf("missing tool %q in tools/list response", want)
		}
	}
}

// TestToolsCall_ProjectsList: a tools/call for projects.list must hit
// GET /v1/teams/{team}/projects on the hub, forward the Authorization
// header, and return the hub's JSON body inside an MCP text content block.
func TestToolsCall_ProjectsList(t *testing.T) {
	var sawPath, sawAuth string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"p1","name":"alpha"}]`))
	})
	tools := buildTools()

	line := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"projects.list","arguments":{}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawPath != "/v1/teams/team-alpha/projects" {
		t.Errorf("hub saw path %q", sawPath)
	}
	if sawAuth != "Bearer test-token" {
		t.Errorf("hub saw auth %q, want 'Bearer test-token'", sawAuth)
	}
	// Decode the outer JSON-RPC envelope, then pull the JSON string out of
	// the content[0].text field and decode that too — that is the shape
	// MCP clients see.
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *jsonrpcError `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal envelope: %v (%s)", err, raw)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", resp.Error)
	}
	if resp.Result.IsError {
		t.Fatalf("tool reported isError: %+v", resp.Result.Content)
	}
	if len(resp.Result.Content) != 1 || resp.Result.Content[0].Type != "text" {
		t.Fatalf("unexpected content: %+v", resp.Result.Content)
	}
	if !strings.Contains(resp.Result.Content[0].Text, `"id":"p1"`) {
		t.Errorf("text content missing project: %q", resp.Result.Content[0].Text)
	}
}

// TestToolsCall_UnknownTool: calling a tool that isn't in the dispatch table
// must produce a method-not-found JSON-RPC error rather than a panic or a
// silent empty result, because that is the only signal an MCP client gets
// that its schema is out of date.
func TestToolsCall_UnknownTool(t *testing.T) {
	c := newHubClient("http://127.0.0.1:1", "", "team-alpha")
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"does.not.exist"}}` + "\n")
	raw, _ := handleLine(c, tools, line)
	var resp struct {
		Error *jsonrpcError `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != errMethodNotFound {
		t.Fatalf("want method-not-found, got %+v", resp.Error)
	}
}

// TestToolsCall_A2AInvoke: the a2a.invoke tool must first look up the card
// via the hub directory, then POST a JSON-RPC message/send envelope to the
// relay URL advertised in that card.
func TestToolsCall_A2AInvoke(t *testing.T) {
	var cardPath, relayPath, relayMethod, relayBody string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayPath = r.URL.Path
		relayMethod = r.Method
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		relayBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"mcp-xx","result":{"id":"task-1","status":{"state":"submitted"}}}`))
	}))
	t.Cleanup(relay.Close)

	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		cardPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		// Card.url points at the relay test server.
		body := `[{"host_id":"h1","agent_id":"a1","handle":"worker.ml","card":{"name":"Worker","url":"` + relay.URL + `/a2a/relay/h1/a1"}}]`
		_, _ = w.Write([]byte(body))
	})
	tools := buildTools()

	line := []byte(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"a2a.invoke","arguments":{"handle":"worker.ml","text":"train me"}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if !strings.Contains(cardPath, "/v1/teams/team-alpha/a2a/cards") || !strings.Contains(cardPath, "handle=worker.ml") {
		t.Errorf("card lookup path = %q", cardPath)
	}
	if relayMethod != "POST" || relayPath != "/a2a/relay/h1/a1" {
		t.Errorf("relay call = %s %s", relayMethod, relayPath)
	}
	if !strings.Contains(relayBody, `"method":"message/send"`) {
		t.Errorf("relay body missing method: %q", relayBody)
	}
	if !strings.Contains(relayBody, `"text":"train me"`) {
		t.Errorf("relay body missing text: %q", relayBody)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *jsonrpcError `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal envelope: %v (%s)", err, raw)
	}
	if resp.Error != nil || resp.Result.IsError {
		t.Fatalf("tool errored: err=%+v isError=%v content=%+v", resp.Error, resp.Result.IsError, resp.Result.Content)
	}
	if !strings.Contains(resp.Result.Content[0].Text, `"id":"task-1"`) {
		t.Errorf("response missing task id: %q", resp.Result.Content[0].Text)
	}
}

// TestToolsCall_PlanStepsUpdate: plans.steps.update must PATCH the step URL
// and surface a success shape even though the hub returns 204 No Content.
func TestToolsCall_PlanStepsUpdate(t *testing.T) {
	var sawMethod, sawPath, sawBody string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		sawBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"plans.steps.update","arguments":{"plan":"pl1","step":"s1","status":"running"}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawMethod != "PATCH" {
		t.Errorf("method = %q, want PATCH", sawMethod)
	}
	if sawPath != "/v1/teams/team-alpha/plans/pl1/steps/s1" {
		t.Errorf("path = %q", sawPath)
	}
	if !strings.Contains(sawBody, `"status":"running"`) {
		t.Errorf("body missing status: %q", sawBody)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Result.IsError {
		t.Errorf("unexpected isError")
	}
}

// TestToolsCall_A2AInvoke_NoCard: invoking a handle with no registered card
// must surface as an isError tool result, not a silent empty response.
func TestToolsCall_A2AInvoke_NoCard(t *testing.T) {
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"a2a.invoke","arguments":{"handle":"nope","text":"x"}}}` + "\n")
	raw, _ := handleLine(c, tools, line)
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &resp)
	if !resp.Result.IsError {
		t.Errorf("want isError=true for missing card, got %+v", resp.Result)
	}
}

// TestToolsCall_ProjectsUpdate: projects.update must PATCH the project URL
// with the fields to change; the `project` routing key must be stripped.
func TestToolsCall_ProjectsUpdate(t *testing.T) {
	var sawMethod, sawPath, sawBody string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		sawBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"p1","goal":"new goal"}`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":41,"method":"tools/call","params":{"name":"projects.update","arguments":{"project":"p1","goal":"new goal","budget_cents":5000}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawMethod != "PATCH" {
		t.Errorf("method = %q, want PATCH", sawMethod)
	}
	if sawPath != "/v1/teams/team-alpha/projects/p1" {
		t.Errorf("path = %q", sawPath)
	}
	if strings.Contains(sawBody, `"project"`) {
		t.Errorf("body should not include routing key: %q", sawBody)
	}
	if !strings.Contains(sawBody, `"goal":"new goal"`) {
		t.Errorf("body missing goal: %q", sawBody)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Result.IsError {
		t.Errorf("unexpected isError")
	}
}

// TestToolsCall_HostsUpdateSSHHint: hosts.update_ssh_hint must PATCH the
// host ssh_hint URL and serialize the ssh_hint object into a stringified
// ssh_hint_json body (that's what the REST handler expects).
func TestToolsCall_HostsUpdateSSHHint(t *testing.T) {
	var sawMethod, sawPath, sawBody string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		sawBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"hosts.update_ssh_hint","arguments":{"host":"h1","ssh_hint":{"user":"alice","port":22}}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawMethod != "PATCH" {
		t.Errorf("method = %q, want PATCH", sawMethod)
	}
	if sawPath != "/v1/teams/team-alpha/hosts/h1/ssh_hint" {
		t.Errorf("path = %q", sawPath)
	}
	// Body must contain ssh_hint_json as a STRING with the serialized object.
	if !strings.Contains(sawBody, `"ssh_hint_json"`) {
		t.Errorf("body missing ssh_hint_json key: %q", sawBody)
	}
	if !strings.Contains(sawBody, `\"user\":\"alice\"`) {
		t.Errorf("body should contain escaped stringified object, got: %q", sawBody)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Result.IsError {
		t.Errorf("unexpected isError")
	}
}

// TestToolsCall_HostsList: hosts.list must GET the team hosts URL and
// pass through the raw JSON array.
func TestToolsCall_HostsList(t *testing.T) {
	var sawMethod, sawPath string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"h1","name":"gpu-01","status":"online"}]`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":50,"method":"tools/call","params":{"name":"hosts.list","arguments":{}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawMethod != "GET" {
		t.Errorf("method = %q, want GET", sawMethod)
	}
	if sawPath != "/v1/teams/team-alpha/hosts" {
		t.Errorf("path = %q", sawPath)
	}
	if !strings.Contains(string(raw), `gpu-01`) {
		t.Errorf("expected host row passed through: %q", raw)
	}
}

// TestToolsCall_HostsGet: hosts.get must GET the host-id URL.
func TestToolsCall_HostsGet(t *testing.T) {
	var sawPath string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"h1","name":"gpu-01"}`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":51,"method":"tools/call","params":{"name":"hosts.get","arguments":{"host":"h1"}}}` + "\n")
	if _, ok := handleLine(c, tools, line); !ok {
		t.Fatalf("expected a response")
	}
	if sawPath != "/v1/teams/team-alpha/hosts/h1" {
		t.Errorf("path = %q", sawPath)
	}
}

// TestToolsCall_AgentsList: agents.list must pass host_id + status as
// query params, and include_archived only when true.
func TestToolsCall_AgentsList(t *testing.T) {
	var sawQuery string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":52,"method":"tools/call","params":{"name":"agents.list","arguments":{"host_id":"h1","status":"running","include_archived":true}}}` + "\n")
	if _, ok := handleLine(c, tools, line); !ok {
		t.Fatalf("expected a response")
	}
	for _, want := range []string{"host_id=h1", "status=running", "include_archived=1"} {
		if !strings.Contains(sawQuery, want) {
			t.Errorf("query missing %q: %q", want, sawQuery)
		}
	}
}

// TestToolsCall_AgentsGet: agents.get must GET the agent-id URL.
func TestToolsCall_AgentsGet(t *testing.T) {
	var sawPath string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"a1"}`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":53,"method":"tools/call","params":{"name":"agents.get","arguments":{"agent":"a1"}}}` + "\n")
	if _, ok := handleLine(c, tools, line); !ok {
		t.Fatalf("expected a response")
	}
	if sawPath != "/v1/teams/team-alpha/agents/a1" {
		t.Errorf("path = %q", sawPath)
	}
}

// TestToolsCall_AgentsTerminate: agents.terminate must PATCH the agent
// URL with {"status":"terminated"}.
func TestToolsCall_AgentsTerminate(t *testing.T) {
	var sawMethod, sawPath, sawBody string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		sawBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"a1","status":"terminated"}`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":54,"method":"tools/call","params":{"name":"agents.terminate","arguments":{"agent":"a1"}}}` + "\n")
	if _, ok := handleLine(c, tools, line); !ok {
		t.Fatalf("expected a response")
	}
	if sawMethod != "PATCH" {
		t.Errorf("method = %q, want PATCH", sawMethod)
	}
	if sawPath != "/v1/teams/team-alpha/agents/a1" {
		t.Errorf("path = %q", sawPath)
	}
	if !strings.Contains(sawBody, `"status":"terminated"`) {
		t.Errorf("body missing terminated status: %q", sawBody)
	}
}

// TestToolsCall_ProjectChannelsCreate: project_channels.create must POST
// to the project-scoped channels endpoint with just {"name": ...}.
func TestToolsCall_ProjectChannelsCreate(t *testing.T) {
	var sawMethod, sawPath, sawBody string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		sawBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ch-1","scope_kind":"project","name":"ops"}`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"project_channels.create","arguments":{"project_id":"p1","name":"ops"}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawMethod != "POST" {
		t.Errorf("method = %q, want POST", sawMethod)
	}
	if sawPath != "/v1/teams/team-alpha/projects/p1/channels" {
		t.Errorf("path = %q", sawPath)
	}
	if strings.Contains(sawBody, `"project_id"`) {
		t.Errorf("body should not include project_id: %q", sawBody)
	}
	if !strings.Contains(sawBody, `"name":"ops"`) {
		t.Errorf("body missing name: %q", sawBody)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Result.IsError {
		t.Errorf("unexpected isError")
	}
}

// TestToolsCall_TeamChannelsCreate: team_channels.create must POST to
// the team-scope /channels endpoint (no project prefix).
func TestToolsCall_TeamChannelsCreate(t *testing.T) {
	var sawPath string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ch-team","scope_kind":"team","name":"hub-meta"}`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"name":"team_channels.create","arguments":{"name":"hub-meta"}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawPath != "/v1/teams/team-alpha/channels" {
		t.Errorf("path = %q, want team-scope /channels", sawPath)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Result.IsError {
		t.Errorf("unexpected isError")
	}
}

// TestToolsCall_TasksCreate: tasks.create must POST to the project-scoped
// path and strip project_id from the body.
func TestToolsCall_TasksCreate(t *testing.T) {
	var sawMethod, sawPath, sawBody string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		sawBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"t-new","title":"do it"}`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"tasks.create","arguments":{"project_id":"p1","title":"do it","status":"todo"}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawMethod != "POST" {
		t.Errorf("method = %q, want POST", sawMethod)
	}
	if sawPath != "/v1/teams/team-alpha/projects/p1/tasks" {
		t.Errorf("path = %q", sawPath)
	}
	if strings.Contains(sawBody, `"project_id"`) {
		t.Errorf("body should not include project_id: %q", sawBody)
	}
	if !strings.Contains(sawBody, `"title":"do it"`) {
		t.Errorf("body missing title: %q", sawBody)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Result.IsError {
		t.Errorf("unexpected isError")
	}
}

// TestToolsCall_TasksUpdate: tasks.update must PATCH the project-scoped
// task URL and drop both routing keys from the body.
func TestToolsCall_TasksUpdate(t *testing.T) {
	var sawMethod, sawPath, sawBody string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		sawBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"tasks.update","arguments":{"project_id":"p1","task":"t1","status":"done"}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawMethod != "PATCH" {
		t.Errorf("method = %q, want PATCH", sawMethod)
	}
	if sawPath != "/v1/teams/team-alpha/projects/p1/tasks/t1" {
		t.Errorf("path = %q", sawPath)
	}
	if strings.Contains(sawBody, `"project_id"`) || strings.Contains(sawBody, `"task"`) {
		t.Errorf("body should not include routing keys: %q", sawBody)
	}
	if !strings.Contains(sawBody, `"status":"done"`) {
		t.Errorf("body missing status: %q", sawBody)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Result.IsError {
		t.Errorf("unexpected isError")
	}
}

// TestToolsCall_SchedulesRun: schedules.run must POST to
// /schedules/{id}/run and surface the hub's plan_id response as the tool
// result. Exercises the schedules surface end-to-end at protocol level.
func TestToolsCall_SchedulesRun(t *testing.T) {
	var sawMethod, sawPath string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"plan_id":"pl-123"}`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"schedules.run","arguments":{"schedule":"s-42"}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawMethod != "POST" {
		t.Errorf("method = %q, want POST", sawMethod)
	}
	if sawPath != "/v1/teams/team-alpha/schedules/s-42/run" {
		t.Errorf("path = %q", sawPath)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Result.IsError {
		t.Fatalf("unexpected isError: %+v", resp.Result.Content)
	}
	if len(resp.Result.Content) == 0 || !strings.Contains(resp.Result.Content[0].Text, `"plan_id":"pl-123"`) {
		t.Errorf("missing plan_id in result: %+v", resp.Result.Content)
	}
}

// TestToolsCall_SchedulesCreate: schedules.create must forward the full
// body to POST /schedules, including cron_expr and parameters_json.
func TestToolsCall_SchedulesCreate(t *testing.T) {
	var sawMethod, sawPath, sawBody string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		sawBody = string(b)
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"s-new","enabled":true}`))
	})
	tools := buildTools()
	line := []byte(`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"schedules.create","arguments":{"project_id":"p1","template_id":"t1","trigger_kind":"cron","cron_expr":"0 3 * * *"}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if sawMethod != "POST" {
		t.Errorf("method = %q, want POST", sawMethod)
	}
	if sawPath != "/v1/teams/team-alpha/schedules" {
		t.Errorf("path = %q", sawPath)
	}
	if !strings.Contains(sawBody, `"cron_expr":"0 3 * * *"`) {
		t.Errorf("body missing cron_expr: %q", sawBody)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Result.IsError {
		t.Errorf("unexpected isError")
	}
}

// TestInitialize_ReturnsServerInfo: a bare initialize call must return
// protocol version + serverInfo so MCP clients can complete the handshake.
func TestInitialize_ReturnsServerInfo(t *testing.T) {
	c := newHubClient("http://127.0.0.1:1", "", "team-alpha")
	tools := buildTools()
	raw, _ := handleLine(c, tools, []byte(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`+"\n"))
	var resp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Result.ServerInfo.Name != serverName {
		t.Errorf("name = %q", resp.Result.ServerInfo.Name)
	}
	if resp.Result.ProtocolVersion == "" {
		t.Errorf("missing protocolVersion")
	}
}

// TestNotification_NoResponse: a request without an id is a JSON-RPC
// notification; the server must process it but write nothing to stdout.
func TestNotification_NoResponse(t *testing.T) {
	c := newHubClient("http://127.0.0.1:1", "", "team-alpha")
	tools := buildTools()
	_, ok := handleLine(c, tools, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"))
	if ok {
		t.Errorf("expected no response for a notification")
	}
}
