package server

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/termipod/hub/internal/buildinfo"
	"github.com/termipod/hub/internal/hostrunner"
)

// hubStatsTables are the tables surfaced in the /v1/hub/stats db.tables
// block — the ones a manager glance cares about. We deliberately don't
// enumerate every migration table because the mobile tile only renders
// a few rows; new tables can be added here when they become load-bearing.
var hubStatsTables = []string{
	"agent_events",
	"audit_events",
	"sessions",
	"agents",
	"documents",
	"attention_items",
}

// startedAt is captured at first call so uptime_seconds reports the
// server lifetime rather than process load time. Using package init
// would tie uptime to import order; lazy-init keeps it tied to the
// first observable request.
var (
	startedAtOnce sync.Once
	startedAt     time.Time
)

// rowCountCache memoizes the slow per-table COUNT(*) for the 30s TTL
// referenced by ADR-022 / insights-phase-1.md. agent_events scans get
// expensive once a project has been running for a while, so we don't
// want to re-scan on every Hub Detail refresh.
type rowCountCache struct {
	mu       sync.Mutex
	taken    time.Time
	rows     map[string]int64
	bytes    map[string]int64
	dbBytes  int64
	walBytes int64
	schemaV  int
}

var hubStatsCache = &rowCountCache{}

const hubStatsTTL = 30 * time.Second

func (s *Server) handleHubStats(w http.ResponseWriter, r *http.Request) {
	startedAtOnce.Do(func() { startedAt = time.Now() })

	ctx := r.Context()
	machine := hostrunner.ProbeHostInfo(ctx)

	tablesBlock, dbBytes, walBytes, schemaV := readDBStats(ctx, s.db)

	out := map[string]any{
		"version":        buildinfo.Version,
		"uptime_seconds": int64(time.Since(startedAt).Seconds()),
		"machine": map[string]any{
			"os":        machine.OS,
			"arch":      machine.Arch,
			"cpu_count": machine.CPUCount,
			"mem_bytes": machine.MemBytes,
			"kernel":    machine.Kernel,
			"hostname":  machine.Hostname,
		},
		"db": map[string]any{
			"size_bytes":     dbBytes,
			"wal_bytes":      walBytes,
			"schema_version": schemaV,
			"tables":         tablesBlock,
		},
		"live": readLiveBlock(ctx, s.db, s.bus),
	}
	if buildinfo.Commit != "" {
		out["commit"] = buildinfo.Commit
	}
	writeJSON(w, http.StatusOK, out)
}

// readDBStats returns per-table {rows, bytes} plus aggregate db size,
// WAL size, and schema version. Honors the 30s row-count cache: a
// concurrent caller that arrives mid-rebuild gets the previous snapshot
// rather than serializing on the slow scan.
func readDBStats(ctx context.Context, db *sql.DB) (tables map[string]map[string]int64, dbBytes, walBytes int64, schemaVersion int) {
	hubStatsCache.mu.Lock()
	defer hubStatsCache.mu.Unlock()

	if !hubStatsCache.taken.IsZero() && time.Since(hubStatsCache.taken) < hubStatsTTL {
		return cloneTables(hubStatsCache.rows, hubStatsCache.bytes), hubStatsCache.dbBytes, hubStatsCache.walBytes, hubStatsCache.schemaV
	}

	rows := map[string]int64{}
	for _, t := range hubStatsTables {
		var n int64
		// Identifiers can't be parameterized; the table list is a
		// hard-coded allowlist above so injection isn't reachable.
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+t).Scan(&n); err != nil {
			continue
		}
		rows[t] = n
	}

	bytes := readDBStatBytes(ctx, db)

	var pageCount, pageSize int64
	_ = db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount)
	_ = db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize)
	dbBytes = pageCount * pageSize

	var walPages int64
	_ = db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(new(int), new(int), &walPages)
	walBytes = walPages * pageSize

	var sv int
	_ = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&sv)
	schemaVersion = sv

	hubStatsCache.taken = time.Now()
	hubStatsCache.rows = rows
	hubStatsCache.bytes = bytes
	hubStatsCache.dbBytes = dbBytes
	hubStatsCache.walBytes = walBytes
	hubStatsCache.schemaV = schemaVersion

	return cloneTables(rows, bytes), dbBytes, walBytes, schemaVersion
}

// readDBStatBytes returns per-table byte usage from the dbstat virtual
// table when SQLite was compiled with -DSQLITE_ENABLE_DBSTAT_VTAB.
// Returns nil when dbstat is unavailable; the caller surfaces only
// db.size_bytes in that case (per ADR-022 / W1 plan).
func readDBStatBytes(ctx context.Context, db *sql.DB) map[string]int64 {
	rows, err := db.QueryContext(ctx, `
		SELECT name, SUM(pgsize) FROM dbstat GROUP BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var bytes sql.NullInt64
		if err := rows.Scan(&name, &bytes); err != nil {
			continue
		}
		if bytes.Valid {
			out[name] = bytes.Int64
		}
	}
	return out
}

func cloneTables(rows, bytes map[string]int64) map[string]map[string]int64 {
	out := make(map[string]map[string]int64, len(rows))
	for name, n := range rows {
		entry := map[string]int64{"rows": n}
		if b, ok := bytes[name]; ok {
			entry["bytes"] = b
		}
		out[name] = entry
	}
	return out
}

// readLiveBlock returns the realtime counters for the stats endpoint.
// active_agents counts agents in 'running' status; open_sessions counts
// sessions with status 'active'. SSE subscribers come from the in-process
// event bus subscriber map.
func readLiveBlock(ctx context.Context, db *sql.DB, bus *eventBus) map[string]any {
	out := map[string]any{
		"active_agents":   countOrZero(ctx, db, "SELECT count(*) FROM agents WHERE status = 'running'"),
		"open_sessions":   countOrZero(ctx, db, "SELECT count(*) FROM sessions WHERE status = 'active'"),
		"sse_subscribers": bus.SubscriberCount(),
	}
	return out
}

func countOrZero(ctx context.Context, db *sql.DB, q string) int64 {
	var n int64
	if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return 0
	}
	return n
}
