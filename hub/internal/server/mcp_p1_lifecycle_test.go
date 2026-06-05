package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/termipod/hub/internal/hubmcpserver"
)

// mcp_p1_lifecycle_test.go — ADR-044 P1. The deliverable / criterion /
// phase MCP affordance: agents can now read the lifecycle and materialize
// their own deliverables + mark text/metric criteria, all over the MCP
// tools/call edge (not REST-only). Reuses phaseTestSetup + authedJSON from
// the W5b deliverables suite and the /mcp/{token} round-trip shape from
// mcp_authority_test.go.

// TestMCP_P1_LifecycleTools_CatalogContract pins the registry side: every
// new P1 tool is registered (so it dispatches), worker-eligible (so a
// project agent may call it — authorizeMCPCall grants on WorkerEligible),
// and the reads/writes carry the right ReadOnly flag.
func TestMCP_P1_LifecycleTools_CatalogContract(t *testing.T) {
	reads := []string{"deliverables_list", "deliverables_get", "criteria_list", "phase_status"}
	writes := []string{
		"deliverables_add_component", "deliverables_remove_component",
		"deliverables_set_state", "criteria_set_state",
	}
	for _, name := range append(append([]string{}, reads...), writes...) {
		spec, ok, _ := hubmcpserver.LookupToolSpec(name)
		if !ok {
			t.Errorf("P1 tool %q not in the unified registry — agents can't see it", name)
			continue
		}
		if !spec.WorkerEligible {
			t.Errorf("P1 tool %q must be WorkerEligible (a project agent has to call it)", name)
		}
		if spec.Backend == "" {
			t.Errorf("P1 tool %q has no Backend — dispatch can't route it", name)
		}
	}
	for _, name := range reads {
		if spec, ok, _ := hubmcpserver.LookupToolSpec(name); ok && !spec.ReadOnly {
			t.Errorf("read tool %q must be ReadOnly (concurrency_safe)", name)
		}
	}
	for _, name := range writes {
		if spec, ok, _ := hubmcpserver.LookupToolSpec(name); ok && spec.ReadOnly {
			t.Errorf("write tool %q must not be ReadOnly", name)
		}
	}
}

// mcpCall drives one tools/call over the /mcp/{token} endpoint and returns
// the decoded result map (or fails on a JSON-RPC error unless wantErr).
func mcpCall(t *testing.T, url, token, name string, args map[string]any, wantErr bool) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST",
		url+"/mcp/"+token, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var out struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s decode: %v (%s)", name, err, raw)
	}
	// A tool failure can surface on either channel: a protocol-level
	// JSON-RPC error (authority dispatch maps an upstream 4xx to one), or
	// a recoverable tool result flagged isError. The gate-criterion refusal
	// (REST 403) arrives as the former. Treat both as "the call errored".
	isErr, _ := out.Result["isError"].(bool)
	errored := out.Error != nil || isErr
	if wantErr && !errored {
		t.Fatalf("%s expected an error, got success: %s", name, raw)
	}
	if !wantErr && errored {
		t.Fatalf("%s unexpected error: %s", name, raw)
	}
	return out.Result
}

// TestMCP_P1_LifecycleTools_RoundTrip drives the full P1 surface through
// the MCP edge against an in-process hub: read the phase, list/get
// deliverables + criteria, attach/remove a component, submit the
// deliverable for review, mark a text criterion met, and confirm a gate
// criterion refuses a manual mark.
func TestMCP_P1_LifecycleTools_RoundTrip(t *testing.T) {
	phases := []string{"idea", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)

	base := "/v1/teams/" + team + "/projects/" + project

	// Seed a deliverable in the active phase + a text and a gate criterion
	// via REST (the director's authoring path).
	delRR := authedJSON(t, s, http.MethodPost, tok, base+"/deliverables",
		map[string]any{"phase": "idea", "kind": "scope-doc", "required": true})
	if delRR.Code != http.StatusCreated {
		t.Fatalf("seed deliverable: %d %s", delRR.Code, delRR.Body.String())
	}
	var del deliverableOut
	_ = json.Unmarshal(delRR.Body.Bytes(), &del)

	textRR := authedJSON(t, s, http.MethodPost, tok, base+"/criteria",
		map[string]any{"phase": "idea", "kind": "text",
			"body": map[string]any{"text": "scope is bounded"}, "required": true})
	if textRR.Code != http.StatusCreated {
		t.Fatalf("seed text criterion: %d %s", textRR.Code, textRR.Body.String())
	}
	var textCrit criterionOut
	_ = json.Unmarshal(textRR.Body.Bytes(), &textCrit)

	gateRR := authedJSON(t, s, http.MethodPost, tok, base+"/criteria",
		map[string]any{"phase": "idea", "kind": "gate", "deliverable_id": del.ID,
			"body": map[string]any{"gate": "deliverable.ratified"}})
	if gateRR.Code != http.StatusCreated {
		t.Fatalf("seed gate criterion: %d %s", gateRR.Code, gateRR.Body.String())
	}
	var gateCrit criterionOut
	_ = json.Unmarshal(gateRR.Body.Bytes(), &gateCrit)

	// --- reads ---
	phase := mcpCall(t, srv.URL, tok, "phase_status", map[string]any{"project": project}, false)
	if got := decodeContent(t, phase)["phase"]; got != "idea" {
		t.Errorf("phase_status phase=%v want=idea", got)
	}

	delList := decodeContent(t, mcpCall(t, srv.URL, tok, "deliverables_list",
		map[string]any{"project": project}, false))
	if items, _ := delList["items"].([]any); len(items) != 1 {
		t.Errorf("deliverables_list items=%v want=1", delList["items"])
	}

	critList := decodeContent(t, mcpCall(t, srv.URL, tok, "criteria_list",
		map[string]any{"project": project}, false))
	if items, _ := critList["items"].([]any); len(items) != 2 {
		t.Errorf("criteria_list items=%d want=2", len(items))
	}

	// --- materialize: attach a component, then read it back, then remove ---
	comp := decodeContent(t, mcpCall(t, srv.URL, tok, "deliverables_add_component",
		map[string]any{"project": project, "deliverable": del.ID,
			"kind": "document", "ref_id": "doc-scope-1"}, false))
	compID, _ := comp["id"].(string)
	if compID == "" {
		t.Fatalf("add_component returned no id: %v", comp)
	}
	got := decodeContent(t, mcpCall(t, srv.URL, tok, "deliverables_get",
		map[string]any{"project": project, "deliverable": del.ID}, false))
	if comps, _ := got["components"].([]any); len(comps) != 1 {
		t.Errorf("deliverables_get components=%v want=1 after attach", got["components"])
	}
	mcpCall(t, srv.URL, tok, "deliverables_remove_component",
		map[string]any{"project": project, "deliverable": del.ID, "component": compID}, false)

	// --- submit for review ---
	setState := decodeContent(t, mcpCall(t, srv.URL, tok, "deliverables_set_state",
		map[string]any{"project": project, "deliverable": del.ID, "state": "in-review"}, false))
	if setState["ratification_state"] != "in-review" {
		t.Errorf("set_state ratification_state=%v want=in-review", setState["ratification_state"])
	}

	// --- mark the text criterion met (agent's signal path) ---
	marked := decodeContent(t, mcpCall(t, srv.URL, tok, "criteria_set_state",
		map[string]any{"project": project, "criterion": textCrit.ID, "state": "met",
			"evidence_ref": "document://doc-scope-1"}, false))
	if marked["state"] != "met" {
		t.Errorf("criteria_set_state state=%v want=met", marked["state"])
	}

	// --- a gate criterion refuses a manual mark (chassis-evaluated) ---
	mcpCall(t, srv.URL, tok, "criteria_set_state",
		map[string]any{"project": project, "criterion": gateCrit.ID, "state": "met"}, true)
}

// decodeContent pulls the JSON object an MCP tool returned out of the
// result envelope. Tool results arrive as {"content":[{"type":"text",
// "text":"<json>"}]}; this unwraps the first text item and parses it.
func decodeContent(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("result has no content: %v", result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	var obj map[string]any
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		t.Fatalf("decode content text %q: %v", text, err)
	}
	return obj
}
