// hub-server — Termipod Hub API daemon.
//
// Subcommands:
//   init                Create a fresh data root and issue an owner token.
//   serve               Run the HTTP API.
//   tokens issue        Issue a new token for an agent or user.
//   tokens list         List tokens (hash-only; plaintext is never stored).
//   reconstruct-db      Rebuild events DB from event_log/ JSONL.
//   backup              Snapshot DB + team/ + blobs/ into a tar.gz.
//   restore             Extract a backup archive into a data root.
//   seed-demo           Insert ablation-sweep-demo state (no-GPU reviewer flow).
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/termipod/hub/internal/auth"
	"github.com/termipod/hub/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	switch os.Args[1] {
	case "init":
		runInit(os.Args[2:], log)
	case "serve":
		runServe(os.Args[2:], log)
	case "tokens":
		runTokens(os.Args[2:], log)
	case "reconstruct-db":
		runReconstructDB(os.Args[2:], log)
	case "backup":
		runBackup(os.Args[2:], log)
	case "restore":
		runRestore(os.Args[2:], log)
	case "seed-demo":
		runSeedDemo(os.Args[2:], log)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `hub-server <command> [flags]

Commands:
  init              Create data root, run migrations, issue owner token.
  serve             Run the HTTP API.
  tokens issue      Issue a token. Plaintext is printed once.
  tokens list       List token kinds and hashes.
  reconstruct-db    Rebuild DB from event_log/ JSONL.
  backup            Snapshot the live DB + team/ + blobs/ into a tar.gz.
  restore           Rehydrate a fresh data root from a backup archive.
  seed-demo         Insert ablation-sweep-demo state for no-GPU reviewer flow.

Run "hub-server <command> -h" for flags.`)
}

// ---- init ----

func runInit(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dataRoot := fs.String("data", defaultDataRoot(), "data root directory")
	dbPath := fs.String("db", "", "sqlite path (default: <data>/hub.db)")
	_ = fs.Parse(args)

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}
	token, err := server.Init(*dataRoot, *dbPath)
	if err != nil {
		log.Error("init failed", "err", err)
		os.Exit(1)
	}
	fmt.Printf("Hub initialized.\n  data root: %s\n  db:        %s\n\n", *dataRoot, *dbPath)
	fmt.Printf("Owner token (shown once — store it in your TUI / mobile config):\n\n  %s\n\n", token)
}

// ---- serve ----

func runServe(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8443", "listen address")
	dataRoot := fs.String("data", defaultDataRoot(), "data root directory")
	dbPath := fs.String("db", "", "sqlite path (default: <data>/hub.db)")
	publicURL := fs.String("public-url", "", "externally reachable base URL (e.g. https://hub.example.com); used to rewrite A2A card urls to the hub relay when hosts are NAT'd. Empty = derive from request Host header.")
	_ = fs.Parse(args)

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}
	if err := ensureDBDir(*dbPath); err != nil {
		log.Error("prepare data dir", "err", err, "path", filepath.Dir(*dbPath))
		os.Exit(1)
	}
	srv, err := server.New(server.Config{
		Listen:    *listen,
		DBPath:    *dbPath,
		DataRoot:  *dataRoot,
		PublicURL: *publicURL,
		Logger:    log,
	})
	if err != nil {
		log.Error("server init failed", "err", err)
		os.Exit(1)
	}
	defer srv.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := srv.Serve(ctx); err != nil {
		log.Error("serve failed", "err", err)
		os.Exit(1)
	}
}

// ---- tokens ----

func runTokens(args []string, log *slog.Logger) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hub-server tokens <issue|list> [flags]")
		os.Exit(2)
	}
	switch args[0] {
	case "issue":
		runTokensIssue(args[1:], log)
	case "list":
		runTokensList(args[1:], log)
	default:
		fmt.Fprintf(os.Stderr, "unknown tokens subcommand: %s\n", args[0])
		os.Exit(2)
	}
}

func runTokensIssue(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("tokens issue", flag.ExitOnError)
	dataRoot := fs.String("data", defaultDataRoot(), "data root directory")
	dbPath := fs.String("db", "", "sqlite path (default: <data>/hub.db)")
	kind := fs.String("kind", "agent", "token kind: owner|agent|host|user")
	team := fs.String("team", "default", "team scope")
	role := fs.String("role", "agent", "role within team")
	agentID := fs.String("agent-id", "", "agent id to bind the token to (for kind=agent / MCP)")
	handle := fs.String("handle", "", "display handle for role=principal tokens (e.g. physercoe); shown on the Members tab")
	_ = fs.Parse(args)

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}
	if err := ensureDBDir(*dbPath); err != nil {
		log.Error("prepare data dir", "err", err, "path", filepath.Dir(*dbPath))
		os.Exit(1)
	}
	db, err := openDBWithHint(*dbPath, log)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	plain := auth.NewToken()
	scopeMap := map[string]any{"team": *team, "role": *role}
	if *agentID != "" {
		scopeMap["agent_id"] = *agentID
	}
	if *handle != "" {
		scopeMap["handle"] = *handle
	}
	scope, _ := json.Marshal(scopeMap)
	if err := auth.InsertToken(context.Background(), db, *kind, string(scope), plain, server.NewID(), server.NowUTC()); err != nil {
		log.Error("insert token", "err", err)
		os.Exit(1)
	}
	fmt.Printf("Issued %s token (shown once):\n\n  %s\n\n", *kind, plain)
}

func runTokensList(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("tokens list", flag.ExitOnError)
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
	db, err := openDBWithHint(*dbPath, log)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	rows, err := db.QueryContext(context.Background(),
		`SELECT id, kind, scope_json, created_at,
		        COALESCE(expires_at, ''), COALESCE(revoked_at, '')
		 FROM auth_tokens ORDER BY created_at`)
	if err != nil {
		log.Error("query", "err", err)
		os.Exit(1)
	}
	defer rows.Close()
	fmt.Printf("%-28s %-8s %-30s %-30s %s\n", "id", "kind", "created_at", "expires_at", "revoked_at")
	for rows.Next() {
		var id, kind, scope, created, expires, revoked string
		if err := rows.Scan(&id, &kind, &scope, &created, &expires, &revoked); err != nil {
			log.Error("scan", "err", err)
			os.Exit(1)
		}
		fmt.Printf("%-28s %-8s %-30s %-30s %s\n", id, kind, created, expires, revoked)
		_ = scope
	}
}

// ---- reconstruct-db ----

// runReconstructDB rebuilds an events DB by replaying every line under
// <data>/event_log/*.jsonl into -db. The target is opened with migrations
// applied; rows use ON CONFLICT DO NOTHING so it's safe to re-run.
//
// Typical use: the live DB is lost / corrupted. Point -db at a fresh file
// and let the JSONL log be the source of truth.
func runReconstructDB(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("reconstruct-db", flag.ExitOnError)
	dataRoot := fs.String("data", defaultDataRoot(), "data root directory (contains event_log/)")
	dbPath := fs.String("db", "", "target sqlite path (default: <data>/hub.db)")
	_ = fs.Parse(args)

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}
	if err := ensureDBDir(*dbPath); err != nil {
		log.Error("prepare data dir", "err", err, "path", filepath.Dir(*dbPath))
		os.Exit(1)
	}
	files, inserted, skipped, err := server.ReconstructDB(context.Background(), *dataRoot, *dbPath)
	if err != nil {
		log.Error("reconstruct failed", "err", err, "files", files, "inserted", inserted, "skipped", skipped)
		os.Exit(1)
	}
	fmt.Printf("reconstruct-db: replayed %d file(s); inserted=%d skipped=%d\n", files, inserted, skipped)
}

// ---- backup ----

// runBackup writes a consistent snapshot of hub.db plus the team/ and
// blobs/ directories into a single tar.gz. The DB snapshot uses VACUUM
// INTO so it's safe to run while hub-server is live.
func runBackup(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dataRoot := fs.String("data", defaultDataRoot(), "data root directory")
	dbPath := fs.String("db", "", "sqlite path (default: <data>/hub.db)")
	out := fs.String("to", "", "output archive path (e.g. ~/backups/hub-2026-04-27.tar.gz)")
	_ = fs.Parse(args)

	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: hub-server backup --to <path>")
		os.Exit(2)
	}
	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}
	if err := server.Backup(context.Background(), *dbPath, *dataRoot, *out); err != nil {
		log.Error("backup failed", "err", err)
		os.Exit(1)
	}
	stat, _ := os.Stat(*out)
	size := int64(0)
	if stat != nil {
		size = stat.Size()
	}
	fmt.Printf("backup: wrote %s (%d bytes)\n", *out, size)
}

// ---- restore ----

// runRestore extracts an archive into a data root and runs migrations
// on the restored DB. Refuses to overwrite a non-empty data root unless
// --force is passed; that guard is the difference between "I lost my
// hub" and "I lost my hub twice".
func runRestore(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	from := fs.String("from", "", "archive path produced by `hub-server backup`")
	dataRoot := fs.String("data", defaultDataRoot(), "destination data root")
	force := fs.Bool("force", false, "overwrite an existing non-empty data root")
	_ = fs.Parse(args)

	if *from == "" {
		fmt.Fprintln(os.Stderr, "usage: hub-server restore --from <path> [--data <dir>] [--force]")
		os.Exit(2)
	}
	if err := server.Restore(context.Background(), *from, *dataRoot, *force); err != nil {
		if errors.Is(err, server.ErrDataRootNotEmpty) {
			log.Error("restore refused", "err", err, "data", *dataRoot,
				"hint", "pass --force to overwrite, or point --data at a fresh directory")
			os.Exit(1)
		}
		log.Error("restore failed", "err", err)
		os.Exit(1)
	}
	fmt.Printf("restore: extracted %s into %s\n", *from, *dataRoot)
	fmt.Printf("note: host-runner tokens reference the old hub URL; reissue them via `hub-server tokens issue` if you've moved hosts.\n")
}

// ---- seed-demo ----

// runSeedDemo inserts a ready-to-review "ablation-sweep-demo" project into an
// already-initialized hub DB. See server.SeedDemo for what it writes.
// Intended for reviewers who want to explore the mobile UI (projects / runs /
// docs / reviews / inbox) without running nanoGPT on a GPU.
//
// Idempotent — running twice reports the existing project and makes no
// changes.
func runSeedDemo(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("seed-demo", flag.ExitOnError)
	dataRoot := fs.String("data", defaultDataRoot(), "data root directory")
	dbPath := fs.String("db", "", "sqlite path (default: <data>/hub.db)")
	reset := fs.Bool("reset", false,
		"delete the existing demo project for the chosen shape "+
			"(and its dependent rows) before re-inserting. Use when "+
			"the seed content has evolved and you want to refresh a "+
			"previously-seeded hub.")
	shape := fs.String("shape", "ablation",
		"which demo to seed: 'ablation' = original Candidate A "+
			"single-phase nanoGPT sweep (run-the-demo.md); 'lifecycle' "+
			"= 5-phase research lifecycle (run-lifecycle-demo.md), "+
			"with phase 1 done, phase 2 in_progress, gate pending.")
	_ = fs.Parse(args)

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}
	if err := ensureDBDir(*dbPath); err != nil {
		log.Error("prepare data dir", "err", err, "path", filepath.Dir(*dbPath))
		os.Exit(1)
	}
	db, err := openDBWithHint(*dbPath, log)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()

	switch *shape {
	case "ablation":
		runSeedAblation(ctx, db, *dataRoot, *reset, log)
	case "lifecycle":
		runSeedLifecycle(ctx, db, *reset, log)
	default:
		log.Error("unknown shape", "shape", *shape)
		fmt.Fprintf(os.Stderr, "seed-demo: unknown shape %q (valid: ablation, lifecycle)\n", *shape)
		os.Exit(2)
	}
}

func runSeedAblation(ctx context.Context, db *sql.DB, dataRoot string, reset bool, log *slog.Logger) {
	var wasReset bool
	if reset {
		deleted, err := server.ResetDemo(ctx, db)
		if err != nil {
			log.Error("seed-demo reset failed", "err", err)
			os.Exit(1)
		}
		wasReset = deleted
		if deleted {
			fmt.Println("seed-demo: reset — deleted prior ablation-demo rows.")
		} else {
			fmt.Println("seed-demo: reset — no prior ablation-demo rows to delete.")
		}
	}
	res, err := server.SeedDemo(ctx, db, dataRoot)
	if err != nil {
		log.Error("seed-demo failed", "err", err)
		os.Exit(1)
	}
	if res.Skipped {
		fmt.Printf("seed-demo: project already exists (id=%s) — nothing written. "+
			"Pass -reset to refresh.\n", res.ProjectID)
		return
	}
	res.Reset = wasReset
	action := "inserted"
	if wasReset {
		action = "reset + re-inserted"
	}
	fmt.Printf("seed-demo: %s ablation-demo state.\n  project:    %s\n  runs:       %d\n  document:   %s\n  review:     %s (pending)\n  attention:  %s (open decision)\n  images:     %d (samples/generations × 3 per run)\n  artifacts:  %d (checkpoint + eval_curve per run)\n",
		action, res.ProjectID, len(res.RunIDs), res.DocumentID, res.ReviewID, res.Attention, res.ImageCount, res.ArtifactCount)
}

func runSeedLifecycle(ctx context.Context, db *sql.DB, reset bool, log *slog.Logger) {
	var wasReset bool
	if reset {
		deleted, err := server.ResetLifecycleDemo(ctx, db)
		if err != nil {
			log.Error("seed-demo reset (lifecycle) failed", "err", err)
			os.Exit(1)
		}
		wasReset = deleted
		if deleted {
			fmt.Println("seed-demo: reset — deleted prior lifecycle-demo rows.")
		} else {
			fmt.Println("seed-demo: reset — no prior lifecycle-demo rows to delete.")
		}
	}
	res, err := server.SeedLifecycleDemo(ctx, db)
	if err != nil {
		log.Error("seed-demo (lifecycle) failed", "err", err)
		os.Exit(1)
	}
	if res.Skipped {
		fmt.Printf("seed-demo: lifecycle portfolio already exists (idea-project id=%s) — "+
			"nothing written. Pass -reset to refresh.\n", res.IdeaProjectID)
		return
	}
	res.Reset = wasReset
	action := "inserted"
	if wasReset {
		action = "reset + re-inserted"
	}
	fmt.Printf("seed-demo: %s lifecycle-demo portfolio (5 phase-staged research projects).\n",
		action)
	rows := []struct {
		label, id, hint string
	}{
		{"idea         ", res.IdeaProjectID, "hero=idea_conversation, scope-criterion pending"},
		{"lit-review   ", res.LitReviewProjectID, "hero=deliverable_focus, doc in-review, metric met"},
		{"method       ", res.MethodProjectID, "hero=deliverable_focus, doc ratified, gate met"},
		{"experiment   ", res.ExperimentProjectID, "hero=experiment_dash, mixed components, criterion failed"},
		{"paper        ", res.PaperProjectID, "hero=paper_acceptance, doc in-review, gate waived"},
	}
	for _, r := range rows {
		fmt.Printf("  %s %s — %s\n", r.label, r.id, r.hint)
	}
	fmt.Printf("  ----\n")
	fmt.Printf("  totals:        %d deliverables, %d criteria (",
		res.DeliverableCount, res.CriterionCount)
	first := true
	for _, k := range []string{"pending", "met", "failed", "waived"} {
		if v := res.CriteriaByState[k]; v > 0 {
			if !first {
				fmt.Print(", ")
			}
			fmt.Printf("%d %s", v, k)
			first = false
		}
	}
	fmt.Printf("), %d typed docs, %d artifacts, %d runs, %d attention items, %d audits\n",
		res.DocumentCount, res.ArtifactCount, res.RunCount,
		res.AttentionItemCount, res.AuditCount)
}

// ---- helpers ----

// ensureDBDir guarantees the directory holding the sqlite file exists,
// because OpenDB will not create parents on its own. Without this the
// modernc driver surfaces SQLITE_CANTOPEN (code 14) as the unhelpful
// string "out of memory (14)" and people chase ghosts.
func ensureDBDir(dbPath string) error {
	return os.MkdirAll(filepath.Dir(dbPath), 0o700)
}

// openDBWithHint wraps server.OpenDB to turn the bare driver error into
// something operators can act on when the path is unreadable / unwritable
// / missing. Exit status is left to the caller.
func openDBWithHint(path string, log *slog.Logger) (_ *serverDB, err error) {
	db, err := server.OpenDB(path)
	if err != nil {
		hint := ""
		msg := err.Error()
		// modernc.org/sqlite reports CANTOPEN with an incorrect "out of
		// memory (14)" prefix. Match on the numeric tail so we catch it
		// even if upstream ever fixes the string.
		if strings.Contains(msg, "(14)") || strings.Contains(msg, "unable to open database file") {
			hint = "sqlite cannot open " + path +
				" — ensure the directory exists and is writable by this user (did you run `hub-server init` first?)"
		}
		if hint != "" {
			log.Error("open db", "err", msg, "hint", hint)
		} else {
			log.Error("open db", "err", msg)
		}
		return nil, err
	}
	return db, nil
}

// serverDB is an alias so the helper signature matches the caller's
// expectation (*sql.DB) without a second import in this file.
type serverDB = sql.DB

func defaultDataRoot() string {
	if v := os.Getenv("HUB_DATA"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "./hub-data"
	}
	return filepath.Join(home, "hub")
}
