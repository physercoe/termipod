package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/termipod/hub/internal/server"
)

// srvCheck is one `hub-server doctor` preflight result. OK=false rows
// carry a Hint with the remediation step.
type srvCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// runDoctor implements `hub-server doctor` (ADR-028 plan W13): a
// preflight an operator runs before `hub-server serve` (or against a
// live hub) to confirm the data root is writable, the DB opens, there
// is room to grow, and the listen address is sane.
//
// It prints green/red per check with a remediation hint and exits 1 if
// any check is red, so it composes into CI / provisioning scripts.
func runDoctor(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	dataRoot := fs.String("data", defaultDataRoot(), "data root directory")
	dbPath := fs.String("db", "", "sqlite path (default: <data>/hub.db)")
	listen := fs.String("listen", "127.0.0.1:8443", "listen address to test")
	asJSON := fs.Bool("json", false, "emit the check results as a JSON array")
	_ = fs.Parse(args)
	_ = log // doctor reports through stdout/stderr, not the slog handler

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataRoot, "hub.db")
	}
	checks := []srvCheck{
		checkDataRootWritable(*dataRoot),
		checkDBReachable(*dbPath),
		checkStoreLayout(*dbPath),
		checkDiskSpace(*dataRoot),
		checkListenAddr(*listen),
	}
	emitSrvChecks("hub-server doctor", checks, *asJSON)
}

// checkDataRootWritable confirms the data root exists and accepts writes.
func checkDataRootWritable(dataRoot string) srvCheck {
	const name = "data root writable"
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		return srvCheck{Name: name, OK: false, Detail: err.Error(),
			Hint: "create " + dataRoot + " or point --data at a writable directory"}
	}
	probe := filepath.Join(dataRoot, ".doctor-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return srvCheck{Name: name, OK: false, Detail: err.Error()}
	}
	_ = os.Remove(probe)
	return srvCheck{Name: name, OK: true, Detail: dataRoot}
}

// checkDBReachable opens the sqlite DB and runs a trivial query. A
// missing file is a distinct failure with an `init` hint.
func checkDBReachable(dbPath string) srvCheck {
	const name = "database reachable"
	if _, err := os.Stat(dbPath); err != nil {
		return srvCheck{Name: name, OK: false, Detail: "no DB at " + dbPath,
			Hint: "run `hub-server init` first"}
	}
	db, err := server.OpenDB(dbPath)
	if err != nil {
		return srvCheck{Name: name, OK: false, Detail: err.Error()}
	}
	defer db.Close()
	var tables int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		return srvCheck{Name: name, OK: false, Detail: err.Error()}
	}
	return srvCheck{Name: name, OK: true,
		Detail: fmt.Sprintf("%s (%d tables)", dbPath, tables)}
}

// checkStoreLayout reports the store layout state (ADR-045). The target is the
// per-team sharded layout (P2): teams/<team>/{events.db,digest.db}. Earlier
// states are reported with the migration to run: a P1 global split (events.db +
// digest.db beside hub.db) → `db split-teams`; a fresh hub with no shards yet is
// fine (the registry creates each team's shard lazily on first event). Exactly
// one global store present is an inconsistent layout the operator must resolve.
func checkStoreLayout(dbPath string) srvCheck {
	const name = "store layout"
	dir := filepath.Dir(dbPath)
	_, eErr := os.Stat(filepath.Join(dir, "events.db"))
	_, dErr := os.Stat(filepath.Join(dir, "digest.db"))

	// Count per-team shards (a team dir holding an events.db).
	teamShards := 0
	if ents, err := os.ReadDir(filepath.Join(dir, "teams")); err == nil {
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, "teams", e.Name(), "events.db")); err == nil {
				teamShards++
			}
		}
	}

	switch {
	case eErr == nil && dErr == nil:
		// A P1 global split that hasn't been sharded per team yet.
		return srvCheck{Name: name, OK: true,
			Detail: "global split (P1) — run `hub-server db split-teams` to shard the event + digest stores per team (ADR-045 P2)"}
	case eErr == nil || dErr == nil:
		return srvCheck{Name: name, OK: false,
			Detail: fmt.Sprintf("inconsistent global store: events.db err=%v digest.db err=%v", eErr, dErr),
			Hint:   "restore the missing store from backup, or move the stray file aside and re-run `hub-server db split`"}
	case teamShards > 0:
		return srvCheck{Name: name, OK: true,
			Detail: fmt.Sprintf("sharded per team (%d shard(s) under teams/)", teamShards)}
	default:
		// No global store, no per-team shards — a fresh hub (or one that hasn't
		// ingested): the registry creates each team's shard on first event.
		return srvCheck{Name: name, OK: true,
			Detail: "per-team sharding (no shards yet — created lazily on first event)"}
	}
}

// checkDiskSpace confirms at least 1 GiB is free under the data root —
// the event log and blobs grow there. An undeterminable figure (no `df`)
// is reported as a pass with a note rather than a hard failure.
func checkDiskSpace(dir string) srvCheck {
	const name = "disk space"
	avail, err := availBytes(dir)
	if err != nil {
		return srvCheck{Name: name, OK: true,
			Detail: "could not determine free space: " + err.Error()}
	}
	const minFree = int64(1) << 30 // 1 GiB
	gib := float64(avail) / float64(1<<30)
	if avail < minFree {
		return srvCheck{Name: name, OK: false,
			Detail: fmt.Sprintf("%.2f GiB free under %s", gib, dir),
			Hint:   "free up space — the event log and blobs grow under the data root"}
	}
	return srvCheck{Name: name, OK: true, Detail: fmt.Sprintf("%.2f GiB free", gib)}
}

// checkListenAddr probes the listen address. An in-use port almost
// always means the hub is already running, so it is reported as a
// non-fatal note rather than a failure.
func checkListenAddr(addr string) srvCheck {
	const name = "listen address"
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return srvCheck{Name: name, OK: true,
			Detail: addr + " is in use (hub-server already running?)"}
	}
	_ = ln.Close()
	return srvCheck{Name: name, OK: true, Detail: addr + " is available"}
}

// availBytes returns the free bytes on the filesystem holding dir, via
// POSIX `df -Pk` (portable across linux + darwin without a syscall
// build-tag split).
func availBytes(dir string) (int64, error) {
	out, err := exec.Command("df", "-Pk", dir).Output()
	if err != nil {
		return 0, err
	}
	return parseDfAvail(string(out))
}

// parseDfAvail extracts the Available column (KiB) from `df -Pk` output
// and returns it in bytes. Split out so it is unit-testable without a
// real filesystem.
func parseDfAvail(out string) (int64, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0, errors.New("unexpected df output")
	}
	// POSIX columns: Filesystem 1024-blocks Used Available Capacity Mounted-on
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0, errors.New("unexpected df columns")
	}
	kib, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse df available column: %w", err)
	}
	return kib * 1024, nil
}

// emitSrvChecks renders the check list (plain text or JSON) and exits 1
// when any check is red.
func emitSrvChecks(title string, checks []srvCheck, asJSON bool) {
	failed := 0
	for _, c := range checks {
		if !c.OK {
			failed++
		}
	}
	if asJSON {
		b, _ := json.MarshalIndent(checks, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("%s\n\n", title)
		for _, c := range checks {
			mark := "PASS"
			if !c.OK {
				mark = "FAIL"
			}
			line := fmt.Sprintf("  [%s] %s", mark, c.Name)
			if c.Detail != "" {
				line += " — " + c.Detail
			}
			fmt.Println(line)
			if !c.OK && c.Hint != "" {
				fmt.Printf("         hint: %s\n", c.Hint)
			}
		}
		fmt.Println()
		if failed == 0 {
			fmt.Printf("doctor: all %d check(s) passed\n", len(checks))
		} else {
			fmt.Fprintf(os.Stderr, "doctor: %d of %d check(s) failed\n", failed, len(checks))
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}
