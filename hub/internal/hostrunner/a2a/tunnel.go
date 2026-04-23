package a2a

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
type TunnelEnvelope struct {
	ReqID    string            `json:"req_id"`
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	RawQuery string            `json:"raw_query,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	BodyB64  string            `json:"body_b64,omitempty"`
}

type TunnelResponseEnvelope struct {
	ReqID   string            `json:"req_id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	BodyB64 string            `json:"body_b64,omitempty"`
}

// RunTunnel loops forever (until ctx cancels) long-polling the hub for
// queued A2A requests, dispatching them through the supplied handler,
// and posting the resulting response back. The handler is typically the
// host-runner's own a2a.Server.Handler(), so relayed calls land on the
// exact same routes a direct peer would hit.
//
// Errors are logged and retried with a small backoff; the loop never
// exits on transient failures. Only ctx.Done() terminates it.
func RunTunnel(ctx context.Context, cli TunnelClient, hostID string, handler http.Handler, log *slog.Logger) {
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
		resp := dispatchLocal(ctx, handler, req)
		if err := cli.PostTunnelResponse(ctx, hostID, resp); err != nil {
			log.Warn("tunnel response post failed", "req_id", req.ReqID, "err", err)
		}
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
