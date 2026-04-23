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
	"sync"
	"time"
)

// AgentInfo is the minimal agent descriptor the A2A server needs to build a
// card. The host-runner derives this from its own running-agent list.
// Skills comes from the agent template (data-driven); when nil the card
// advertises no skills, which is a legitimate state for an agent whose
// template opts out or hasn't been registered yet.
type AgentInfo struct {
	ID     string
	Handle string
	Skills []Skill
}

// AgentSource returns the set of agents currently live on this host-runner.
// The server calls it per request so agent churn is reflected immediately
// without cache invalidation.
type AgentSource func(ctx context.Context) ([]AgentInfo, error)

// Server is the host-runner's A2A HTTP server. Serves agent-cards (§5.4,
// P3.2) and A2A v0.3 JSON-RPC task endpoints (P3.2b).
type Server struct {
	// PublicURL is the base URL clients use to reach this server, e.g.
	// "http://10.0.0.5:47821". Agent-card urls are derived as
	// <PublicURL>/a2a/<agent-id>. When empty, the server falls back to
	// the Host header from the incoming request.
	PublicURL string

	// Source lists the agents that should have cards. Required.
	Source AgentSource

	// Tasks holds per-agent task state for message/send, tasks/get,
	// tasks/cancel. Nil defaults to a fresh in-memory store on first
	// request so tests that only exercise the card path don't need to
	// allocate one. Concurrent callers share the store — safe; locked
	// internally.
	Tasks *TaskStore

	// Dispatcher hands submitted messages off to the agent runtime.
	// Nil uses NoopDispatcher so the server stays RPC-complete even
	// before a concrete driver is wired in. Host-runner integration is
	// a follow-up wedge.
	Dispatcher Dispatcher

	// Log receives server-level errors. Optional; defaults to slog.Default().
	Log *slog.Logger

	// http is the underlying server set up by Listen.
	http *http.Server

	// tasksOnce guards lazy init of Tasks so Handler() is safe to call
	// from multiple tests concurrently.
	tasksOnce sync.Once
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

// handleAgent routes /a2a/<id>/... paths. Two valid tails today:
//
//	/a2a/<id>                       — JSON-RPC task endpoint (POST)
//	/a2a/<id>/                      — ditto (trailing slash tolerant)
//	/a2a/<id>/.well-known/agent.json — agent-card (GET)
//
// Anything else is 404.
func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/a2a/")
	slash := strings.Index(rest, "/")
	var agentID, tail string
	if slash < 0 {
		// /a2a/<id> with no trailing slash — JSON-RPC endpoint.
		agentID = rest
		tail = ""
	} else {
		agentID = rest[:slash]
		tail = rest[slash+1:]
	}
	if agentID == "" {
		http.NotFound(w, r)
		return
	}

	switch tail {
	case ".well-known/agent.json":
		s.serveAgentCard(w, r, agentID)
	case "":
		// Before dispatching, confirm the agent exists on this host —
		// peers should see the same 404 they'd get for a bad card URL.
		if !s.agentExists(r.Context(), agentID) {
			http.NotFound(w, r)
			return
		}
		s.ensureTasks()
		TaskRPCHandler(agentID, s.Tasks, s.Dispatcher, nil).ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

// agentExists returns true when the Source reports an agent with this id
// on the current host. Errors are treated as "not present" — the peer
// gets a 404 either way, and the Log catches the underlying cause.
func (s *Server) agentExists(ctx context.Context, agentID string) bool {
	if s.Source == nil {
		return false
	}
	agents, err := s.Source(ctx)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("a2a source lookup failed", "agent", agentID, "err", err)
		}
		return false
	}
	for _, a := range agents {
		if a.ID == agentID {
			return true
		}
	}
	return false
}

func (s *Server) ensureTasks() {
	s.tasksOnce.Do(func() {
		if s.Tasks == nil {
			s.Tasks = NewTaskStore()
		}
	})
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
		Skills:             found.Skills,
	}
	writeJSON(w, http.StatusOK, card)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
