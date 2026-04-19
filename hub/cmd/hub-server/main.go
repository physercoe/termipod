// hub-server — Termipod Hub API daemon.
//
// Subcommands:
//   init                Create a fresh data root and issue an owner token.
//   serve               Run the HTTP API.
//   tokens issue        Issue a new token for an agent or user.
//   tokens list         List tokens (hash-only; plaintext is never stored).
//   reconstruct-db      Rebuild events DB from event_log/ JSONL.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
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
	_ = fs.Parse(args)

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}
	srv, err := server.New(server.Config{
		Listen:   *listen,
		DBPath:   *dbPath,
		DataRoot: *dataRoot,
		Logger:   log,
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
	_ = fs.Parse(args)

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}
	db, err := server.OpenDB(*dbPath)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	plain := auth.NewToken()
	scopeMap := map[string]any{"team": *team, "role": *role}
	if *agentID != "" {
		scopeMap["agent_id"] = *agentID
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
	db, err := server.OpenDB(*dbPath)
	if err != nil {
		log.Error("open db", "err", err)
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
	files, inserted, skipped, err := server.ReconstructDB(context.Background(), *dataRoot, *dbPath)
	if err != nil {
		log.Error("reconstruct failed", "err", err, "files", files, "inserted", inserted, "skipped", skipped)
		os.Exit(1)
	}
	fmt.Printf("reconstruct-db: replayed %d file(s); inserted=%d skipped=%d\n", files, inserted, skipped)
}

// ---- helpers ----

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
