// hub-server — Termipod Hub API daemon.
//
// Subcommands:
//   init                Create a fresh data root and issue an owner token.
//   serve               Run the HTTP API.
//   tokens issue        Issue a new token for an agent or user.
//   tokens list         List tokens (hash-only; plaintext is never stored).
//   reconstruct-db      Rebuild events DB from event_log/ JSONL.
//   seed-demo           Insert ablation-sweep-demo state (no-GPU reviewer flow).
package main

import (
	"context"
	"database/sql"
	"encoding/json"
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
		"delete the existing ablation-sweep-demo project (and its runs, "+
			"metrics, docs, reviews, attention) before re-inserting. Use "+
			"when the seed content has evolved (new plot families, etc.) "+
			"and you want to refresh a previously-seeded hub.")
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
	var wasReset bool
	if *reset {
		deleted, err := server.ResetDemo(ctx, db)
		if err != nil {
			log.Error("seed-demo reset failed", "err", err)
			os.Exit(1)
		}
		wasReset = deleted
		if deleted {
			fmt.Println("seed-demo: reset — deleted prior demo rows.")
		} else {
			fmt.Println("seed-demo: reset — no prior demo rows to delete.")
		}
	}

	res, err := server.SeedDemo(ctx, db, *dataRoot)
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
	fmt.Printf("seed-demo: %s demo state.\n  project:    %s\n  runs:       %d\n  document:   %s\n  review:     %s (pending)\n  attention:  %s (open decision)\n  images:     %d (samples/generations × 3 per run)\n",
		action, res.ProjectID, len(res.RunIDs), res.DocumentID, res.ReviewID, res.Attention, res.ImageCount)
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
