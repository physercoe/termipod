package hostrunner

// egressProxy is the in-process reverse proxy that masks the hub URL
// from spawned agents. It binds 127.0.0.1:<port> on the host, and
// `.mcp.json` for each spawned agent points at this address instead of
// the real hub. The agent → bridge → proxy → hub chain works
// transparently; from the agent's perspective the hub is "the thing on
// localhost".
//
// What this hides: a passive `cat .mcp.json` or env-dump on the agent
// no longer reveals the public hub URL. What it does NOT hide: an
// agent that runs `ss -tnp`, `lsof -i`, or otherwise probes the host
// network state can still infer the real hub from host-runner's own
// outbound connections. Closing that gap requires an OS sandbox
// (network namespace + allowlist) — see docs/threat-model.md.
//
// The agent token itself is still in `.mcp.json`. Token-injection at
// the proxy (where the bridge would carry no secret) is deliberately
// out of scope for this wedge — it requires changing the bridge wire
// shape and is documented as a follow-up post-MVP.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// DefaultEgressProxyAddr is an uncommon 5-digit dynamic-range port
// chosen to avoid colliding with anything an operator is likely to
// already be running. The IANA dynamic/private range is 49152-65535;
// we pick from the unassigned span below it so the value is memorable
// and unlikely to be claimed by a future registration. Override with
// host-runner --egress-proxy-addr if 41825 conflicts on your host.
const DefaultEgressProxyAddr = "127.0.0.1:41825"

// egressProxy holds the bound listener + the URL we hand to agents.
// LocalURL is what callers should set on M2LaunchConfig.HubURL so the
// `.mcp.json` materialiser writes the masked address.
type egressProxy struct {
	LocalURL string
	listener net.Listener
	server   *http.Server
}

// startEgressProxy binds addr on 127.0.0.1 and spins up a reverse proxy
// to upstreamURL. Returns immediately once the listener is bound;
// caller invokes proxy.shutdown(ctx) at teardown. If addr is empty
// the proxy is disabled and a nil egressProxy is returned (callers
// should treat that as "no masking, use the upstream URL directly").
func startEgressProxy(ctx context.Context, addr, upstreamURL string, log *slog.Logger) (*egressProxy, error) {
	if addr == "" {
		return nil, nil
	}
	upstream, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream %q: %w", upstreamURL, err)
	}
	if upstream.Scheme == "" || upstream.Host == "" {
		return nil, fmt.Errorf("upstream %q must be an absolute URL", upstreamURL)
	}

	rp := httputil.NewSingleHostReverseProxy(upstream)
	// FlushInterval = -1 makes the reverse proxy flush every write
	// immediately. Required for SSE pass-through (agent_events stream)
	// — the default buffered behavior would hold frames in the proxy
	// until a write completes, which never happens for a long-lived
	// stream. The same setting is fine for short JSON responses; the
	// extra flushes are a no-op on small bodies.
	rp.FlushInterval = -1
	// The default Director sets Host to the upstream's. We keep that
	// behavior so the hub sees the same Host header it would from a
	// direct client. Authorization, cookies, and everything else flow
	// through unmodified.
	directorOrig := rp.Director
	rp.Director = func(req *http.Request) {
		directorOrig(req)
		// X-Forwarded-* lets the hub log "this came from the egress
		// proxy on host X" if it ever needs to disambiguate. Not used
		// today; cheap to set so future debugging is easier.
		req.Header.Set("X-Forwarded-Proto", upstream.Scheme)
		if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
			req.Header.Set("X-Forwarded-For", host)
		}
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Warn("egress-proxy upstream error",
			"path", r.URL.Path, "err", err.Error())
		http.Error(w, "egress proxy: upstream error: "+err.Error(),
			http.StatusBadGateway)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler: rp,
		// ReadHeaderTimeout exists to prevent slowloris on the loopback
		// listener; the deadline is generous because legitimate clients
		// (the bridge) are local and should always send headers fast.
		ReadHeaderTimeout: 10 * time.Second,
	}

	// LocalURL is what agents will see. Use the actual bound address
	// (handles addr=":0" → real port) and prefix http:// because the
	// proxy is plaintext on loopback (TLS would just be ceremony for
	// localhost, and the bridge wouldn't validate certs anyway).
	bound := ln.Addr().String()
	local := "http://" + bound
	// If the operator passed a hostname rather than an IP, preserve it
	// so the URL matches what they think they configured.
	if host, port, err := net.SplitHostPort(addr); err == nil && host != "" && !strings.Contains(host, ":") {
		// host is empty for ":NNNN" — only override when the operator
		// actually named one (e.g. "127.0.0.1:41825" or "::1:NNN").
		if _, listenedPort, err := net.SplitHostPort(bound); err == nil {
			if port == "0" {
				port = listenedPort
			}
			local = "http://" + net.JoinHostPort(host, port)
		}
	}

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Error("egress-proxy serve failed", "err", err)
		}
	}()
	log.Info("egress-proxy listening",
		"addr", bound, "upstream", upstream.String(), "local_url", local)

	return &egressProxy{LocalURL: local, listener: ln, server: srv}, nil
}

func (p *egressProxy) shutdown(ctx context.Context) error {
	if p == nil || p.server == nil {
		return nil
	}
	return p.server.Shutdown(ctx)
}
