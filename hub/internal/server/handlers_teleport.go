package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// Host-to-host session teleport (ADR-057, wedge T1). POST
// /v1/teams/{team}/sessions/{id}/teleport {target_host_id} relocates a paused
// worktree session's two host-owned byte-stores — the git worktree and the
// engine-state directory — to the target host, then re-targets the existing
// resume path so the conversation continues there. The transcript never moves
// (it is hub-stored agent_events keyed by session_id).
//
// Orchestration is pause-first (ADR-014): the session must already be paused, so
// every step before the row flip is rollback-safe by simply leaving it paused
// on the source. The handoff itself rides the existing host_commands pull queue
// as the session_handoff_pack (source) and session_handoff_unpack (target)
// kinds; the hub never runs git or touches bytes.

// teleportCmdPoll / teleportCmdTimeout bound awaitHostCommand. The poll is short
// so a fast handoff returns promptly; the timeout is generous because a real
// pack pushes a branch and uploads an engine-state bundle over the network.
var (
	teleportCmdPoll    = 500 * time.Millisecond
	teleportCmdTimeout = 15 * time.Minute
)

// worktreeSpecView is the slice of a spawn spec teleport needs: the git repo and
// branch the worktree tracks.
type worktreeSpecView struct {
	Worktree struct {
		Repo   string `yaml:"repo"`
		Branch string `yaml:"branch"`
	} `yaml:"worktree"`
}

func (s *Server) handleTeleportSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "session")
	var in struct {
		TargetHostID string `json:"target_host_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.TargetHostID == "" {
		writeErr(w, http.StatusBadRequest, "target_host_id is required")
		return
	}
	out, code, err := s.teleportSession(r.Context(), team, id, in.TargetHostID)
	if err != nil {
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// teleportSession runs the full pause-first orchestration and returns the
// response body (or an HTTP status + error). Any failure before the resume flip
// leaves the session paused on the source — the caller can retry or resume.
func (s *Server) teleportSession(ctx context.Context, team, id, targetHost string) (map[string]any, int, error) {
	// 1. Load the session. Teleport requires it PAUSED (pause-first, ADR-014):
	//    the desktop action pauses then teleports. An active session returns 409
	//    so the operator/UI pauses first rather than us guessing it's idle.
	var status, curAgent, worktreePath, spawnSpec, engineSessionID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT status, current_agent_id, worktree_path, spawn_spec_yaml, engine_session_id
		  FROM sessions WHERE team_id = ? AND id = ?`, team, id).Scan(
		&status, &curAgent, &worktreePath, &spawnSpec, &engineSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, http.StatusNotFound, errors.New("session not found")
	}
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if status.String != "paused" {
		return nil, http.StatusConflict,
			errors.New("session must be paused before teleport (status=" + status.String + ")")
	}
	if curAgent.String == "" {
		return nil, http.StatusConflict, errors.New("session has no current agent")
	}
	if worktreePath.String == "" {
		return nil, http.StatusConflict, errors.New("session has no worktree (T1 teleports worktree sessions only)")
	}
	var wv worktreeSpecView
	if spawnSpec.String != "" {
		_ = yaml.Unmarshal([]byte(spawnSpec.String), &wv) // best-effort; empty repo handled below
	}
	if wv.Worktree.Repo == "" {
		return nil, http.StatusConflict,
			errors.New("session spec declares no worktree repo (T1 teleports git-worktree sessions only)")
	}

	// 2. Source host + engine kind from the paused agent.
	var kind, sourceHost sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT kind, host_id FROM agents WHERE team_id = ? AND id = ?`,
		team, curAgent.String).Scan(&kind, &sourceHost); err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("lookup agent: %w", err)
	}
	if targetHost == sourceHost.String {
		return nil, http.StatusConflict, errors.New("target host is the session's current host")
	}

	// 3. Target must be online and (best-effort) advertise the engine family.
	if rerr := s.checkSpawnHostReachable(ctx, team, targetHost); rerr != nil {
		return nil, http.StatusConflict, rerr
	}
	if serr := s.checkHostSupportsFamily(ctx, team, targetHost, kind.String); serr != nil {
		return nil, http.StatusConflict, serr
	}

	// 4. Pack on the SOURCE: commit+push the worktree branch, snapshot the engine
	//    state, chunk it to blobs. Returns portable (home-relative) paths.
	packArgs := map[string]any{
		"engine":            kind.String,
		"worktree_path":     worktreePath.String,
		"repo":              wv.Worktree.Repo,
		"branch":            wv.Worktree.Branch,
		"remote":            "origin",
		"engine_session_id": engineSessionID.String,
	}
	packRaw, perr := s.awaitHostCommand(ctx, sourceHost.String, curAgent.String, "session_handoff_pack", packArgs)
	if perr != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("teleport pack on source failed: %w", perr)
	}
	var pack struct {
		Branch               string `json:"branch"`
		HeadSHA              string `json:"head_sha"`
		Remote               string `json:"remote"`
		ManifestSHA          string `json:"manifest_sha"`
		PortableWorktreePath string `json:"portable_worktree_path"`
		PortableRepo         string `json:"portable_repo"`
	}
	if err := json.Unmarshal(packRaw, &pack); err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("teleport pack: bad result: %w", err)
	}
	if pack.ManifestSHA == "" || pack.HeadSHA == "" || pack.Branch == "" {
		return nil, http.StatusBadGateway, errors.New("teleport pack: incomplete result from source host")
	}

	// 5. Unpack on the TARGET: re-anchor the portable paths against the target's
	//    home, fetch+worktree-add, restore the engine state. Returns the
	//    target-absolute worktree path.
	unpackArgs := map[string]any{
		"engine":            kind.String,
		"repo":              pack.PortableRepo,
		"worktree_path":     pack.PortableWorktreePath,
		"branch":            pack.Branch,
		"remote":            pack.Remote,
		"expect_head":       pack.HeadSHA,
		"engine_session_id": engineSessionID.String,
		"manifest_sha":      pack.ManifestSHA,
	}
	unpackRaw, uerr := s.awaitHostCommand(ctx, targetHost, "", "session_handoff_unpack", unpackArgs)
	if uerr != nil {
		// Rollback: the session is untouched (still paused on the source). The
		// pushed branch + uploaded blobs are harmless orphans.
		return nil, http.StatusBadGateway, fmt.Errorf("teleport unpack on target failed: %w", uerr)
	}
	var unpack struct {
		WorktreePath string `json:"worktree_path"`
	}
	if err := json.Unmarshal(unpackRaw, &unpack); err != nil || unpack.WorktreePath == "" {
		return nil, http.StatusBadGateway, errors.New("teleport unpack: target host returned no worktree path")
	}

	// 6. Commit point: re-target the resume onto the target host + its worktree.
	resumeOut, code, rerr := s.resumePausedSessionWith(ctx, team, id, resumeOverrides{
		hostID:       targetHost,
		worktreePath: unpack.WorktreePath,
	})
	if rerr != nil {
		return nil, code, fmt.Errorf("teleport spawn on target failed: %w", rerr)
	}

	s.recordAudit(ctx, team, "session.teleport", "session", id,
		"teleported to host="+targetHost,
		map[string]any{
			"source_host":   sourceHost.String,
			"target_host":   targetHost,
			"new_agent_id":  resumeOut["new_agent_id"],
			"worktree_path": unpack.WorktreePath,
		})

	return map[string]any{
		"session_id":    id,
		"source_host":   sourceHost.String,
		"target_host":   targetHost,
		"new_agent_id":  resumeOut["new_agent_id"],
		"worktree_path": unpack.WorktreePath,
	}, http.StatusOK, nil
}

// awaitHostCommand enqueues a host command and blocks until the host-runner
// marks it done (returning its result_json) or failed/timeout (returning an
// error). The host-runner picks the row up on its next poll and PATCHes the
// result back — this is the hub-side inverse of that pull loop.
func (s *Server) awaitHostCommand(ctx context.Context, hostID, agentID, kind string, args any) (json.RawMessage, error) {
	cmdID, err := s.enqueueHostCommand(ctx, hostID, agentID, kind, args)
	if err != nil {
		return nil, fmt.Errorf("enqueue %s: %w", kind, err)
	}
	deadline := time.Now().Add(teleportCmdTimeout)
	ticker := time.NewTicker(teleportCmdPoll)
	defer ticker.Stop()
	for {
		var st, result, cmdErr sql.NullString
		qerr := s.db.QueryRowContext(ctx,
			`SELECT status, result_json, error FROM host_commands WHERE id = ?`, cmdID).
			Scan(&st, &result, &cmdErr)
		if qerr != nil {
			return nil, fmt.Errorf("poll %s: %w", kind, qerr)
		}
		switch st.String {
		case "done":
			return json.RawMessage(result.String), nil
		case "failed":
			if cmdErr.String != "" {
				return nil, errors.New(cmdErr.String)
			}
			return nil, fmt.Errorf("%s failed on host", kind)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s timed out after %s (host not responding?)", kind, teleportCmdTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// checkHostSupportsFamily best-effort verifies the target host advertises the
// engine family. A host that hasn't reported capabilities (empty map) is allowed
// — the spawn itself fails later if the engine is truly missing — but a host
// that reports the family explicitly not installed is refused up front.
func (s *Server) checkHostSupportsFamily(ctx context.Context, team, hostID, family string) error {
	if family == "" {
		return nil
	}
	var caps sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT capabilities_json FROM hosts WHERE team_id = ? AND id = ?`,
		team, hostID).Scan(&caps); err != nil {
		return nil // reachability already checked; don't hard-fail on caps read
	}
	if caps.String == "" {
		return nil
	}
	var parsed struct {
		Agents map[string]struct {
			Installed bool `json:"installed"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(caps.String), &parsed); err != nil {
		return nil // unparseable caps → don't block
	}
	if parsed.Agents == nil {
		return nil
	}
	entry, present := parsed.Agents[family]
	if present && !entry.Installed {
		return errors.New("target host does not have engine " + family + " installed")
	}
	return nil
}
