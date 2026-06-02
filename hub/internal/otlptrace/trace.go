// Package otlptrace is a dependency-free OTLP/HTTP trace exporter.
//
// It deliberately avoids the OpenTelemetry Go SDK and the protobuf
// dependency tree: the hub re-emits *historical* spans reconstructed from
// stored rows (ADR-038 §4), with caller-chosen deterministic trace/span
// IDs and explicit start/end timestamps — a shape the live-instrumentation
// SDK fights rather than helps. We encode the OTLP `ExportTraceServiceRequest`
// in its JSON-protobuf form (a first-class part of the OTLP/HTTP spec) and
// POST it to `<endpoint>/v1/traces`. Jaeger's OTLP receiver and the
// OpenTelemetry Collector both accept this; protobuf-only backends sit
// behind a Collector.
//
// Two OTLP/JSON specifics the encoder gets right (both are easy to miss):
//   - trace_id / span_id are HEX strings, not the base64 that protojson uses
//     for `bytes` fields by default — the OTLP spec overrides protojson here.
//   - 64-bit timestamps are decimal STRINGS (protojson int64/uint64 rule).
package otlptrace

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SpanKind is the OTLP span-kind enum. The projection only emits INTERNAL
// spans (re-materialised turns/tools have no live client/server role).
type SpanKind int

const SpanKindInternal SpanKind = 1

// StatusCode is the OTLP status-code enum: 0 unset, 1 ok, 2 error.
type StatusCode int

const (
	StatusUnset StatusCode = 0
	StatusOK    StatusCode = 1
	StatusError StatusCode = 2
)

// Attr is a typed key/value attribute. Construct with the String/Int/Float/
// Bool helpers so the encoder can emit the correct OTLP value wrapper.
type Attr struct {
	Key string
	val any // string | int64 | float64 | bool
}

func String(k, v string) Attr        { return Attr{k, v} }
func Int(k string, v int64) Attr     { return Attr{k, v} }
func Float(k string, v float64) Attr { return Attr{k, v} }
func Bool(k string, v bool) Attr     { return Attr{k, v} }

// Status is a span's terminal status.
type Status struct {
	Code    StatusCode
	Message string
}

// Event is a point-in-time span event (used for exceptions).
type Event struct {
	Name     string
	TimeNano uint64
	Attrs    []Attr
}

// Span is one re-materialised span. IDs are fixed-width byte arrays so the
// caller controls them exactly (deterministic projection); a zero ParentID
// marks a root span.
type Span struct {
	TraceID   [16]byte
	SpanID    [8]byte
	ParentID  [8]byte
	Name      string
	Kind      SpanKind
	StartNano uint64
	EndNano   uint64
	Attrs     []Attr
	Status    Status
	Events    []Event
}

// Resource describes the producer; service.name is the one attribute every
// backend keys on.
type Resource struct {
	ServiceName string
}

const (
	scopeName    = "github.com/termipod/hub/internal/otlpexport"
	scopeVersion = "0.1.0"
)

// ---- wire structs (OTLP/HTTP JSON) ----

type wireValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
}

type wireAttr struct {
	Key   string    `json:"key"`
	Value wireValue `json:"value"`
}

type wireEvent struct {
	TimeUnixNano string     `json:"timeUnixNano"`
	Name         string     `json:"name"`
	Attributes   []wireAttr `json:"attributes,omitempty"`
}

type wireStatus struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type wireSpan struct {
	TraceID           string      `json:"traceId"`
	SpanID            string      `json:"spanId"`
	ParentSpanID      string      `json:"parentSpanId,omitempty"`
	Name              string      `json:"name"`
	Kind              int         `json:"kind"`
	StartTimeUnixNano string      `json:"startTimeUnixNano"`
	EndTimeUnixNano   string      `json:"endTimeUnixNano"`
	Attributes        []wireAttr  `json:"attributes,omitempty"`
	Status            wireStatus  `json:"status"`
	Events            []wireEvent `json:"events,omitempty"`
}

type wireScopeSpans struct {
	Scope struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"scope"`
	Spans []wireSpan `json:"spans"`
}

type wireResourceSpans struct {
	Resource struct {
		Attributes []wireAttr `json:"attributes"`
	} `json:"resource"`
	ScopeSpans []wireScopeSpans `json:"scopeSpans"`
}

type wireRequest struct {
	ResourceSpans []wireResourceSpans `json:"resourceSpans"`
}

func attrWire(a Attr) wireAttr {
	w := wireAttr{Key: a.Key}
	switch x := a.val.(type) {
	case string:
		w.Value.StringValue = &x
	case int64:
		s := strconv.FormatInt(x, 10)
		w.Value.IntValue = &s
	case float64:
		w.Value.DoubleValue = &x
	case bool:
		w.Value.BoolValue = &x
	default:
		// Unknown value type — emit an empty string rather than a malformed
		// wrapper, so one bad attribute can't poison the whole batch.
		empty := ""
		w.Value.StringValue = &empty
	}
	return w
}

func attrsWire(in []Attr) []wireAttr {
	if len(in) == 0 {
		return nil
	}
	out := make([]wireAttr, 0, len(in))
	for _, a := range in {
		out = append(out, attrWire(a))
	}
	return out
}

func spanIDIsZero(id [8]byte) bool {
	for _, b := range id {
		if b != 0 {
			return false
		}
	}
	return true
}

// Encode renders a batch of spans (possibly spanning many traces — each span
// carries its own trace_id) as an OTLP/HTTP JSON request body.
func Encode(res Resource, spans []Span) []byte {
	var rs wireResourceSpans
	if res.ServiceName != "" {
		rs.Resource.Attributes = []wireAttr{attrWire(String("service.name", res.ServiceName))}
	}
	ss := wireScopeSpans{}
	ss.Scope.Name = scopeName
	ss.Scope.Version = scopeVersion
	ss.Spans = make([]wireSpan, 0, len(spans))
	for _, sp := range spans {
		ws := wireSpan{
			TraceID:           hex.EncodeToString(sp.TraceID[:]),
			SpanID:            hex.EncodeToString(sp.SpanID[:]),
			Name:              sp.Name,
			Kind:              int(sp.Kind),
			StartTimeUnixNano: strconv.FormatUint(sp.StartNano, 10),
			EndTimeUnixNano:   strconv.FormatUint(sp.EndNano, 10),
			Attributes:        attrsWire(sp.Attrs),
			Status:            wireStatus{Code: int(sp.Status.Code), Message: sp.Status.Message},
		}
		if !spanIDIsZero(sp.ParentID) {
			ws.ParentSpanID = hex.EncodeToString(sp.ParentID[:])
		}
		for _, ev := range sp.Events {
			ws.Events = append(ws.Events, wireEvent{
				TimeUnixNano: strconv.FormatUint(ev.TimeNano, 10),
				Name:         ev.Name,
				Attributes:   attrsWire(ev.Attrs),
			})
		}
		ss.Spans = append(ss.Spans, ws)
	}
	rs.ScopeSpans = []wireScopeSpans{ss}
	body, _ := json.Marshal(wireRequest{ResourceSpans: []wireResourceSpans{rs}})
	return body
}

// Client posts encoded batches to an OTLP/HTTP endpoint.
type Client struct {
	// Endpoint is the OTLP/HTTP base URL, e.g. "http://localhost:4318".
	// The exporter appends "/v1/traces".
	Endpoint string
	Resource Resource
	HTTP     *http.Client
	// Headers are sent on every request (e.g. an auth bearer for a hosted
	// backend like Phoenix). Optional.
	Headers map[string]string
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// TracesURL is the resolved POST target — exposed for tests/logging.
func (c *Client) TracesURL() string {
	return strings.TrimRight(c.Endpoint, "/") + "/v1/traces"
}

// Export POSTs the spans. A nil/empty batch is a no-op. Non-2xx responses
// return an error carrying the status and a body snippet for diagnosis.
func (c *Client) Export(ctx context.Context, spans []Span) error {
	if len(spans) == 0 {
		return nil
	}
	body := Encode(c.Resource, spans)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TracesURL(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("otlp export: %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
