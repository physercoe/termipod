package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// Read-side admin host inspection (ADR-028 Phase 4 W15 / W14 --remote).
// GET /v1/admin/hosts lists the fleet; POST /v1/admin/hosts/{host}/ping
// round-trips the host.ping verb. Both are owner-scope and hub-wide,
// alongside the /v1/admin/fleet/* control verbs. Neither writes an
// audit row — plan §5.1: read-side commands don't audit.

// AdminHostRow is one row of GET /v1/admin/hosts.
type AdminHostRow struct {
	HostID          string `json:"host_id"`
	TeamID          string `json:"team_id"`
	Name            string `json:"name,omitempty"`
	Status          string `json:"status,omitempty"`
	Live            bool   `json:"live"`
	LastSeenAt      string `json:"last_seen_at,omitempty"`
	RunnerCommit    string `json:"runner_commit,omitempty"`
	RunnerBuildTime string `json:"runner_build_time,omitempty"`

	// Ping fields — populated only when the request carries ?ping=1.
	Pinged    bool   `json:"pinged,omitempty"`
	Version   string `json:"version,omitempty"`
	PingMS    int64  `json:"ping_ms,omitempty"`
	PingError string `json:"ping_error,omitempty"`
}

// hostPingResult is the decoded outcome of one host.ping round-trip.
type hostPingResult struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version,omitempty"`
	Commit    string `json:"commit,omitempty"`
	BuildTime string `json:"build_time,omitempty"`
	Modified  bool   `json:"modified,omitempty"`
	HostTS    string `json:"host_ts,omitempty"`
	PingMS    int64  `json:"ping_ms"`
	Error     string `json:"error,omitempty"`
}

// pingHost fires the read-side host.ping verb at one host and decodes
// the host-runner's build identity. It never errors out — an
// unreachable host comes back as {OK:false, Error:...} so callers can
// render a per-host status without special-casing transport failures.
func (s *Server) pingHost(ctx context.Context, hostID string) hostPingResult {
	start := time.Now()
	resp, err := s.tunnel.enqueueHostVerb(ctx, hostID, "host.ping", nil)
	res := hostPingResult{PingMS: time.Since(start).Milliseconds()}
	switch {
	case err != nil:
		res.Error = err.Error()
		return res
	case resp == nil:
		res.Error = "no response"
		return res
	case resp.Status < 200 || resp.Status >= 300:
		res.Error = fmt.Sprintf("host returned status %d", resp.Status)
		return res
	}
	raw, decErr := base64.StdEncoding.DecodeString(resp.BodyB64)
	if decErr != nil {
		res.Error = "host response body not base64"
		return res
	}
	var b struct {
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildTime string `json:"build_time"`
		Modified  bool   `json:"modified"`
		TS        string `json:"ts"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		res.Error = "host response not JSON"
		return res
	}
	res.OK = true
	res.Version, res.Commit = b.Version, b.Commit
	res.BuildTime, res.Modified, res.HostTS = b.BuildTime, b.Modified, b.TS
	return res
}

// handleAdminListHosts is GET /v1/admin/hosts — owner-scope. Lists
// every registered host with its heartbeat-derived liveness and the
// runner build info captured at registration. With ?ping=1 it also
// round-trips host.ping at each LIVE host to report the version it is
// actually running right now (W14 --remote uses this).
func (s *Server) handleAdminListHosts(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(w, r) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, team_id, COALESCE(name, ''), COALESCE(status, ''),
		       COALESCE(last_seen_at, ''), COALESCE(runner_commit, ''),
		       COALESCE(runner_build_time, ''),
		       (last_seen_at IS NOT NULL
		        AND last_seen_at > datetime('now', '-5 minutes')) AS live
		  FROM hosts
		 ORDER BY team_id, name`)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()
	hosts := []AdminHostRow{}
	for rows.Next() {
		var h AdminHostRow
		var live int
		if err := rows.Scan(&h.HostID, &h.TeamID, &h.Name, &h.Status,
			&h.LastSeenAt, &h.RunnerCommit, &h.RunnerBuildTime, &live); err != nil {
			s.writeDBErr(w, err)
			return
		}
		h.Live = live == 1
		hosts = append(hosts, h)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
	}

	if q := r.URL.Query().Get("ping"); q == "1" || q == "true" {
		for i := range hosts {
			if !hosts[i].Live {
				continue // an offline host won't answer the verb
			}
			pctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			pr := s.pingHost(pctx, hosts[i].HostID)
			cancel()
			hosts[i].Pinged = true
			hosts[i].Version = pr.Version
			hosts[i].PingMS = pr.PingMS
			hosts[i].PingError = pr.Error
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts})
}

// handleAdminHostPing is POST /v1/admin/hosts/{host}/ping — owner-scope.
// Round-trips the host.ping verb at one host and returns its build
// identity plus the measured latency.
func (s *Server) handleAdminHostPing(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(w, r) {
		return
	}
	host := chi.URLParam(r, "host")
	if host == "" {
		writeErr(w, http.StatusBadRequest, "host id required")
		return
	}
	// Resolve the host first so a typo'd id returns 404 promptly rather
	// than blocking on a verb that no host-runner will ever dequeue.
	var exists string
	switch err := s.db.QueryRowContext(r.Context(),
		`SELECT id FROM hosts WHERE id = ?`, host).Scan(&exists); {
	case err == nil:
		// found
	case err.Error() == "sql: no rows in result set":
		writeErr(w, http.StatusNotFound, "host not found")
		return
	default:
		s.writeDBErr(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res := s.pingHost(ctx, host)
	writeJSON(w, http.StatusOK, map[string]any{"host_id": host, "ping": res})
}
