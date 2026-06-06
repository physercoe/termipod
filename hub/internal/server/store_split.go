package server

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// store_split.go — ADR-045 P1 step 4: the physical three-store split.
//
// The control plane (hub.db), the append-only event firehose (events.db =
// agent_events + agent_events_fts), and the derived digest read-model
// (digest.db = agent_event_digests + agent_turns) are three data classes with
// opposite shapes. SQLite's single-writer lock is per *file*, so giving the
// firehose and its fold their own files gives them their own writers — the
// structural fix for fold-vs-insert contention (plan §P1, ADR-045 D2).
//
// The moving tables are built by the existing single migration chain in hub.db
// (the chain is the unchanged control-store authority; repartitioning its 55
// mixed-concern migrations is infeasible and would break every deployed
// schema_migrations). `splitStores` relocates them into their own files by
// copying — no ATTACH at runtime (that re-couples writers and blocks the P2
// per-team shard); ATTACH is used only here, offline, by the one-shot split.
//
// The recreated tables drop the dormant `REFERENCES agents(id) ON DELETE
// CASCADE` foreign keys: the parent `agents` table stays in hub.db, so the FK
// can't live in events.db / digest.db. The cascade is dormant anyway (no agent
// hard-delete exists); it becomes an app-level cascade hook if/when one lands.

// movingTables is the set relocated out of hub.db, by destination store.
var (
	eventsMovingTables = []string{"agent_events", "agent_events_fts"}
	digestMovingTables = []string{"agent_event_digests", "agent_turns"}
)

// eventsStoreTablesDDL is the events.db schema sans the FTS virtual table — the
// real table + its indexes, ready to receive copied rows. The column set and
// indexes are the post-migration shape of agent_events (verified against a
// fully-migrated hub.db), with the agents FK dropped.
const eventsStoreTablesDDL = `
CREATE TABLE IF NOT EXISTS agent_events (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL,
    seq             INTEGER NOT NULL,
    ts              TEXT NOT NULL,
    kind            TEXT NOT NULL,
    producer        TEXT NOT NULL CHECK (producer IN ('agent','user','system','a2a')),
    payload_json    TEXT NOT NULL DEFAULT '{}',
    session_id      TEXT,
    project_id      TEXT,
    session_ordinal INTEGER,
    UNIQUE(agent_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_agent_events_agent_seq ON agent_events(agent_id, seq);
CREATE INDEX IF NOT EXISTS idx_agent_events_agent_ts  ON agent_events(agent_id, ts);
CREATE INDEX IF NOT EXISTS idx_agent_events_project_ts ON agent_events(project_id, ts) WHERE project_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_events_session    ON agent_events(session_id, ts) WHERE session_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS ux_agent_events_session_ordinal ON agent_events(session_id, session_ordinal) WHERE session_id IS NOT NULL;
`

// eventsStoreFTSDDL recreates the FTS5 index + its sync triggers. Created after
// the rows are copied so the bulk backfill (one INSERT…SELECT) builds the index
// in one pass rather than firing the per-row insert trigger during the copy.
const eventsStoreFTSDDL = `
CREATE VIRTUAL TABLE IF NOT EXISTS agent_events_fts USING fts5(
    event_id UNINDEXED,
    text,
    tokenize = 'porter unicode61'
);
CREATE TRIGGER IF NOT EXISTS agent_events_fts_insert AFTER INSERT ON agent_events BEGIN
    INSERT INTO agent_events_fts(event_id, text) VALUES (new.id, new.payload_json);
END;
CREATE TRIGGER IF NOT EXISTS agent_events_fts_delete AFTER DELETE ON agent_events BEGIN
    DELETE FROM agent_events_fts WHERE event_id = old.id;
END;
CREATE TRIGGER IF NOT EXISTS agent_events_fts_update AFTER UPDATE OF payload_json ON agent_events BEGIN
    DELETE FROM agent_events_fts WHERE event_id = old.id;
    INSERT INTO agent_events_fts(event_id, text) VALUES (new.id, new.payload_json);
END;
`

// digestStoreDDL is the digest.db schema — the post-migration shape of
// agent_event_digests + agent_turns, with the agents FK dropped.
const digestStoreDDL = `
CREATE TABLE IF NOT EXISTS agent_event_digests (
    agent_id          TEXT PRIMARY KEY,
    team_id           TEXT NOT NULL,
    schema_version    INTEGER NOT NULL DEFAULT 1,
    updated_at        TEXT NOT NULL,
    watermark_seq     INTEGER NOT NULL DEFAULT 0,
    event_count       INTEGER NOT NULL DEFAULT 0,
    turn_count        INTEGER NOT NULL DEFAULT 0,
    first_ts          TEXT NOT NULL DEFAULT '',
    last_ts           TEXT NOT NULL DEFAULT '',
    duration_ms       INTEGER NOT NULL DEFAULT 0,
    cost_usd          REAL NOT NULL DEFAULT 0,
    by_model_json     TEXT NOT NULL DEFAULT '{}',
    error_count       INTEGER NOT NULL DEFAULT 0,
    errors_json       TEXT NOT NULL DEFAULT '{}',
    tool_total        INTEGER NOT NULL DEFAULT 0,
    tool_failed       INTEGER NOT NULL DEFAULT 0,
    tools_json        TEXT NOT NULL DEFAULT '{}',
    latency_hist_json TEXT NOT NULL DEFAULT '{}',
    outcome           TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_agent_event_digests_team ON agent_event_digests(team_id);

CREATE TABLE IF NOT EXISTS agent_turns (
    agent_id      TEXT NOT NULL,
    turn_id       TEXT NOT NULL,
    team_id       TEXT NOT NULL,
    idx           INTEGER NOT NULL,
    start_seq     INTEGER NOT NULL,
    start_ts      TEXT NOT NULL,
    end_seq       INTEGER NOT NULL DEFAULT 0,
    end_ts        TEXT NOT NULL DEFAULT '',
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT '',
    cost_usd      REAL NOT NULL DEFAULT 0,
    in_tokens     INTEGER NOT NULL DEFAULT 0,
    out_tokens    INTEGER NOT NULL DEFAULT 0,
    tool_count    INTEGER NOT NULL DEFAULT 0,
    tool_failed   INTEGER NOT NULL DEFAULT 0,
    error_count   INTEGER NOT NULL DEFAULT 0,
    start_ordinal INTEGER,
    session_id    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (agent_id, turn_id)
);
CREATE INDEX IF NOT EXISTS idx_agent_turns_agent_idx     ON agent_turns(agent_id, idx);
CREATE INDEX IF NOT EXISTS idx_agent_turns_agent_seq     ON agent_turns(agent_id, start_seq);
CREATE INDEX IF NOT EXISTS idx_agent_turns_agent_ordinal ON agent_turns(agent_id, start_ordinal);
CREATE INDEX IF NOT EXISTS idx_agent_turns_session       ON agent_turns(session_id);
`

// dsnFKOff opens a connection with foreign keys OFF (the table-recreate /
// DROP pattern — DROP TABLE must not fire dormant cascades) and WAL.
func dsnFKOff(path string) string {
	return path + "?_pragma=foreign_keys(0)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
}

// storePathsFor derives the event + digest store paths that sit alongside the
// control DB (hub.db → events.db / digest.db in the same directory).
func storePathsFor(controlPath string) (eventsPath, digestPath string) {
	dir := filepath.Dir(controlPath)
	return filepath.Join(dir, "events.db"), filepath.Join(dir, "digest.db")
}

// openStorePool opens a reader (uncapped) or single-writer pool against an
// already-schema'd store file. Mirrors the control reader/writer split (New(),
// OpenWriterDB): writes serialize through one connection so they queue in Go
// instead of colliding on SQLite's per-file write lock. The moving tables carry
// no foreign keys post-split, but foreign_keys(1) is harmless and consistent.
func openStorePool(path string, writer bool) (*sql.DB, error) {
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", filepath.Base(path), err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping store %s: %w", filepath.Base(path), err)
	}
	if writer {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	return db, nil
}

// ensureEventsStore opens events.db and makes sure its schema is present
// (idempotent). Returns an open connection the caller must Close.
func ensureEventsStore(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsnFKOff(path))
	if err != nil {
		return nil, fmt.Errorf("open events store: %w", err)
	}
	if _, err := db.Exec(eventsStoreTablesDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("events store schema: %w", err)
	}
	if _, err := db.Exec(eventsStoreFTSDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("events store fts schema: %w", err)
	}
	return db, nil
}

// ensureDigestStore opens digest.db and makes sure its schema is present.
func ensureDigestStore(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsnFKOff(path))
	if err != nil {
		return nil, fmt.Errorf("open digest store: %w", err)
	}
	if _, err := db.Exec(digestStoreDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("digest store schema: %w", err)
	}
	return db, nil
}

// controlHasMovingTables reports whether hub.db still holds the moving tables —
// i.e. it has not yet been split. The serve-guard uses this to refuse booting a
// populated-but-un-split control DB (which would mis-route writes).
func controlHasMovingTables(db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		 WHERE type IN ('table','view') AND name = 'agent_events'`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// movingTableRowCount sums the rows across the moving tables still in hub.db —
// 0 means a fresh install (the migration chain just created empty tables), so
// the split is risk-free and can run automatically; non-zero is real data that
// the operator must split deliberately.
func movingTableRowCount(db *sql.DB) (int64, error) {
	var total int64
	for _, tbl := range append(append([]string{}, eventsMovingTables...), digestMovingTables...) {
		if tbl == "agent_events_fts" {
			continue // shadow of agent_events; counted via agent_events
		}
		var n int64
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&n); err != nil {
			return 0, fmt.Errorf("count %s: %w", tbl, err)
		}
		total += n
	}
	return total, nil
}

// SplitReport is the outcome of the one-shot `hub-server db split` command.
type SplitReport struct {
	AlreadySplit bool
	Events       int64
	Digests      int64
	Turns        int64
	EventsPath   string
	DigestPath   string
}

// RunStoreSplit is the offline `hub-server db split` entry point: migrate the
// control DB to current, then (if not already split) relocate the moving tables
// into events.db / digest.db beside it. It refuses to clobber pre-existing
// store files. The server must not be running. Returns a report for the CLI.
func RunStoreSplit(controlPath string) (SplitReport, error) {
	var rep SplitReport
	rep.EventsPath, rep.DigestPath = storePathsFor(controlPath)

	// OpenDB applies any pending migrations so the moving tables are at their
	// current schema before we copy them.
	db, err := OpenDB(controlPath)
	if err != nil {
		return rep, err
	}
	has, err := controlHasMovingTables(db)
	if err != nil {
		db.Close()
		return rep, err
	}
	if !has {
		db.Close()
		rep.AlreadySplit = true
		return rep, nil
	}
	// Record what we're about to move (for the CLI summary).
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_events`).Scan(&rep.Events)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_event_digests`).Scan(&rep.Digests)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_turns`).Scan(&rep.Turns)
	db.Close() // release the control handle before the offline copy/drop

	for _, p := range []string{rep.EventsPath, rep.DigestPath} {
		if _, err := os.Stat(p); err == nil {
			return rep, fmt.Errorf("%s already exists; refusing to overwrite — move it aside first", filepath.Base(p))
		}
	}
	if err := splitStores(controlPath, rep.EventsPath, rep.DigestPath); err != nil {
		return rep, err
	}
	return rep, nil
}

// splitStores performs the one-shot physical split: it copies the moving tables
// out of controlPath into freshly-created eventsPath + digestPath, verifies the
// row counts match, then drops them from controlPath. Offline only (the server
// must not be running). Idempotent-safe to call only on an un-split hub.db; the
// caller guards (controlHasMovingTables).
func splitStores(controlPath, eventsPath, digestPath string) error {
	// 1. Events store: schema → copy rows → build FTS.
	ev, err := ensureEventsStoreTablesOnly(eventsPath)
	if err != nil {
		return err
	}
	if err := copyTableVia(ev, controlPath, "agent_events",
		"id, agent_id, seq, ts, kind, producer, payload_json, session_id, project_id, session_ordinal"); err != nil {
		ev.Close()
		return err
	}
	if _, err := ev.Exec(eventsStoreFTSDDL); err != nil {
		ev.Close()
		return fmt.Errorf("events store fts schema: %w", err)
	}
	if _, err := ev.Exec(`INSERT INTO agent_events_fts(event_id, text) SELECT id, payload_json FROM agent_events`); err != nil {
		ev.Close()
		return fmt.Errorf("events store fts backfill: %w", err)
	}
	ev.Close()

	// 2. Digest store: schema → copy rows.
	dg, err := ensureDigestStore(digestPath)
	if err != nil {
		return err
	}
	if err := copyTableVia(dg, controlPath, "agent_event_digests",
		"agent_id, team_id, schema_version, updated_at, watermark_seq, event_count, turn_count, first_ts, last_ts, duration_ms, cost_usd, by_model_json, error_count, errors_json, tool_total, tool_failed, tools_json, latency_hist_json, outcome"); err != nil {
		dg.Close()
		return err
	}
	if err := copyTableVia(dg, controlPath, "agent_turns",
		"agent_id, turn_id, team_id, idx, start_seq, start_ts, end_seq, end_ts, duration_ms, status, cost_usd, in_tokens, out_tokens, tool_count, tool_failed, error_count, start_ordinal, session_id"); err != nil {
		dg.Close()
		return err
	}
	dg.Close()

	// 3. Verify copies before dropping anything from the source.
	if err := verifySplitCounts(controlPath, eventsPath, digestPath); err != nil {
		return err
	}

	// 4. Drop the moving tables from hub.db (foreign_keys OFF so the dormant
	//    cascades don't fire). Order: the FTS vtable, then agent_events (its
	//    triggers drop with it), then the digest tables.
	ctl, err := sql.Open("sqlite", dsnFKOff(controlPath))
	if err != nil {
		return fmt.Errorf("open control for drop: %w", err)
	}
	defer ctl.Close()
	for _, tbl := range []string{"agent_events_fts", "agent_events", "agent_event_digests", "agent_turns"} {
		if _, err := ctl.Exec(`DROP TABLE IF EXISTS ` + tbl); err != nil {
			return fmt.Errorf("drop %s from control: %w", tbl, err)
		}
	}
	return nil
}

// ensureEventsStoreTablesOnly opens events.db and creates only the real table +
// indexes (not the FTS index) — the split builds FTS after copying rows.
func ensureEventsStoreTablesOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsnFKOff(path))
	if err != nil {
		return nil, fmt.Errorf("open events store: %w", err)
	}
	if _, err := db.Exec(eventsStoreTablesDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("events store schema: %w", err)
	}
	return db, nil
}

// copyTableVia copies all rows of one table from the control DB (ATTACHed as
// `src`) into the already-open destination store, listing columns explicitly so
// a future column-order drift between the migrated source and the recreated
// destination can't silently misalign.
func copyTableVia(dst *sql.DB, controlPath, table, cols string) error {
	if _, err := dst.Exec(`ATTACH DATABASE ? AS src`, controlPath); err != nil {
		return fmt.Errorf("attach control: %w", err)
	}
	_, err := dst.Exec(fmt.Sprintf(`INSERT INTO %s (%s) SELECT %s FROM src.%s`, table, cols, cols, table))
	if _, derr := dst.Exec(`DETACH DATABASE src`); derr != nil && err == nil {
		err = fmt.Errorf("detach control: %w", derr)
	}
	if err != nil {
		return fmt.Errorf("copy %s: %w", table, err)
	}
	return nil
}

// verifySplitCounts asserts each moving table's row count in its new store
// equals the source, so the split never drops data it failed to copy.
func verifySplitCounts(controlPath, eventsPath, digestPath string) error {
	src, err := sql.Open("sqlite", dsnFKOff(controlPath))
	if err != nil {
		return err
	}
	defer src.Close()
	ev, err := sql.Open("sqlite", dsnFKOff(eventsPath))
	if err != nil {
		return err
	}
	defer ev.Close()
	dg, err := sql.Open("sqlite", dsnFKOff(digestPath))
	if err != nil {
		return err
	}
	defer dg.Close()
	checks := []struct {
		table string
		dst   *sql.DB
	}{
		{"agent_events", ev},
		{"agent_event_digests", dg},
		{"agent_turns", dg},
	}
	for _, c := range checks {
		var want, got int64
		if err := src.QueryRow(`SELECT COUNT(*) FROM ` + c.table).Scan(&want); err != nil {
			return fmt.Errorf("source count %s: %w", c.table, err)
		}
		if err := c.dst.QueryRow(`SELECT COUNT(*) FROM ` + c.table).Scan(&got); err != nil {
			return fmt.Errorf("dest count %s: %w", c.table, err)
		}
		if want != got {
			return fmt.Errorf("split verify %s: copied %d of %d rows", c.table, got, want)
		}
	}
	return nil
}
