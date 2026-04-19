package server

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// EventLogRow is the canonical on-disk shape for a replayable event.
// Field order is the same as the events table column order so that
// reconstructing doesn't need per-field glue code on the way back in.
type EventLogRow struct {
	ID              string  `json:"id"`
	SchemaVersion   int     `json:"schema_version"`
	TS              string  `json:"ts"`
	ReceivedTS      string  `json:"received_ts"`
	ChannelID       string  `json:"channel_id"`
	Type            string  `json:"type"`
	FromID          *string `json:"from_id,omitempty"`
	ToIDsJSON       string  `json:"to_ids_json"`
	PartsJSON       string  `json:"parts_json"`
	TaskID          *string `json:"task_id,omitempty"`
	CorrelationID   *string `json:"correlation_id,omitempty"`
	PaneRefJSON     *string `json:"pane_ref_json,omitempty"`
	UsageTokensJSON *string `json:"usage_tokens_json,omitempty"`
	MetadataJSON    string  `json:"metadata_json"`
}

// eventLogMu serializes appends within a single process. JSONL files are
// append-only so concurrent writes are corruption-safe on most OSes, but a
// mutex also keeps the line-boundary invariant on non-POSIX filesystems.
var eventLogMu sync.Mutex

// logEventJSONL reads the row we just inserted and appends one JSONL line
// to <dataRoot>/event_log/<YYYY-MM-DD>.jsonl. Errors are swallowed with a
// warn so a broken disk doesn't fail the request — the DB row is still the
// source of truth at write time.
func (s *Server) logEventJSONL(ctx context.Context, eventID string) {
	if s.cfg.DataRoot == "" {
		return
	}
	row, err := readEventRow(ctx, s.db, eventID)
	if err != nil {
		s.log.Warn("event log: read row", "id", eventID, "err", err)
		return
	}
	if err := appendEventJSONL(s.cfg.DataRoot, row); err != nil {
		s.log.Warn("event log: append", "id", eventID, "err", err)
	}
}

func readEventRow(ctx context.Context, db *sql.DB, id string) (EventLogRow, error) {
	var r EventLogRow
	var fromID, taskID, correlationID, paneRef, usage sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT id, schema_version, ts, received_ts, channel_id, type,
		       from_id, to_ids_json, parts_json, task_id, correlation_id,
		       pane_ref_json, usage_tokens_json, metadata_json
		FROM events WHERE id = ?`, id,
	).Scan(
		&r.ID, &r.SchemaVersion, &r.TS, &r.ReceivedTS, &r.ChannelID, &r.Type,
		&fromID, &r.ToIDsJSON, &r.PartsJSON, &taskID, &correlationID,
		&paneRef, &usage, &r.MetadataJSON,
	)
	if err != nil {
		return r, err
	}
	r.FromID = nullToPtr(fromID)
	r.TaskID = nullToPtr(taskID)
	r.CorrelationID = nullToPtr(correlationID)
	r.PaneRefJSON = nullToPtr(paneRef)
	r.UsageTokensJSON = nullToPtr(usage)
	return r, nil
}

func nullToPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func appendEventJSONL(dataRoot string, row EventLogRow) error {
	dir := filepath.Join(dataRoot, "event_log")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// File is named after the *received_ts* date (the hub's clock), so a
	// replay in the same order as received_ts also replays in file order.
	day := row.ReceivedTS
	if len(day) >= 10 {
		day = day[:10]
	} else {
		day = time.Now().UTC().Format("2006-01-02")
	}
	path := filepath.Join(dir, day+".jsonl")

	line, err := json.Marshal(row)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	eventLogMu.Lock()
	defer eventLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(line)
	return err
}

// ReconstructDB rebuilds a SQLite events table by replaying every JSONL
// line under <dataRoot>/event_log/. The destination DB must be freshly
// migrated (empty events table). Rows are inserted with ON CONFLICT DO
// NOTHING so partial-run recovery is safe.
//
// Returns (filesRead, rowsInserted, rowsSkipped, err).
func ReconstructDB(ctx context.Context, dataRoot, dbPath string) (int, int, int, error) {
	dir := filepath.Join(dataRoot, "event_log")
	files, err := listJSONLFiles(dir)
	if err != nil {
		return 0, 0, 0, err
	}
	if len(files) == 0 {
		return 0, 0, 0, fmt.Errorf("no JSONL files found under %s", dir)
	}

	db, err := OpenDB(dbPath)
	if err != nil {
		return 0, 0, 0, err
	}
	defer db.Close()

	// Pin a single conn for the whole replay. Events reference channels /
	// agents / tasks by FK, and the event log only carries events — a
	// freshly migrated DB has no parent rows to satisfy the constraints.
	// reconstruct-db is a last-resort recovery path: operators re-seed the
	// control-plane tables (channels, agents, hosts, projects) from their
	// own backups or re-init, then replay the event log on top. Disable FK
	// enforcement on this conn only; PRAGMA is per-connection, so we can't
	// just set it on db and hope it sticks across the pool.
	conn, err := db.Conn(ctx)
	if err != nil {
		return len(files), 0, 0, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return len(files), 0, 0, fmt.Errorf("disable fks: %w", err)
	}

	ins := `
		INSERT INTO events (
			id, schema_version, ts, received_ts, channel_id, type,
			from_id, to_ids_json, parts_json, task_id, correlation_id,
			pane_ref_json, usage_tokens_json, metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`
	stmt, err := conn.PrepareContext(ctx, ins)
	if err != nil {
		return len(files), 0, 0, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	var inserted, skipped int
	for _, p := range files {
		n, s, err := replayJSONLFile(ctx, stmt, p)
		inserted += n
		skipped += s
		if err != nil {
			return len(files), inserted, skipped, fmt.Errorf("%s: %w", p, err)
		}
	}
	// Rebuild FTS: triggers fire on INSERT so the shadow table is already
	// populated. But if callers run this against a DB that already had
	// events (e.g. partial re-run), the FTS rows for the new inserts will
	// still be there. Nothing to do explicitly.
	return len(files), inserted, skipped, nil
}

func listJSONLFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errorsIsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out, nil
}

func errorsIsNotExist(err error) bool {
	return err != nil && (os.IsNotExist(err) || err == fs.ErrNotExist)
}

func replayJSONLFile(ctx context.Context, stmt *sql.Stmt, path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Some event payloads are large (multi-KB parts_json); bump the buffer.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var inserted, skipped int
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var row EventLogRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return inserted, skipped, fmt.Errorf("line %d: %w", lineNo, err)
		}
		res, err := stmt.ExecContext(ctx,
			row.ID, row.SchemaVersion, row.TS, row.ReceivedTS, row.ChannelID, row.Type,
			ptrOrNil(row.FromID), row.ToIDsJSON, row.PartsJSON,
			ptrOrNil(row.TaskID), ptrOrNil(row.CorrelationID),
			ptrOrNil(row.PaneRefJSON), ptrOrNil(row.UsageTokensJSON),
			row.MetadataJSON,
		)
		if err != nil {
			return inserted, skipped, fmt.Errorf("line %d: exec: %w", lineNo, err)
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			inserted++
		} else {
			skipped++
		}
	}
	if err := scanner.Err(); err != nil {
		return inserted, skipped, err
	}
	return inserted, skipped, nil
}

func ptrOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
