package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/termipod/hub/internal/hostjobs"
)

// handlers_dataset_export.go — the desktop-facing half of ADR-058 §3.
//
// Submit is a dataset-scoped POST that inserts a `dataset_export_rrd` command
// row and returns its id; status is a team-scoped read of that row. The desktop
// polls. Nothing holds a connection open, so the failure family ADR-057's T1c
// fix closed — a short client timeout stacked on a long command, stranding work
// and breaking cancel — is structurally impossible here.

// datasetExportIn is the submit body.
type datasetExportIn struct {
	// EpisodeIndex is a pointer so a missing field is distinguishable from
	// episode 0, which is a perfectly ordinary episode to export.
	EpisodeIndex *int64 `json:"episode_index"`
	// RepoID overrides LeRobot's dataset identity. Optional: the host derives
	// `owner/name` from the root's last two segments when it is absent, and
	// reports back what it used.
	RepoID string `json:"repo_id,omitempty"`
}

type datasetExportOut struct {
	CommandID string `json:"command_id"`
	Kind      string `json:"kind"`
	// Reused is true when an identical export was already in flight and this
	// call joined it instead of queueing a second one.
	Reused bool `json:"reused,omitempty"`
}

// handleExportDatasetEpisode submits the export as a host job.
func (s *Server) handleExportDatasetEpisode(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	ds, err := s.datasetInTeam(r.Context(), team, chi.URLParam(r, "dataset"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}

	var in datasetExportIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.EpisodeIndex == nil || *in.EpisodeIndex < 0 {
		writeErr(w, http.StatusBadRequest, "episode_index is required and must not be negative")
		return
	}

	// Same posture as the synchronous verbs (datasetHostVerb): a dataset with
	// no host has nobody to run the export, and a non-local root is an honest
	// refusal rather than a slow path that half-works.
	if ds.HostID == "" {
		writeErr(w, http.StatusConflict, "dataset has no host; exporting it requires one")
		return
	}
	if ds.Source != "local" {
		writeErr(w, http.StatusNotImplemented, "only local dataset roots can be exported today")
		return
	}
	// Refuse at submit when the host has said it cannot do this, so a caller is
	// told now instead of polling a job to its failure (ADR-058 §2, #394).
	if err := s.checkHostSupportsTool(r.Context(), team, ds.HostID, hostjobs.ToolLeRobotExport); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}

	args := map[string]any{
		"root_path":     ds.RootPath,
		"episode_index": *in.EpisodeIndex,
	}
	if in.RepoID != "" {
		args["repo_id"] = in.RepoID
	}

	// Joining an identical in-flight export rather than queueing a second one.
	// The episodes table is browsed and the button is a button: a double-click
	// must not cost a second pass over every frame of the episode, and the two
	// exports would race for the same artifact path.
	if existing, err := s.findInFlightJob(r.Context(), ds.HostID, hostjobs.KindDatasetExportRRD, args); err != nil {
		s.writeDBErr(w, err)
		return
	} else if existing != "" {
		writeJSON(w, http.StatusAccepted, datasetExportOut{
			CommandID: existing, Kind: hostjobs.KindDatasetExportRRD, Reused: true,
		})
		return
	}

	id, err := s.enqueueHostCommand(r.Context(), ds.HostID, "", hostjobs.KindDatasetExportRRD, args)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	s.log.Info("dataset export submitted",
		"dataset", ds.ID, "host", ds.HostID, "episode", *in.EpisodeIndex, "command", id)
	writeJSON(w, http.StatusAccepted, datasetExportOut{
		CommandID: id, Kind: hostjobs.KindDatasetExportRRD,
	})
}

// findInFlightJob returns the id of an unfinished command with byte-identical
// args, or "".
//
// Comparing marshalled args works because enqueueHostCommand marshals the same
// map the same way — Go's encoder sorts map keys — so two submissions of the same
// export produce the same string. A drift in how args are built would only cost
// a duplicate export, never a wrong one.
func (s *Server) findInFlightJob(ctx context.Context, hostID, kind string, args map[string]any) (string, error) {
	b, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	var id string
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM host_commands
		 WHERE host_id = ? AND kind = ? AND args_json = ?
		   AND status IN ('pending','delivered')
		 ORDER BY created_at DESC LIMIT 1`, hostID, kind, string(b)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// checkHostSupportsTool refuses when a host has explicitly reported a tool as
// unavailable.
//
// Deliberately the same shape as checkHostSupportsFamily: a host that has not
// reported capabilities yet, or whose payload will not parse, is allowed through
// and the job itself gives the authoritative answer. Only an explicit "not
// installed" blocks, and it carries the host's own detail so the caller learns
// *which* half of the pin is missing rather than just that something is.
func (s *Server) checkHostSupportsTool(ctx context.Context, team, hostID, tool string) error {
	var caps sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT capabilities_json FROM hosts WHERE team_id = ? AND id = ?`,
		team, hostID).Scan(&caps); err != nil {
		return nil
	}
	if caps.String == "" {
		return nil
	}
	var parsed struct {
		Tools map[string]struct {
			Installed bool   `json:"installed"`
			Detail    string `json:"detail"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(caps.String), &parsed); err != nil {
		return nil
	}
	entry, present := parsed.Tools[tool]
	if !present || entry.Installed {
		return nil
	}
	msg := "the host cannot run this export: " + tool + " is not available there"
	if entry.Detail != "" {
		msg += " (" + entry.Detail + ")"
	}
	return errors.New(msg)
}

// handleGetHostCommand is the team-scoped status read a job poller uses.
//
// Team scope comes from the command's host, joined here rather than trusted from
// the path: host_commands has no team_id of its own, and a caller who can name a
// command id must not thereby be able to read another team's row.
func (s *Server) handleGetHostCommand(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	cmdID := chi.URLParam(r, "cmd")

	var (
		c                           commandOut
		args, result, progress      string
		progressAt, delivered, comp sql.NullString
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT c.id, c.host_id, COALESCE(c.agent_id, ''), c.kind, c.args_json,
		       c.status, COALESCE(c.result_json, ''), COALESCE(c.error, ''),
		       COALESCE(c.progress_json, ''), c.progress_at,
		       c.created_at, c.delivered_at, c.completed_at
		  FROM host_commands c
		  JOIN hosts h ON h.id = c.host_id
		 WHERE c.id = ? AND h.team_id = ?`, cmdID, team).
		Scan(&c.ID, &c.HostID, &c.AgentID, &c.Kind, &args,
			&c.Status, &result, &c.Error,
			&progress, &progressAt,
			&c.CreatedAt, &delivered, &comp)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "command not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	c.Args = json.RawMessage(args)
	if result != "" {
		c.Result = json.RawMessage(result)
	}
	if progress != "" {
		c.Progress = json.RawMessage(progress)
	}
	if progressAt.Valid {
		c.ProgressAt = &progressAt.String
	}
	if delivered.Valid {
		c.DeliveredAt = &delivered.String
	}
	if comp.Valid {
		c.CompletedAt = &comp.String
	}
	writeJSON(w, http.StatusOK, c)
}

// handleCancelHostCommand asks the owning host to stop a running job.
//
// Enqueued as the inline `job_cancel` kind rather than mutating the target row
// here: the hub cannot stop a subprocess, and a row flipped to `failed` while
// the export kept running would leave the host burning CPU on work nobody is
// waiting for. Cancelling something already finished is a no-op success.
func (s *Server) handleCancelHostCommand(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	cmdID := chi.URLParam(r, "cmd")

	var hostID, kind, status string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT c.host_id, c.kind, c.status
		  FROM host_commands c
		  JOIN hosts h ON h.id = c.host_id
		 WHERE c.id = ? AND h.team_id = ?`, cmdID, team).Scan(&hostID, &kind, &status)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "command not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !hostjobs.Is(kind) {
		writeErr(w, http.StatusBadRequest, "only detached host jobs can be cancelled")
		return
	}
	if status == "done" || status == "failed" {
		w.WriteHeader(http.StatusNoContent) // already over; idempotent
		return
	}
	if _, err := s.enqueueHostCommand(r.Context(), hostID, "", hostjobs.KindCancel,
		map[string]any{"command_id": cmdID}); err != nil {
		s.writeDBErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
