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

	"github.com/termipod/hub/internal/hostrunner"
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

// teleportSpecView is the slice of a spawn spec teleport needs: the git repo +
// branch a worktree session tracks, and the explicit default_workdir a
// non-worktree session may pin (T2a — otherwise its workdir is derived).
type teleportSpecView struct {
	Worktree struct {
		Repo   string `yaml:"repo"`
		Branch string `yaml:"branch"`
	} `yaml:"worktree"`
	Backend struct {
		DefaultWorkdir string `yaml:"default_workdir"`
	} `yaml:"backend"`
}

func (s *Server) handleTeleportSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "session")
	var in struct {
		TargetHostID string `json:"target_host_id"`
		// EnvSecretEnvelope, when present, is the session's vault secrets
		// re-sealed to the TARGET host's key by a vault-holding client (D-7).
		// A secret-bearing session is refused without it (the hub cannot
		// re-seal); a non-secret session ignores it.
		EnvSecretEnvelope string `json:"env_secret_envelope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.TargetHostID == "" {
		writeErr(w, http.StatusBadRequest, "target_host_id is required")
		return
	}
	out, code, err := s.teleportSession(r.Context(), team, id, in.TargetHostID, in.EnvSecretEnvelope)
	if err != nil {
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// teleportSession runs the full pause-first orchestration and returns the
// response body (or an HTTP status + error). Any failure before the resume flip
// leaves the session paused on the source — the caller can retry or resume.
func (s *Server) teleportSession(ctx context.Context, team, id, targetHost, resealEnvelope string) (map[string]any, int, error) {
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
	var wv teleportSpecView
	if spawnSpec.String != "" {
		_ = yaml.Unmarshal([]byte(spawnSpec.String), &wv) // best-effort; empty fields handled below
	}

	// 2. Source host + engine kind + identity from the paused agent.
	var kind, sourceHost, agentProject, agentHandle sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT kind, host_id, project_id, COALESCE(handle, '') FROM agents WHERE team_id = ? AND id = ?`,
		team, curAgent.String).Scan(&kind, &sourceHost, &agentProject, &agentHandle); err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("lookup agent: %w", err)
	}
	if targetHost == sourceHost.String {
		return nil, http.StatusConflict, errors.New("target host is the session's current host")
	}

	// A worktree session moves its files via git; a non-worktree session (T2a)
	// moves its whole working directory as a tar bundle. The workdir is re-derived
	// here exactly the way DoSpawn does at spawn — deterministic from identity +
	// home — so the source tars it and the target respawn re-derives the same
	// relative path (`~/hub-work/…`) and finds the restored files. Deriving with
	// needsWorkdir=true resolves the explicit / project-bound / context-bearing
	// cases; a session that genuinely runs from the host cwd (no workdir at all)
	// has nothing to relocate and its pack fails loudly on the source.
	worktreeMode := worktreePath.String != ""
	var portableWorkdir string
	if worktreeMode {
		if wv.Worktree.Repo == "" {
			return nil, http.StatusConflict,
				errors.New("worktree session spec declares no git repo (cannot teleport its files)")
		}
	} else {
		portableWorkdir = hostrunner.DeriveWorkdir(
			team, wv.Backend.DefaultWorkdir, agentProject.String, agentHandle.String, curAgent.String, true)
		if portableWorkdir == "" {
			return nil, http.StatusConflict, errors.New(
				"session has neither a git worktree nor a derivable workdir (it runs from the host cwd) — nothing to relocate")
		}
	}

	// 3. Target must be online and (best-effort) advertise the engine family.
	if rerr := s.checkSpawnHostReachable(ctx, team, targetHost); rerr != nil {
		return nil, http.StatusConflict, rerr
	}
	if serr := s.checkHostSupportsFamily(ctx, team, targetHost, kind.String); serr != nil {
		return nil, http.StatusConflict, serr
	}

	// 3b. Secret-bearing sessions (ADR-056 D-6, D-7): the source env_secret_envelope
	//     is sealed to the SOURCE host's key (the AAD binds the host id), so the
	//     hub cannot re-seal it and the resume path cannot mint a new one — only a
	//     vault-holding client can. The desktop teleport flow does exactly that:
	//     it re-resolves the profile's secret_refs and re-seals them to the target
	//     host, passing the result as env_secret_envelope. So:
	//       - no re-sealed envelope supplied → refuse UP FRONT (409), before any
	//         byte-moving. Headless/API teleport of a secret-bearing session stays
	//         impossible by design (nothing else can re-seal).
	//       - re-sealed envelope supplied → accept, but verify it is bound to the
	//         TARGET host. The hub can't open it, but the envelope's host_id is
	//         authenticated by the AAD and the target host refuses to open one
	//         whose host_id isn't its own (envseal.Open); reject a mismatch here
	//         with a clean 400 rather than let the target respawn fail opaquely.
	//     Detect secret-bearing via the paused agent's spawn row (D-4 guarantees a
	//     secret-bearing spawn carried an envelope) plus the project profile, which
	//     the re-spawn would re-resolve.
	var priorEnvelope string
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(env_secret_envelope, '') FROM agent_spawns
		 WHERE child_agent_id = ? ORDER BY spawned_at DESC LIMIT 1`,
		curAgent.String).Scan(&priorEnvelope)
	secretBearing := priorEnvelope != ""
	if !secretBearing && agentProject.String != "" {
		if pid := s.projectEnvProfileID(ctx, agentProject.String); pid != "" {
			if prof, perr := s.getEnvProfileByID(ctx, team, pid); perr == nil && len(prof.SecretRefs) > 0 {
				secretBearing = true
			}
		}
	}
	if secretBearing {
		if resealEnvelope == "" {
			return nil, http.StatusConflict, errors.New(
				"session carries vault secrets sealed to its current host; the teleport request " +
					"must include an env_secret_envelope re-sealed to the target host (a vault-holding " +
					"client does this). Headless/API teleport of a secret-bearing session is refused by design")
		}
		var env struct {
			HostID string `json:"host_id"`
		}
		if err := json.Unmarshal([]byte(resealEnvelope), &env); err != nil {
			return nil, http.StatusBadRequest, errors.New("env_secret_envelope is not valid JSON")
		}
		if env.HostID != targetHost {
			return nil, http.StatusBadRequest, fmt.Errorf(
				"env_secret_envelope is sealed to host %q, not the teleport target %q", env.HostID, targetHost)
		}
	} else {
		// A non-secret session ignores any stray envelope so it never lands on
		// the target respawn (the host would otherwise try to open it).
		resealEnvelope = ""
	}

	// 4. Pack on the SOURCE. Worktree: commit+push the branch + tar the engine
	//    state. Non-worktree: tar the workdir + the engine state. Both chunk to
	//    blobs and return portable (home-relative) paths.
	packArgs := map[string]any{
		"engine":            kind.String,
		"engine_session_id": engineSessionID.String,
	}
	if worktreeMode {
		packArgs["worktree_path"] = worktreePath.String
		packArgs["repo"] = wv.Worktree.Repo
		packArgs["branch"] = wv.Worktree.Branch
		packArgs["remote"] = "origin"
	} else {
		packArgs["workdir"] = portableWorkdir
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
		WorkdirManifestSHA   string `json:"workdir_manifest_sha"`
		PortableWorkdirPath  string `json:"portable_workdir_path"`
	}
	if err := json.Unmarshal(packRaw, &pack); err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("teleport pack: bad result: %w", err)
	}
	if pack.ManifestSHA == "" {
		return nil, http.StatusBadGateway, errors.New("teleport pack: incomplete result from source host")
	}
	if worktreeMode && (pack.HeadSHA == "" || pack.Branch == "") {
		return nil, http.StatusBadGateway, errors.New("teleport pack: worktree result missing branch/head")
	}
	if !worktreeMode && pack.WorkdirManifestSHA == "" {
		return nil, http.StatusBadGateway, errors.New("teleport pack: non-worktree result missing workdir bundle")
	}

	// 5. Unpack on the TARGET: re-anchor the portable paths against the target's
	//    home and restore the byte-stores. Worktree: fetch+worktree-add + engine
	//    state. Non-worktree: workdir tar + engine state. Returns the target-
	//    absolute path.
	unpackArgs := map[string]any{
		"engine":            kind.String,
		"engine_session_id": engineSessionID.String,
		"manifest_sha":      pack.ManifestSHA,
	}
	if worktreeMode {
		unpackArgs["repo"] = pack.PortableRepo
		unpackArgs["worktree_path"] = pack.PortableWorktreePath
		unpackArgs["branch"] = pack.Branch
		unpackArgs["remote"] = pack.Remote
		unpackArgs["expect_head"] = pack.HeadSHA
	} else {
		unpackArgs["workdir"] = pack.PortableWorkdirPath
		unpackArgs["workdir_manifest_sha"] = pack.WorkdirManifestSHA
	}
	unpackRaw, uerr := s.awaitHostCommand(ctx, targetHost, "", "session_handoff_unpack", unpackArgs)
	if uerr != nil {
		// Rollback: the session is untouched (still paused on the source). The
		// pushed branch + uploaded blobs are harmless orphans.
		return nil, http.StatusBadGateway, fmt.Errorf("teleport unpack on target failed: %w", uerr)
	}
	var unpack struct {
		WorktreePath string `json:"worktree_path"`
		Workdir      string `json:"workdir"`
	}
	if err := json.Unmarshal(unpackRaw, &unpack); err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("teleport unpack: bad result: %w", err)
	}
	if worktreeMode && unpack.WorktreePath == "" {
		return nil, http.StatusBadGateway, errors.New("teleport unpack: target host returned no worktree path")
	}
	if !worktreeMode && unpack.Workdir == "" {
		return nil, http.StatusBadGateway, errors.New("teleport unpack: target host returned no workdir path")
	}

	// 6. Commit point: re-target the resume onto the target host. A worktree
	//    session pins the target-side worktree path; a non-worktree session sets
	//    no path — its respawn re-derives the same workdir the unpack restored to.
	//    From here the orchestration must not die with the caller: the awaits
	//    above are safely cancellable (failure mode = still paused on source),
	//    but a cancel landing between DoSpawn and the session row-flip inside
	//    the resume would strand a live target agent on a still-paused session.
	//    The desktop's request timeout or a closed laptop must not do that.
	ctx = context.WithoutCancel(ctx)
	resumeOv := resumeOverrides{hostID: targetHost, envSecretEnvelope: resealEnvelope}
	if worktreeMode {
		resumeOv.worktreePath = unpack.WorktreePath
	}
	resumeOut, code, rerr := s.resumePausedSessionWith(ctx, team, id, resumeOv)
	if rerr != nil {
		return nil, code, fmt.Errorf("teleport spawn on target failed: %w", rerr)
	}

	relocated := unpack.WorktreePath
	if relocated == "" {
		relocated = unpack.Workdir
	}
	s.recordAudit(ctx, team, "session.teleport", "session", id,
		"teleported to host="+targetHost,
		map[string]any{
			"source_host":  sourceHost.String,
			"target_host":  targetHost,
			"new_agent_id": resumeOut["new_agent_id"],
			"location":     relocated,
		})

	return map[string]any{
		"session_id":    id,
		"source_host":   sourceHost.String,
		"target_host":   targetHost,
		"new_agent_id":  resumeOut["new_agent_id"],
		"worktree_path": unpack.WorktreePath,
		"workdir":       unpack.Workdir,
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
