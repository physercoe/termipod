package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
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

	resp, err := s.tunnel.enqueueAndWait(ctx, host, req)
	if err != nil {
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
	w.WriteHeader(status)
	_, _ = w.Write(bodyBytes)
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

