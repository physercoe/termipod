package otlptrace

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func sampleSpans() []Span {
	tr := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	parent := [8]byte{1, 1, 1, 1, 1, 1, 1, 1}
	child := [8]byte{2, 2, 2, 2, 2, 2, 2, 2}
	return []Span{
		{
			TraceID:   tr,
			SpanID:    parent,
			Name:      "turn 0",
			Kind:      SpanKindInternal,
			StartNano: 1000,
			EndNano:   5000,
			Attrs: []Attr{
				String("gen_ai.system", "claude-code"),
				Int("gen_ai.usage.input_tokens", 120),
				Float("cost_usd", 0.0033),
				Bool("termipod.turn.open", false),
			},
			Status: Status{Code: StatusOK},
		},
		{
			TraceID:   tr,
			SpanID:    child,
			ParentID:  parent,
			Name:      "bash",
			Kind:      SpanKindInternal,
			StartNano: 1500,
			EndNano:   2200,
			Status:    Status{Code: StatusError, Message: "exit 1"},
			Events: []Event{
				{Name: "exception", TimeNano: 2200, Attrs: []Attr{String("exception.type", "tool_error")}},
			},
		},
	}
}

// The OTLP/JSON wire form has two easy-to-botch rules: IDs are hex (not
// base64) and 64-bit numbers are decimal strings. Lock both, plus the value
// wrappers and parent linkage.
func TestEncode_WireShape(t *testing.T) {
	body := Encode(Resource{ServiceName: "termipod-hub"}, sampleSpans())

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("encoded body is not valid JSON: %v\n%s", err, body)
	}
	rs := got["resourceSpans"].([]any)[0].(map[string]any)
	spans := rs["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
	if len(spans) != 2 {
		t.Fatalf("want 2 spans, got %d", len(spans))
	}
	turn := spans[0].(map[string]any)
	tool := spans[1].(map[string]any)

	// Hex IDs (16 bytes -> 32 chars; 8 bytes -> 16 chars), not base64.
	if id := turn["traceId"].(string); id != "0102030405060708090a0b0c0d0e0f10" {
		t.Fatalf("traceId = %q, want lowercase hex", id)
	}
	if id := turn["spanId"].(string); id != "0101010101010101" {
		t.Fatalf("spanId = %q, want hex", id)
	}
	// Root span omits parentSpanId; child carries the parent's hex id.
	if _, present := turn["parentSpanId"]; present {
		t.Fatalf("root turn span should omit parentSpanId, got %v", turn["parentSpanId"])
	}
	if got := tool["parentSpanId"].(string); got != "0101010101010101" {
		t.Fatalf("tool parentSpanId = %q, want the turn's span id", got)
	}

	// Timestamps are decimal strings.
	if ts := turn["startTimeUnixNano"]; ts != "1000" {
		t.Fatalf("startTimeUnixNano = %v (%T), want the string \"1000\"", ts, ts)
	}

	// Attribute value wrappers by type.
	attrs := turn["attributes"].([]any)
	byKey := map[string]map[string]any{}
	for _, a := range attrs {
		m := a.(map[string]any)
		byKey[m["key"].(string)] = m["value"].(map[string]any)
	}
	if v := byKey["gen_ai.system"]["stringValue"]; v != "claude-code" {
		t.Fatalf("gen_ai.system stringValue = %v", v)
	}
	if v := byKey["gen_ai.usage.input_tokens"]["intValue"]; v != "120" {
		t.Fatalf("intValue should be the decimal string \"120\", got %v (%T)", v, v)
	}
	if _, ok := byKey["cost_usd"]["doubleValue"]; !ok {
		t.Fatalf("cost_usd should use doubleValue, got %v", byKey["cost_usd"])
	}
	if v := byKey["termipod.turn.open"]["boolValue"]; v != false {
		t.Fatalf("boolValue = %v", v)
	}

	// Error status + exception event on the tool span.
	if code := tool["status"].(map[string]any)["code"]; code != float64(StatusError) {
		t.Fatalf("tool status code = %v, want %d", code, StatusError)
	}
	ev := tool["events"].([]any)[0].(map[string]any)
	if ev["name"] != "exception" || ev["timeUnixNano"] != "2200" {
		t.Fatalf("unexpected exception event: %v", ev)
	}
}

func TestExport_PostsToTracesPathWithJSONContentType(t *testing.T) {
	var gotPath, gotCT, gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Client{
		Endpoint: srv.URL + "/", // trailing slash must be trimmed, not doubled
		Resource: Resource{ServiceName: "termipod-hub"},
		Headers:  map[string]string{"Authorization": "Bearer t"},
	}
	if err := c.Export(context.Background(), sampleSpans()); err != nil {
		t.Fatalf("export: %v", err)
	}
	if gotPath != "/v1/traces" {
		t.Fatalf("POST path = %q, want /v1/traces", gotPath)
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q", gotCT)
	}
	if gotAuth != "Bearer t" {
		t.Fatalf("custom header not sent: %q", gotAuth)
	}
	if !json.Valid(gotBody) {
		t.Fatalf("server received invalid JSON: %s", gotBody)
	}
}

func TestExport_EmptyIsNoop(t *testing.T) {
	c := &Client{Endpoint: "http://127.0.0.1:1"} // would fail if it dialed
	if err := c.Export(context.Background(), nil); err != nil {
		t.Fatalf("empty export should be a no-op, got %v", err)
	}
}

func TestExport_Non2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad span"))
	}))
	defer srv.Close()
	c := &Client{Endpoint: srv.URL}
	if err := c.Export(context.Background(), sampleSpans()); err == nil {
		t.Fatal("expected an error on 400")
	}
}
