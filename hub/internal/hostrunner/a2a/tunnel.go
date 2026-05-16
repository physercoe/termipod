package a2a

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
)

// TunnelClient is the minimal client-side shape the tunnel loop needs.
// Decouples this package from the concrete hostrunner.Client and keeps
// the loop easy to test with a fake.
type TunnelClient interface {
	NextTunnelRequest(ctx context.Context, hostID string, waitMs int) (*TunnelEnvelope, error)
	PostTunnelResponse(ctx context.Context, hostID string, env *TunnelResponseEnvelope) error
}

// TunnelEnvelope mirrors the host-runner client's wire type. Redeclared
// here to keep the a2a package free of an upstream dep.
//
// Kind discriminates two traffic classes on the same tunnel (ADR-028 D-1):
//   - "" or "a2a" → A2A relay, dispatched through the local http.Handler.
//   - "host.<verb>" → control-plane verb, dispatched through HostVerbHandler.
//
// For Kind=="host.*" the relay fields (Method/Path/Headers/BodyB64) are
// unused; verb args ride in Payload as opaque JSON.
type TunnelEnvelope struct {
	ReqID    string            `json:"req_id"`
	Kind     string            `json:"kind,omitempty"`
	Method   string            `json:"method,omitempty"`
	Path     string            `json:"path,omitempty"`
	RawQuery string            `json:"raw_query,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	BodyB64  string            `json:"body_b64,omitempty"`
	Payload  json.RawMessage   `json:"payload,omitempty"`
}

// HostVerbHandler dispatches a host.<verb> envelope to its local handler
// and returns the canonical response envelope. Implementations should
// never block longer than a few seconds — verbs like host.shutdown ack
// synchronously and then SIGTERM after the response is posted.
//
// Returning nil tells the loop to emit the typed unknown_verb response.
type HostVerbHandler func(ctx context.Context, env *TunnelEnvelope) *TunnelResponseEnvelope

type TunnelResponseEnvelope struct {
	ReqID   string            `json:"req_id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	BodyB64 string            `json:"body_b64,omitempty"`
}

// RunTunnel loops forever (until ctx cancels) long-polling the hub for
// queued envelopes, routing each by Kind, and posting the resulting
// response back.
//
//   - Kind == "" or "a2a" → dispatched through handler (typically the
//     host-runner's own a2a.Server.Handler()) so relayed calls land on
//     the exact same routes a direct peer would hit.
//   - Kind == "host.<verb>" → dispatched through verbs. If verbs is nil
//     or returns nil, the loop emits a typed unknown_verb response.
//
// Errors are logged and retried with a small backoff; the loop never
// exits on transient failures. Only ctx.Done() terminates it.
//
// hostVersion is stamped into the unknown_verb response body so the
// orchestrator can report "this host is too old for this verb" cleanly.
// Pass an empty string to omit the field.
func RunTunnel(ctx context.Context, cli TunnelClient, hostID string, handler http.Handler, verbs HostVerbHandler, hostVersion string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	const waitMs = 25_000
	backoff := 500 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return
		}
		req, err := cli.NextTunnelRequest(ctx, hostID, waitMs)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn("tunnel next failed", "err", err)
			sleepOrDone(ctx, backoff)
			if backoff < 10*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 500 * time.Millisecond
		if req == nil {
			continue
		}
		var resp *TunnelResponseEnvelope
		switch {
		case req.Kind == "" || req.Kind == "a2a":
			resp = dispatchLocal(ctx, handler, req)
		case strings.HasPrefix(req.Kind, "host."):
			verb := strings.TrimPrefix(req.Kind, "host.")
			if verbs != nil {
				resp = verbs(ctx, req)
			}
			if resp == nil {
				resp = unknownVerbEnvelope(req.ReqID, verb, hostVersion)
			}
		default:
			resp = unknownVerbEnvelope(req.ReqID, req.Kind, hostVersion)
		}
		if err := cli.PostTunnelResponse(ctx, hostID, resp); err != nil {
			log.Warn("tunnel response post failed", "req_id", req.ReqID, "err", err)
		}
	}
}

// unknownVerbEnvelope returns the typed 400 response per ADR-028 D-1.
// Callers (the dispatcher loop, or a HostVerbHandler that recognises
// the kind prefix but not the specific verb) can construct it directly.
func unknownVerbEnvelope(reqID, verb, hostVersion string) *TunnelResponseEnvelope {
	body := map[string]any{"error": "unknown_verb", "verb": verb}
	if hostVersion != "" {
		body["host_version"] = hostVersion
	}
	b, _ := json.Marshal(body)
	return &TunnelResponseEnvelope{
		ReqID:   reqID,
		Status:  http.StatusBadRequest,
		Headers: map[string]string{"Content-Type": "application/json"},
		BodyB64: base64.StdEncoding.EncodeToString(b),
	}
}

// dispatchLocal runs the envelope through handler with a synthetic
// *http.Request and captures the response via httptest.ResponseRecorder.
// Any decoding error short-circuits to a 502 so the hub surfaces it
// rather than hanging.
func dispatchLocal(ctx context.Context, handler http.Handler, env *TunnelEnvelope) *TunnelResponseEnvelope {
	body, err := base64.StdEncoding.DecodeString(env.BodyB64)
	if err != nil {
		return errEnvelope(env.ReqID, http.StatusBadGateway, "bad body_b64: "+err.Error())
	}
	u := &url.URL{Path: env.Path, RawQuery: env.RawQuery}
	method := env.Method
	if method == "" {
		method = http.MethodGet
	}
	r, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return errEnvelope(env.ReqID, http.StatusBadGateway, "synthetic request: "+err.Error())
	}
	for k, v := range env.Headers {
		r.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	outHeaders := map[string]string{}
	for k, vs := range rr.Header() {
		if len(vs) > 0 {
			outHeaders[k] = vs[0]
		}
	}
	outBody, _ := io.ReadAll(rr.Body)
	return &TunnelResponseEnvelope{
		ReqID:   env.ReqID,
		Status:  rr.Code,
		Headers: outHeaders,
		BodyB64: base64.StdEncoding.EncodeToString(outBody),
	}
}

func errEnvelope(reqID string, status int, msg string) *TunnelResponseEnvelope {
	return &TunnelResponseEnvelope{
		ReqID:   reqID,
		Status:  status,
		Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
		BodyB64: base64.StdEncoding.EncodeToString([]byte(msg)),
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
