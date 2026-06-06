package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/termipod/hub/internal/server"
)

// runDB dispatches `hub-server db <vacuum|migrate>` (ADR-028 plan
// W18 / W19). Both operate directly on the sqlite file — no hub
// process, no tunnel — so they are safe to run as an offline preflight.
func runDB(args []string, log *slog.Logger) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hub-server db <vacuum|migrate|split|split-teams> [flags]")
		os.Exit(2)
	}
	switch args[0] {
	case "vacuum":
		runDBVacuum(args[1:], log)
	case "migrate":
		runDBMigrate(args[1:], log)
	case "split":
		runDBSplit(args[1:], log)
	case "split-teams":
		runDBSplitTeams(args[1:], log)
	default:
		fmt.Fprintf(os.Stderr, "unknown db subcommand: %s\n", args[0])
		os.Exit(2)
	}
}

// runDBSplit performs the one-shot three-store split (ADR-045 P1 step 4):
// relocate the high-volume agent_events / agent_event_digests / agent_turns
// tables out of hub.db into events.db / digest.db beside it, so each gets its
// own SQLite writer. Offline — the server must be stopped (and a backup taken)
// first; the server refuses to serve a populated un-split hub.db. Idempotent: a
// no-op once the split has run.
func runDBSplit(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("db split", flag.ExitOnError)
	dataRoot := fs.String("data", defaultDataRoot(), "data root directory")
	dbPath := fs.String("db", "", "sqlite path (default: <data>/hub.db)")
	_ = fs.Parse(args)
	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}

	rep, err := server.RunStoreSplit(*dbPath)
	if err != nil {
		log.Error("db split failed", "err", err)
		os.Exit(1)
	}
	if rep.AlreadySplit {
		fmt.Printf("db split: %s is already split — nothing to do\n", *dbPath)
		return
	}
	fmt.Printf("db split: %s\n", *dbPath)
	fmt.Printf("  events.db: %d agent_events\n", rep.Events)
	fmt.Printf("  digest.db: %d agent_event_digests, %d agent_turns\n", rep.Digests, rep.Turns)
	fmt.Printf("  paths:     %s, %s\n", rep.EventsPath, rep.DigestPath)
}

// runDBSplitTeams performs the one-shot per-team shard migration (ADR-045 P2):
// fan the P1 global events.db / digest.db out into per-team files under
// <data>/teams/<team>/. Offline — the server must be stopped (and a backup
// taken) first; the server refuses to serve a populated global store once P2
// lands. Run `db split` first if hub.db has not been split yet.
func runDBSplitTeams(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("db split-teams", flag.ExitOnError)
	dataRoot := fs.String("data", defaultDataRoot(), "data root directory")
	dbPath := fs.String("db", "", "sqlite path (default: <data>/hub.db)")
	_ = fs.Parse(args)
	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}

	rep, err := server.RunTeamShardSplit(*dbPath)
	if err != nil {
		log.Error("db split-teams failed", "err", err)
		os.Exit(1)
	}
	if rep.AlreadySharded {
		fmt.Printf("db split-teams: no global store at %s — already sharded (nothing to do)\n", *dbPath)
		return
	}
	fmt.Printf("db split-teams: %s\n", *dbPath)
	fmt.Printf("  %d agent_events fanned across %d team(s):\n", rep.TotalEvents, len(rep.Teams))
	for _, team := range rep.Teams {
		fmt.Printf("    %s: %d events -> %s\n", team, rep.EventsByTeam[team],
			filepath.Join(*dataRoot, "teams", team))
	}
	fmt.Printf("  retired global store to events.db.pre-shard / digest.db.pre-shard\n")
}

// vacuumResult is the before/after accounting `db vacuum` reports.
type vacuumResult struct {
	BeforeBytes int64
	AfterBytes  int64
	Events      int
	AuditEvents int
}

// vacuumStats opens the DB, records the event + audit row counts, runs
// VACUUM (a whole-database rebuild — sqlite has no per-table form), and
// returns the file size on either side. Split out for testing.
func vacuumStats(dbPath string) (vacuumResult, error) {
	var res vacuumResult
	fi, err := os.Stat(dbPath)
	if err != nil {
		return res, fmt.Errorf("no database at %s: %w", dbPath, err)
	}
	res.BeforeBytes = fi.Size()

	db, err := server.OpenDB(dbPath)
	if err != nil {
		return res, err
	}
	defer db.Close()
	ctx := context.Background()
	res.Events = countRows(ctx, db, "events")
	res.AuditEvents = countRows(ctx, db, "audit_events")

	// VACUUM cannot run inside a transaction; a bare Exec satisfies that.
	if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
		return res, fmt.Errorf("vacuum: %w", err)
	}
	if fi, err := os.Stat(dbPath); err == nil {
		res.AfterBytes = fi.Size()
	}
	return res, nil
}

// countRows returns the row count of table, or 0 if the table is
// absent. table is always a fixed literal from this file's call sites,
// never caller input.
func countRows(ctx context.Context, db *sql.DB, table string) int {
	var n int
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n)
	return n
}

func runDBVacuum(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("db vacuum", flag.ExitOnError)
	dataRoot := fs.String("data", defaultDataRoot(), "data root directory")
	dbPath := fs.String("db", "", "sqlite path (default: <data>/hub.db)")
	_ = fs.Parse(args)
	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}

	res, err := vacuumStats(*dbPath)
	if err != nil {
		log.Error("db vacuum failed", "err", err)
		os.Exit(1)
	}
	const mib = float64(1 << 20)
	fmt.Printf("db vacuum: %s\n", *dbPath)
	fmt.Printf("  rows:      events=%d audit_events=%d\n", res.Events, res.AuditEvents)
	fmt.Printf("  size:      %.2f MiB -> %.2f MiB (reclaimed %.2f MiB)\n",
		float64(res.BeforeBytes)/mib, float64(res.AfterBytes)/mib,
		float64(res.BeforeBytes-res.AfterBytes)/mib)

	// The high-volume transcript data lives in the per-team event/digest shards
	// (ADR-045 P2), not hub.db — VACUUM each so a `db vacuum` actually reclaims
	// the space that retention/deletes free there.
	reclaimed, files, err := vacuumTeamStores(*dataRoot)
	if err != nil {
		log.Error("db vacuum (team shards) failed", "err", err)
		os.Exit(1)
	}
	if files > 0 {
		fmt.Printf("  shards:    vacuumed %d per-team store file(s), reclaimed %.2f MiB\n",
			files, float64(reclaimed)/mib)
	}
}

// vacuumTeamStores VACUUMs every per-team event + digest shard under
// <data>/teams/<team>/ (ADR-045 P2) and reports the bytes reclaimed across them.
// Offline, like the rest of `db vacuum` — the server must be stopped.
func vacuumTeamStores(dataRoot string) (reclaimed int64, files int, err error) {
	teamsRoot := filepath.Join(dataRoot, "teams")
	ents, rerr := os.ReadDir(teamsRoot)
	if os.IsNotExist(rerr) {
		return 0, 0, nil
	}
	if rerr != nil {
		return 0, 0, rerr
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		for _, store := range []string{"events.db", "digest.db"} {
			p := filepath.Join(teamsRoot, e.Name(), store)
			fi, statErr := os.Stat(p)
			if statErr != nil {
				continue // shard not materialized for this team yet
			}
			if verr := vacuumFile(p); verr != nil {
				return reclaimed, files, fmt.Errorf("vacuum %s: %w", p, verr)
			}
			files++
			if after, serr := os.Stat(p); serr == nil {
				reclaimed += fi.Size() - after.Size()
			}
		}
	}
	return reclaimed, files, nil
}

// vacuumFile runs a whole-database VACUUM on a single sqlite store file.
func vacuumFile(path string) error {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(context.Background(), "VACUUM")
	return err
}

// schemaVersion opens the DB (which applies any pending migrations as a
// side effect, since OpenDB always migrates) and reports the resulting
// golang-migrate bookkeeping version.
func schemaVersion(dbPath string) (version int64, dirty bool, err error) {
	db, err := server.OpenDB(dbPath)
	if err != nil {
		return 0, false, err
	}
	defer db.Close()
	var d int
	err = db.QueryRowContext(context.Background(),
		`SELECT version, dirty FROM schema_migrations`).Scan(&version, &d)
	if err != nil {
		return 0, false, err
	}
	return version, d != 0, nil
}

// runDBMigrate makes the migration step an explicit, scriptable
// preflight rather than a hidden side effect of the first DB
// connection. `hub-server serve` still auto-migrates on startup.
func runDBMigrate(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("db migrate", flag.ExitOnError)
	dataRoot := fs.String("data", defaultDataRoot(), "data root directory")
	dbPath := fs.String("db", "", "sqlite path (default: <data>/hub.db)")
	_ = fs.Parse(args)
	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}
	if err := ensureDBDir(*dbPath); err != nil {
		log.Error("prepare data dir", "err", err, "path", filepath.Dir(*dbPath))
		os.Exit(1)
	}

	version, dirty, err := schemaVersion(*dbPath)
	if err != nil {
		log.Error("db migrate failed", "err", err)
		os.Exit(1)
	}
	if dirty {
		fmt.Fprintf(os.Stderr,
			"db migrate: schema is DIRTY at version %d — a prior migration "+
				"rolled back; restore from backup or repair manually\n", version)
		os.Exit(1)
	}
	fmt.Printf("db migrate: schema up to date at version %d\n", version)
}
