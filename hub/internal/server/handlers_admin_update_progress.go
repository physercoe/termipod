package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// hostUpdateProgress is one self-update progress sample as posted by a
// host-runner mid-update and read back by the operator's admin pane.
// Phase values: downloading | installing | done | error (host side
// mirrors selfupdate's own phase names for the first two).
type hostUpdateProgress struct {
	Phase     string    `json:"phase"`
	Done      int64     `json:"done"`
	Total     int64     `json:"total"`
	ToVersion string    `json:"to_version,omitempty"`
	Error     string    `json:"error,omitempty"`
	At        time.Time `json:"at"`
}

// updateProgressStore holds the latest self-update progress sample per
// host. In-memory on purpose: this is ops ephemera — a hub restart
// loses a progress bar, never state — and the alternatives (audit rows
// per 256KiB step, a table) buy nothing. Entries older than
// updateProgressTTL are pruned on write so a host that died mid-update
// doesn't leave a stale "downloading" behind forever.
type updateProgressStore struct {
	mu     sync.Mutex
	byHost map[string]hostUpdateProgress
}

const updateProgressTTL = time.Hour

func (s *updateProgressStore) put(hostID string, p hostUpdateProgress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byHost == nil {
		s.byHost = map[string]hostUpdateProgress{}
	}
	for id, e := range s.byHost {
		if time.Since(e.At) > updateProgressTTL {
			delete(s.byHost, id)
		}
	}
	s.byHost[hostID] = p
}

func (s *updateProgressStore) get(hostID string) (hostUpdateProgress, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byHost[hostID]
	if !ok || time.Since(p.At) > updateProgressTTL {
		return hostUpdateProgress{}, false
	}
	return p, true
}

// handleHostUpdateProgress is POST /v1/teams/{team}/hosts/{host}/update-progress
// — the host-runner's half of the update-progress channel, same auth
// posture as the heartbeat route beside it (host bearer + teamGate).
// The body is one progress sample; the latest per host wins.
func (s *Server) handleHostUpdateProgress(w http.ResponseWriter, r *http.Request) {
	host := chi.URLParam(r, "host")
	if host == "" {
		writeErr(w, http.StatusBadRequest, "host id required")
		return
	}
	var p hostUpdateProgress
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid progress body")
		return
	}
	switch p.Phase {
	case "downloading", "installing", "done", "error":
	default:
		writeErr(w, http.StatusBadRequest, "phase must be downloading|installing|done|error")
		return
	}
	p.At = time.Now().UTC()
	s.updateProgress.put(host, p)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminHostUpdateProgress is GET /v1/admin/hosts/{host}/update-progress
// — owner-scope read half the mobile Admin pane polls while an update is
// in flight. A host that never reported reads as {"phase":"idle"} so the
// client can distinguish "no sample yet" from an error.
func (s *Server) handleAdminHostUpdateProgress(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(w, r) {
		return
	}
	host := chi.URLParam(r, "host")
	if host == "" {
		writeErr(w, http.StatusBadRequest, "host id required")
		return
	}
	p, ok := s.updateProgress.get(host)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"phase": "idle"})
		return
	}
	writeJSON(w, http.StatusOK, p)
}
