package server

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

// store_shard_split.go — ADR-045 P2: the one-shot `hub-server db split-teams`
// migration. It takes a P1-split deployment (one global events.db + digest.db
// beside hub.db) and fans those rows out into per-team files under
// dataRoot/teams/<team>/{events.db,digest.db}, so each team gets its own SQLite
// writer (plan §P2).
//
// Like the P1 split it is OFFLINE and copy-based — the server must be stopped
// (and a backup taken); the server refuses to serve a populated global store
// once P2 lands. ATTACH is used only here, offline, never at runtime (runtime
// ATTACH re-couples writers and defeats the shard).
//
// The event store has no team_id column (team is a property of the agent), so
// the events copy JOINs the global events.db to hub.db's `agents` to partition
// by team. The digest tables carry team_id already, so they filter directly. A
// global event whose agent has no team row would be silently dropped, so the
// copy verifies the per-team total equals the source and refuses to finish (and
// to delete the source) on a mismatch.

// TeamSplitReport is the outcome of `hub-server db split-teams`.
type TeamSplitReport struct {
	AlreadySharded bool             // no global store present → nothing to do
	Teams          []string         // teams that received a shard, sorted
	EventsByTeam   map[string]int64 // agent_events copied per team
	TotalEvents    int64            // source agent_events count (for the verify)
}

// RunTeamShardSplit migrates a P1 global events.db/digest.db into per-team
// shards. controlPath is the hub.db path; the global stores sit beside it and
// the per-team files land under <dir(hub.db)>/teams/<team>/. Returns a report
// for the CLI. The server must not be running.
func RunTeamShardSplit(controlPath string) (TeamSplitReport, error) {
	var rep TeamSplitReport
	rep.EventsByTeam = map[string]int64{}
	eventsPath, digestPath := storePathsFor(controlPath)

	// A global store must exist (a P1-split deployment). If it doesn't, either
	// the hub is fresh / already sharded (nothing to do) or it was never P1
	// split — tell the operator to run `db split` first in that case.
	if _, err := os.Stat(eventsPath); err != nil {
		has, herr := controlStillHoldsMovingTables(controlPath)
		if herr == nil && has {
			return rep, fmt.Errorf("hub.db has not been split into events.db/digest.db yet; run `hub-server db split` first, then `db split-teams`")
		}
		rep.AlreadySharded = true
		return rep, nil
	}

	// OpenDB migrates hub.db to current, then read the team set + the source
	// event count (for the post-copy verify).
	db, err := OpenDB(controlPath)
	if err != nil {
		return rep, err
	}
	teams, err := allTeamIDs(db)
	db.Close()
	if err != nil {
		return rep, err
	}
	if total, err := countTableRows(eventsPath, "agent_events"); err == nil {
		rep.TotalEvents = total
	} else {
		return rep, err
	}

	teamsRoot := filepath.Join(filepath.Dir(controlPath), "teams")
	var copied int64
	for _, team := range teams {
		n, err := shardOneTeam(controlPath, eventsPath, digestPath, teamsRoot, team)
		if err != nil {
			return rep, fmt.Errorf("shard team %s: %w", team, err)
		}
		if n > 0 {
			rep.Teams = append(rep.Teams, team)
			rep.EventsByTeam[team] = n
		}
		copied += n
	}
	sort.Strings(rep.Teams)

	// Verify every source event landed in exactly one team shard before we
	// touch the source — an agent_events row whose agent has no team (or no
	// agents row at all) would otherwise be silently lost.
	if copied != rep.TotalEvents {
		return rep, fmt.Errorf("split-teams verify: copied %d of %d agent_events across teams (missing rows belong to agents with no team row); source left intact", copied, rep.TotalEvents)
	}

	// Move the global stores aside (recoverable) now that the shards hold every
	// row; the serve-guard keys on the global store's presence.
	for _, p := range []string{eventsPath, digestPath} {
		if _, err := os.Stat(p); err == nil {
			if err := os.Rename(p, p+".pre-shard"); err != nil {
				return rep, fmt.Errorf("retire %s: %w", filepath.Base(p), err)
			}
		}
	}
	return rep, nil
}

// shardOneTeam copies one team's agent_events (via a JOIN to hub.db's agents)
// and its digest/turns rows (filtered by team_id) into the team's shard files,
// then builds the events FTS. Returns the number of agent_events copied.
func shardOneTeam(controlPath, eventsPath, digestPath, teamsRoot, team string) (int64, error) {
	dir := filepath.Join(teamsRoot, team)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("team dir: %w", err)
	}
	teamEvents := filepath.Join(dir, "events.db")
	teamDigest := filepath.Join(dir, "digest.db")
	for _, p := range []string{teamEvents, teamDigest} {
		if _, err := os.Stat(p); err == nil {
			return 0, fmt.Errorf("%s already exists; move it aside first", p)
		}
	}

	// Events: JOIN the global event store to control's agents to select this
	// team's rows. The per-team file is created tables-only; FTS is built after
	// the bulk copy so the per-row trigger doesn't fire during it.
	ev, err := ensureEventsStoreTablesOnly(teamEvents)
	if err != nil {
		return 0, err
	}
	const evCols = "id, agent_id, seq, ts, kind, producer, payload_json, session_id, project_id, session_ordinal"
	if _, err := ev.Exec(`ATTACH DATABASE ? AS evsrc`, eventsPath); err != nil {
		ev.Close()
		return 0, fmt.Errorf("attach events: %w", err)
	}
	if _, err := ev.Exec(`ATTACH DATABASE ? AS ctl`, controlPath); err != nil {
		ev.Close()
		return 0, fmt.Errorf("attach control: %w", err)
	}
	if _, err := ev.Exec(`INSERT INTO agent_events (`+evCols+`)
		SELECT `+prefixCols(evCols, "e")+`
		  FROM evsrc.agent_events e
		  JOIN ctl.agents a ON a.id = e.agent_id
		 WHERE a.team_id = ?`, team); err != nil {
		ev.Close()
		return 0, fmt.Errorf("copy events: %w", err)
	}
	var n int64
	_ = ev.QueryRow(`SELECT COUNT(*) FROM agent_events`).Scan(&n)
	if _, err := ev.Exec(eventsStoreFTSDDL); err != nil {
		ev.Close()
		return 0, fmt.Errorf("events fts schema: %w", err)
	}
	if _, err := ev.Exec(`INSERT INTO agent_events_fts(event_id, text) SELECT id, payload_json FROM agent_events`); err != nil {
		ev.Close()
		return 0, fmt.Errorf("events fts backfill: %w", err)
	}
	ev.Close()

	// Digest + turns: team_id is on the rows, so a direct filtered copy.
	dg, err := ensureDigestStore(teamDigest)
	if err != nil {
		return 0, err
	}
	if _, err := dg.Exec(`ATTACH DATABASE ? AS dgsrc`, digestPath); err != nil {
		dg.Close()
		return 0, fmt.Errorf("attach digest: %w", err)
	}
	const digCols = "agent_id, team_id, schema_version, updated_at, watermark_seq, event_count, turn_count, first_ts, last_ts, duration_ms, cost_usd, by_model_json, error_count, errors_json, tool_total, tool_failed, tools_json, latency_hist_json, outcome"
	const turnCols2 = "agent_id, turn_id, team_id, idx, start_seq, start_ts, end_seq, end_ts, duration_ms, status, cost_usd, in_tokens, out_tokens, tool_count, tool_failed, error_count, start_ordinal, session_id"
	if _, err := dg.Exec(`INSERT INTO agent_event_digests (`+digCols+`) SELECT `+digCols+` FROM dgsrc.agent_event_digests WHERE team_id = ?`, team); err != nil {
		dg.Close()
		return 0, fmt.Errorf("copy digests: %w", err)
	}
	if _, err := dg.Exec(`INSERT INTO agent_turns (`+turnCols2+`) SELECT `+turnCols2+` FROM dgsrc.agent_turns WHERE team_id = ?`, team); err != nil {
		dg.Close()
		return 0, fmt.Errorf("copy turns: %w", err)
	}
	dg.Close()
	return n, nil
}

// prefixCols rewrites a comma-separated column list "a, b, c" to "p.a, p.b, p.c"
// for use in a JOINed SELECT.
func prefixCols(cols, alias string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// allTeamIDs reads the team id set from the control store.
func allTeamIDs(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT id FROM teams ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// countTableRows opens a store file read-only-ish and counts one table.
func countTableRows(path, table string) (int64, error) {
	db, err := sql.Open("sqlite", dsnFKOff(path))
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var n int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		return 0, fmt.Errorf("count %s in %s: %w", table, filepath.Base(path), err)
	}
	return n, nil
}

// controlStillHoldsMovingTables reports whether hub.db itself still has the
// moving tables (a never-P1-split deployment).
func controlStillHoldsMovingTables(controlPath string) (bool, error) {
	db, err := sql.Open("sqlite", dsnFKOff(controlPath))
	if err != nil {
		return false, err
	}
	defer db.Close()
	return controlHasMovingTables(db)
}
