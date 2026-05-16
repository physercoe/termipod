package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/termipod/hub/internal/auth"
)

// P3.3b — A2A reverse tunnel.
//
// Typical GPU hosts are NAT'd — only the hub has a public IP. A2A peers
// therefore cannot dial a host-runner directly. Instead the hub exposes
// /a2a/relay/<host>/<agent>/* as the publicly visible A2A surface, and
// each host-runner opens an outbound long-poll on /v1/.../a2a/tunnel/next
// to pick up queued requests, dispatch them locally, and POST the response
// back to /v1/.../a2a/tunnel/responses.
//
// Transport chosen: HTTP long-poll (stdlib, no new deps, matches the
// existing ListPendingSpawns cadence). A2A requests aren't especially
// latency-sensitive and the reconnect penalty between polls is <100ms.
//
// State is in-memory: on hub restart any in-flight requests fail, which
// is acceptable — the steward retries at the A2A layer.

// TunnelManager brokers A2A requests between the hub's public relay
// endpoint and each host-runner's outbound poll loop. Safe for concurrent
// use; zero value is ready.
type TunnelManager struct {
	mu       sync.Mutex
	requests map[string]chan *tunnelRequest  // host_id → queue of pending requests
	pending  map[string]chan *tunnelResponse // req_id → response waiter
	// metrics is the W3 throughput counter — see relay_metrics.go.
	// Always non-nil; absence is signalled from the stats handler by
	// omitting the block when no traffic was observed.
	metrics *RelayMetrics
}

// tunnelRequest is one queued A2A request awaiting a host-runner dispatch.
type tunnelRequest struct {
	ReqID   string            `json:"req_id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`            // the /a2a/<agent>/... tail, i.e. what the local A2A handler sees
	RawQuery string           `json:"raw_query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	BodyB64 string            `json:"body_b64,omitempty"`
}

// tunnelResponse is the host-runner's reply, addressed by ReqID.
type tunnelResponse struct {
	ReqID   string            `json:"req_id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	BodyB64 string            `json:"body_b64,omitempty"`
}

// newTunnelManager is used in tests; Server wires its own on startup.
func newTunnelManager() *TunnelManager {
	return &TunnelManager{
		requests: map[string]chan *tunnelRequest{},
		pending:  map[string]chan *tunnelResponse{},
		metrics:  NewRelayMetrics(),
	}
}

// queueFor returns (possibly creating) the per-host request queue. Queue
// depth caps outstanding dispatches per host; at steady state the loop
// drains within the long-poll interval so 16 is ample.
func (m *TunnelManager) queueFor(hostID string) chan *tunnelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.requests[hostID]
	if !ok {
		q = make(chan *tunnelRequest, 16)
		m.requests[hostID] = q
	}
	return q
}

// enqueueAndWait pushes req onto the host's queue, registers the waiter,
// and blocks until a response arrives or ctx is done. The caller owns the
// req_id (typically a ULID).
func (m *TunnelManager) enqueueAndWait(ctx context.Context, hostID string, req *tunnelRequest) (*tunnelResponse, error) {
	q := m.queueFor(hostID)

	waiter := make(chan *tunnelResponse, 1)
	m.mu.Lock()
	m.pending[req.ReqID] = waiter
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.pending, req.ReqID)
		m.mu.Unlock()
	}()

	select {
	case q <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case resp := <-waiter:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// nextForHost blocks for up to wait on the host's queue. Returns nil on
// timeout, which the handler translates to 204 No Content so host-runners
// immediately reconnect with a fresh poll.
func (m *TunnelManager) nextForHost(ctx context.Context, hostID string, wait time.Duration) (*tunnelRequest, error) {
	q := m.queueFor(hostID)
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case req := <-q:
		return req, nil
	case <-t.C:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// deliverResponse routes a reply to the pending waiter. Returns
// errNoPending when no one is waiting — usually means the caller timed
// out before the host got around to dispatching.
func (m *TunnelManager) deliverResponse(resp *tunnelResponse) error {
	m.mu.Lock()
	w, ok := m.pending[resp.ReqID]
	m.mu.Unlock()
	if !ok {
		return errNoPending
	}
	select {
	case w <- resp:
		return nil
	default:
		// Waiter slot is buffered-1, so a second delivery is a bug.
		return errDuplicateResponse
	}
}

var (
	errNoPending         = errors.New("no pending request for that req_id")
	errDuplicateResponse = errors.New("duplicate tunnel response")
)

// ----- HTTP handlers -----

// handleTunnelNext is the host-runner long-poll. Waits up to ~25s for a
// queued request on this host. Returns the envelope JSON or 204.
func (s *Server) handleTunnelNext(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r,"team")
	host := chi.URLParam(r,"host")
	if ok, err := s.authorizeHostInTeam(r.Context(), team, host); !ok {
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeErr(w, http.StatusNotFound, "host not found in team")
		return
	}

	wait := 25 * time.Second
	if v := r.URL.Query().Get("wait_ms"); v != "" {
		if ms, err := parsePositiveInt(v); err == nil && ms > 0 && ms <= 60_000 {
			wait = time.Duration(ms) * time.Millisecond
		}
	}

	req, err := s.tunnel.nextForHost(r.Context(), host, wait)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// Client went away; no response body needed.
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// handleTunnelResponse is the host-runner POST-back. Body is a
// tunnelResponse with the ReqID that came from /next. Hub routes it to
// the public-relay request that's still blocked on the waiter channel.
func (s *Server) handleTunnelResponse(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r,"team")
	host := chi.URLParam(r,"host")
	if ok, err := s.authorizeHostInTeam(r.Context(), team, host); !ok {
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeErr(w, http.StatusNotFound, "host not found in team")
		return
	}

	var resp tunnelResponse
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	if resp.ReqID == "" {
		writeErr(w, http.StatusBadRequest, "req_id required")
		return
	}
	if err := s.tunnel.deliverResponse(&resp); err != nil {
		// 410 Gone captures "the waiter is no longer there" better than 404.
		writeErr(w, http.StatusGone, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delivered": true})
}

// handleRelay is the public A2A entry point. Not authed — A2A peer calls
// are token-less per spec. Mounted under /a2a/relay/{host}/{agent}/* ;
// the handler forwards /a2a/<agent>/<tail> to the host-runner's local
// A2A server via the tunnel, which knows how to dispatch that path.
func (s *Server) handleRelay(w http.ResponseWriter, r *http.Request) {
	host := chi.URLParam(r,"host")
	agent := chi.URLParam(r,"agent")
	tail := chi.URLParam(r,"*")

	if host == "" || agent == "" {
		writeErr(w, http.StatusBadRequest, "host and agent required")
		return
	}

	// Cap request body to keep memory bounded.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := readAllCapped(r)
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}

	// Reconstruct the path the host-runner's A2A handler expects:
	// /a2a/<agent_id>[/<tail>]
	localPath := "/a2a/" + agent
	if tail != "" {
		localPath += "/" + tail
	}

	req := &tunnelRequest{
		ReqID:    NewID(),
		Method:   r.Method,
		Path:     localPath,
		RawQuery: r.URL.RawQuery,
		Headers:  map[string]string{},
		BodyB64:  base64.StdEncoding.EncodeToString(body),
	}
	for k, vs := range r.Header {
		if len(vs) == 0 {
			continue
		}
		// Drop hop-by-hop headers; forward everything else as-is.
		switch k {
		case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
			"Te", "Trailer", "Transfer-Encoding", "Upgrade":
			continue
		}
		req.Headers[k] = vs[0]
	}

	// Overall deadline for the relay round-trip. Tighter than the poll
	// wait so a stuck host returns 504 cleanly.
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// Stats accounting (insights-phase-1 W3) — counted around the
	// enqueue→wait so the gauge reflects actual hub-side time spent on
	// this round trip, including the host-runner's processing latency.
	releaseActive := s.tunnel.metrics.Begin()
	defer releaseActive()

	resp, err := s.tunnel.enqueueAndWait(ctx, host, req)
	if err != nil {
		s.tunnel.metrics.Dropped()
		if errors.Is(err, context.DeadlineExceeded) {
			writeErr(w, http.StatusGatewayTimeout, "host-runner did not respond in time")
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}

	for k, v := range resp.Headers {
		// Don't let the host override hop-by-hop / transport-layer headers.
		switch k {
		case "Content-Length", "Transfer-Encoding", "Connection":
			continue
		}
		w.Header().Set(k, v)
	}
	bodyBytes, err := base64.StdEncoding.DecodeString(resp.BodyB64)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "host response body not base64: "+err.Error())
		return
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	// Record the round-trip's byte volume against the destination pair
	// before writing the response — ensures the metrics tick before the
	// client closes its end and a quick burst of subsequent calls.
	s.tunnel.metrics.Record(host, agent, int64(len(body))+int64(len(bodyBytes)))
	// Hub-side audit row for the A2A message — closes the
	// "no audit trail outside engine JSONL" gap surfaced by the
	// 2026-05-16 audit. Best-effort: relay is unauthed, so a missing /
	// invalid bearer leaves actor_kind='peer' and from_agent_id empty
	// in meta. Only fired when the upstream returned a 2xx — failed
	// relays are tracked via tunnel.metrics.Dropped() above.
	if status < 400 {
		s.recordA2ARelayAudit(r, body, host, agent)
	}
	w.WriteHeader(status)
	_, _ = w.Write(bodyBytes)
}

// recordA2ARelayAudit writes a hub-side audit row for one successful
// A2A relay. Best-effort — never affects relay outcome.
func (s *Server) recordA2ARelayAudit(r *http.Request, body []byte, hostID, recvAgentID string) {
	// Resolve team via the receiving agent — the relay URL doesn't
	// carry a team in the path, but the agents table is the source
	// of truth for which team a given agent belongs to.
	var teamID string
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT team_id FROM agents WHERE id = ?`, recvAgentID).Scan(&teamID); err != nil {
		return // no team → no audit row; the agent_id was bogus.
	}
	// Optional sender attribution: client may forward the same bearer
	// it uses for authed endpoints (see doAbsolute). When present we
	// resolve it to actor_handle/agent_id for the row; when absent
	// the row records actor_kind='peer'.
	var (
		actorKind   = "peer"
		actorHandle string
		fromAgent   string
	)
	if tok, _ := auth.ResolveBearer(r.Context(), s.db, r); tok != nil {
		actorKind = tok.Kind
		var scope struct {
			AgentID string `json:"agent_id"`
			Handle  string `json:"handle"`
		}
		if err := json.Unmarshal([]byte(tok.ScopeJSON), &scope); err == nil {
			fromAgent = scope.AgentID
			actorHandle = strings.TrimPrefix(scope.Handle, "@")
		}
	}
	// Extract a short body preview (JSON-RPC message text) without
	// blowing the audit row up. A2A v0.3 envelopes carry
	// params.message.parts[].text — we grab the first text part and
	// truncate to 200 chars.
	preview := previewA2ABody(body)
	// Best-effort receiver handle lookup for the summary line.
	var recvHandle string
	_ = s.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(handle, '') FROM agents WHERE id = ?`, recvAgentID).Scan(&recvHandle)
	summary := "a2a → " + recvHandle
	if preview != "" {
		summary += ": " + preview
	}
	meta := map[string]any{
		"host_id":           hostID,
		"recv_agent_id":     recvAgentID,
		"recv_agent_handle": recvHandle,
		"body_preview":      preview,
		"body_bytes":        len(body),
	}
	if fromAgent != "" {
		meta["from_agent_id"] = fromAgent
	}
	if actorHandle != "" {
		meta["from_agent_handle"] = actorHandle
	}
	// Bypass recordAudit's actorFromContext (no auth ctx on this
	// route) and stamp the resolved values directly.
	metaJSON := "{}"
	if b, err := json.Marshal(meta); err == nil {
		metaJSON = string(b)
	}
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO audit_events (
			id, team_id, ts, actor_token_id, actor_kind, actor_handle,
			action, target_kind, target_id, summary, meta_json
		) VALUES (?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?)`,
		NewID(), teamID, NowUTC(), actorKind, nullIfEmpty(actorHandle),
		"a2a.message_sent", "agent", recvAgentID, summary, metaJSON)
	if err != nil {
		s.log.Warn("a2a audit insert", "err", err)
	}
}

// previewA2ABody extracts the first text part from a JSON-RPC
// `message/send` envelope's `params.message.parts[]` for the audit
// summary line. Returns "" on any parse hiccup — the audit row still
// lands with meta.body_bytes for size accounting.
func previewA2ABody(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var env struct {
		Params struct {
			Message struct {
				Parts []struct {
					Kind string `json:"kind"`
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	for _, p := range env.Params.Message.Parts {
		if p.Kind == "text" && p.Text != "" {
			if len(p.Text) > 200 {
				return p.Text[:200] + "…"
			}
			return p.Text
		}
	}
	return ""
}

// authorizeHostInTeam returns true iff the host row exists in the team.
// Returns (false, nil) on no-rows; (false, err) on DB trouble.
func (s *Server) authorizeHostInTeam(ctx context.Context, team, host string) (bool, error) {
	var existing string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM hosts WHERE team_id = ? AND id = ?`, team, host).Scan(&existing)
	if err == nil {
		return true, nil
	}
	// database/sql returns ErrNoRows for no-match; anything else is DB trouble.
	if err.Error() == "sql: no rows in result set" {
		return false, nil
	}
	return false, err
}

// parsePositiveInt parses an unsigned decimal integer; any non-digit run
// yields (0, error). Used for the `wait_ms` query param.
func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a positive integer")
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 { // guard against overflow / silly values
			return 0, errors.New("value too large")
		}
	}
	return n, nil
}

// readAllCapped reads the (already-capped) request body.
func readAllCapped(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}

