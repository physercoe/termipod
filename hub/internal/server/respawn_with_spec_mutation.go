package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// flagForField maps the abstract picker concept ("mode" / "model") to
// the concrete CLI flag each driver listens to (ADR-021 W2.3). The
// mapping is keyed by the agent's family because per-engine flag spelling
// differs even when the concept is the same:
//
//   - claude-code: --model / --permission-mode
//   - codex:       --model / --approval-policy
//
// gemini-cli M2 (exec-per-turn) is not in this table — it routes via
// per_turn_argv (W2.4 driver-side), not respawn. M1 routes via rpc
// (W2.2). Anything not in this table falls through to errUnknownFamilyField.
var flagForField = map[string]map[string]string{
	"claude-code": {
		"model": "model",
		"mode":  "permission-mode",
	},
	"codex": {
		"model": "model",
		"mode":  "approval-policy",
	},
}

var errUnknownFamilyField = errors.New(
	"respawn-with-spec-mutation: unknown family/field combination")

// respawnWithSpecMutation reads the active spawn spec, mutates the
// requested mode/model flag, terminates the current agent, and spawns
// a fresh one with the new flags inside the same session row. The new
// agent re-attaches via the engine_session_id resume cursor (ADR-014)
// so the transcript stays continuous.
//
// W2.3 implementation. Returns:
//   - nil — the swap succeeded; the new agent is pending and the
//     session row now points at it.
//   - errUnknownFamilyField — the (family, field) pair has no
//     declared flag; mobile renders this as "this engine doesn't
//     support runtime <field> switching."
//   - errFlagNotInCmd — the spec's backend.cmd doesn't carry the
//     expected flag; respawn would be a no-op.
//   - generic errors — DB / template / DoSpawn failures.
func (s *Server) respawnWithSpecMutation(
	ctx context.Context,
	agentID string,
	field string, // "mode" or "model"
	value string,
) error {
	if field != "mode" && field != "model" {
		return fmt.Errorf("respawn-with-spec-mutation: invalid field %q (want mode|model)", field)
	}
	if value == "" {
		return errors.New("respawn-with-spec-mutation: empty value")
	}

	// 1. Resolve the agent's identity + the session it's attached to.
	var (
		teamID, kind, handle, hostID, parentID sql.NullString
		worktreePath, backendJSON              sql.NullString
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT team_id, kind, handle, host_id,
		       (SELECT parent_agent_id FROM agent_spawns
		         WHERE child_agent_id = agents.id
		         ORDER BY spawned_at DESC LIMIT 1),
		       worktree_path, COALESCE(backend_json, '{}')
		  FROM agents WHERE id = ?`, agentID).Scan(
		&teamID, &kind, &handle, &hostID, &parentID, &worktreePath, &backendJSON,
	); err != nil {
		return fmt.Errorf("respawn-with-spec-mutation: lookup agent: %w", err)
	}
	if teamID.String == "" || kind.String == "" || handle.String == "" {
		return errors.New("respawn-with-spec-mutation: agent missing required fields (team/kind/handle)")
	}

	// `agents.kind` is the ENGINE only for a direct spawn. For a steward it is
	// the persona template (`steward.claude-m4`), and the engine family lives
	// in `backend_json.kind` (handlers_agents.go:1567) — the same distinction
	// handlers_sessions.go draws for context mutations. Everything below that
	// is keyed by FAMILY must use `engine`; the respawn's own `Kind` must stay
	// `kind`, because it re-spawns the same persona, not a bare engine.
	engine := backendKindOf(backendJSON.String)
	if engine == "" {
		engine = kind.String
	}

	// 2. Resolve the flag for the agent's family + field.
	flagMap, ok := flagForField[engine]
	if !ok {
		return errUnknownFamilyField
	}
	flag, ok := flagMap[field]
	if !ok {
		return errUnknownFamilyField
	}

	// 3. Find the session row carrying the live spawn_spec_yaml. We
	//    prefer sessions.spawn_spec_yaml (the canonical post-resume
	//    copy) over agent_spawns.spawn_spec_yaml (a snapshot at spawn
	//    time) so prior flag mutations stick across consecutive picker
	//    flips.
	var (
		sessionID, specYAML sql.NullString
		engineSessionID     sql.NullString
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, spawn_spec_yaml, engine_session_id
		  FROM sessions
		 WHERE team_id = ? AND current_agent_id = ?
		 ORDER BY last_active_at DESC LIMIT 1`,
		teamID.String, agentID).Scan(
		&sessionID, &specYAML, &engineSessionID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("respawn-with-spec-mutation: agent has no live session")
		}
		return fmt.Errorf("respawn-with-spec-mutation: lookup session: %w", err)
	}
	if !specYAML.Valid || specYAML.String == "" {
		return errors.New("respawn-with-spec-mutation: session has no spawn_spec_yaml")
	}

	// 4. Mutate the spec's backend.cmd.
	mutated, err := mutateBackendCmdFlag(specYAML.String, flag, value)
	if err != nil {
		return err
	}

	// 5. Splice the engine_session_id resume cursor (same call handleResumeSession
	//    makes). Without this the new agent cold-starts and the transcript
	//    visibly jumps.
	//
	//    This used to be a second copy of the family switch, and the copies had
	//    already diverged: this one never grew antigravity's arm. It was latent
	//    rather than live — step 2 rejects any family absent from flagForField,
	//    and antigravity is absent — so an antigravity agent never reached here.
	//    Adding it to flagForField would have silently turned that into a
	//    cold-start on every mode/model flip. One table, one dispatch.
	//
	//    That warning came due the moment step 2 started resolving stewards:
	//    `spliceResume` takes a FAMILY, and a persona template matches none, so
	//    it would have returned the spec untouched and every steward's
	//    mode/model flip would have cold-started — a silent transcript break
	//    replacing a loud 422. Hence `engine`, not `kind`.
	if engineSessionID.Valid && engineSessionID.String != "" {
		mutated = spliceResume(mutated, engine, engineSessionID.String)
	}

	// 6. Best-effort host-side terminate command for the running pane;
	//    DoSpawn's session-swap branch will mark the row terminated in
	//    the same tx. The host_command queues the pane teardown; it's
	//    safe to fire-and-forget because if it fails the host's
	//    reconcile loop catches up later.
	if hostID.Valid && hostID.String != "" {
		var paneID sql.NullString
		_ = s.db.QueryRowContext(ctx,
			`SELECT pane_id FROM agents WHERE id = ?`, agentID).Scan(&paneID)
		_, _ = s.enqueueHostCommand(ctx, hostID.String, agentID,
			"terminate", map[string]any{"pane_id": paneID.String})
	}

	// 7. Spawn the new agent with the mutated spec, attached to the
	//    same session row. DoSpawn's swap branch (in.SessionID != "")
	//    handles agent termination + sessions.current_agent_id update
	//    inside a single tx, so a failure here doesn't leave a
	//    half-swapped session.
	in := spawnIn{
		ParentID:     parentID.String,
		ChildHandle:  handle.String,
		Kind:         kind.String,
		HostID:       hostID.String,
		SpawnSpec:    mutated,
		WorktreePath: worktreePath.String,
		SessionID:    sessionID.String,
	}
	if _, _, err := s.DoSpawn(ctx, teamID.String, in); err != nil {
		return fmt.Errorf("respawn-with-spec-mutation: spawn: %w", err)
	}
	return nil
}
