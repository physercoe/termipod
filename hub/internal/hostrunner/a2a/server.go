package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// AgentInfo is the minimal agent descriptor the A2A server needs to build a
// card. The host-runner derives this from its own running-agent list.
type AgentInfo struct {
	ID     string
	Handle string
}

// AgentSource returns the set of agents currently live on this host-runner.
// The server calls it per request so agent churn is reflected immediately
// without cache invalidation.
type AgentSource func(ctx context.Context) ([]AgentInfo, error)

// Server is the host-runner's A2A HTTP server. It serves agent-cards today
// (P3.2); task endpoints will be added in a follow-up wedge.
type Server struct {
	// PublicURL is the base URL clients use to reach this server, e.g.
	// "http://10.0.0.5:47821". Agent-card urls are derived as
	// <PublicURL>/a2a/<agent-id>. When empty, the server falls back to
	// the Host header from the incoming request.
	PublicURL string

	// Source lists the agents that should have cards. Required.
	Source AgentSource

	// Log receives server-level errors. Optional; defaults to slog.Default().
	Log *slog.Logger

	// http is the underlying server set up by Listen.
	http *http.Server
}

// Handler returns the HTTP mux so callers can mount it under their own
// server, or test it directly.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/a2a/agents", s.handleList)
	mux.HandleFunc("/a2a/", s.handleAgent)
	return mux
}

// Listen binds to addr (":0" picks a free port) and serves until ctx is
// done. Returns the actual listen address so callers can publish it.
func (s *Server) Listen(ctx context.Context, addr string) (string, error) {
	if s.Source == nil {
		return "", errors.New("a2a: Source is required")
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	s.http = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.Log.Error("a2a server serve failed", "err", err)
		}
	}()
	return ln.Addr().String(), nil
}

// handleList returns the set of live agent ids. Helper endpoint — not part
// of the A2A spec, but useful for hub directory sync.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	agents, err := s.Source(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ids := make([]string, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": ids})
}

// handleAgent routes /a2a/<id>/.well-known/agent.json to the card builder.
// Any other /a2a/<id>/... path is 404 for now; task endpoints land here.
func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/a2a/")
	slash := strings.Index(rest, "/")
	if slash < 0 {
		http.NotFound(w, r)
		return
	}
	agentID := rest[:slash]
	tail := rest[slash+1:]
	if agentID == "" {
		http.NotFound(w, r)
		return
	}

	switch tail {
	case ".well-known/agent.json":
		s.serveAgentCard(w, r, agentID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveAgentCard(w http.ResponseWriter, r *http.Request, agentID string) {
	agents, err := s.Source(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var found *AgentInfo
	for i := range agents {
		if agents[i].ID == agentID {
			found = &agents[i]
			break
		}
	}
	if found == nil {
		http.NotFound(w, r)
		return
	}

	base := s.PublicURL
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = fmt.Sprintf("%s://%s", scheme, r.Host)
	}
	card := AgentCard{
		ProtocolVersion:    ProtocolVersion,
		Name:               found.Handle,
		URL:                fmt.Sprintf("%s/a2a/%s", strings.TrimRight(base, "/"), found.ID),
		Version:            "1.0.0",
		Capabilities:       Capabilities{Streaming: false},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             SkillsForHandle(found.Handle),
	}
	writeJSON(w, http.StatusOK, card)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
