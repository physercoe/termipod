package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestLoad_AgentEventIngest measures the hub's agent-event ingest ceiling:
// how many events/sec the single SQLite writer sustains when N synthetic
// agents POST events concurrently, and what the per-request latency looks
// like under that contention. It exercises the REAL write path
// (handlePostAgentEvent: insert with the MAX(seq)+1 read-modify-write +
// digest fold + session touches + in-process publish) by driving s.router
// in-process — no real socket, so the http.DefaultClient connection pool
// can't masquerade as the bottleneck; what's measured is the server.
//
// Grounds docs/discussions/hub-scaling-storage-and-concurrency.md §4 ("how
// many concurrent agents?") and §8-Q1 (the load test that turns every TBD
// into a number).
//
// Skipped unless HUB_LOADTEST=1 so it never runs in CI. Knobs (env):
//
//	HUB_LOADTEST_AGENTS         concurrent agents (default 100)
//	HUB_LOADTEST_SECONDS        run duration (default 5)
//	HUB_LOADTEST_PAYLOAD_BYTES  approx payload_json size per event (default 256)
//
// Run, e.g.:
//
//	HUB_LOADTEST=1 HUB_LOADTEST_AGENTS=200 HUB_LOADTEST_SECONDS=10 \
//	  go test ./internal/server -run TestLoad_AgentEventIngest -v -timeout 5m
func TestLoad_AgentEventIngest(t *testing.T) {
	if os.Getenv("HUB_LOADTEST") == "" {
		t.Skip("set HUB_LOADTEST=1 to run the event-ingest load test")
	}
	nAgents := loadEnvInt("HUB_LOADTEST_AGENTS", 100)
	durSec := loadEnvInt("HUB_LOADTEST_SECONDS", 5)
	payloadBytes := loadEnvInt("HUB_LOADTEST_PAYLOAD_BYTES", 256)
	// HUB_LOADTEST_THINK_MS models BURSTY real agents: each agent sleeps this
	// long between events (think/tool/wait), so aggregate write rate stays
	// below the single-writer ceiling and the writer has spare capacity for
	// the deferred fold. 0 (default) = the flat-out ceiling test. This is the
	// regime the bounded-staleness fold (digest_worker.go) actually targets —
	// the flat-out test pins ingest throughput, this one pins whether the
	// fold keeps up when it realistically can.
	thinkMs := loadEnvInt("HUB_LOADTEST_THINK_MS", 0)
	// HUB_LOADTEST_TEAMS distributes the agents round-robin across this many
	// teams (default 1). Each team is its own events.db/digest.db shard with its
	// own writer (ADR-045 P2), so >1 measures whether per-team sharding raises
	// the aggregate ingest ceiling on this box, or whether it's CPU-bound.
	nTeams := loadEnvInt("HUB_LOADTEST_TEAMS", 1)

	c := newE2E(t)

	// Build the team set. team[0] is the default team (newE2E's owner token);
	// extra teams are provisioned with their own owner token (the W1 path-team
	// auth gate requires each team's events be posted with that team's token).
	teams := []string{c.teamID}
	teamTok := map[string]string{c.teamID: c.token}
	for i := 1; i < nTeams; i++ {
		tid := fmt.Sprintf("loadteam-%d", i)
		tok, _, _, err := ProvisionTeam(context.Background(), c.s.writeDB, tid, tid, "")
		if err != nil {
			t.Fatalf("provision team %s: %v", tid, err)
		}
		teams = append(teams, tid)
		teamTok[tid] = tok
	}

	// Seed N agents distributed round-robin across the teams — bypass the spawn
	// machinery, we only need a row handlePostAgentEvent's agentBelongsToTeam
	// check accepts. Each agent's events route to its team's shard.
	agentIDs := make([]string, nAgents)
	agentTeams := make([]string, nAgents)
	for i := 0; i < nAgents; i++ {
		id := NewID()
		agentIDs[i] = id
		team := teams[i%len(teams)]
		agentTeams[i] = team
		if _, err := c.s.db.Exec(
			`INSERT INTO agents (id, team_id, handle, kind, status, created_at)
			 VALUES (?, ?, ?, ?, 'running', ?)`,
			id, team, fmt.Sprintf("load-%d", i), "claude-code", NowUTC(),
		); err != nil {
			t.Fatalf("seed agent %d: %v", i, err)
		}
	}

	// A representative text event with a payload of ~payloadBytes.
	body := func(agentID string) []byte {
		filler := strings.Repeat("x", maxInt(0, payloadBytes-32))
		b, _ := json.Marshal(map[string]any{
			"kind":     "text",
			"producer": "agent",
			"payload":  map[string]any{"text": filler},
		})
		return b
	}

	var (
		okN, errN int64
		statusMu  sync.Mutex
		statuses  = map[int]int64{}
		sampleErr atomic.Value // string
		latsMu    sync.Mutex
		allLats   []time.Duration
	)

	// Realistic A (lever 7): run the deferred-fold worker so the fold work
	// still happens — off the request path, competing for the single writer.
	// Without HUB_LOADTEST_WORKER set, the worker is inert → pure request-path
	// ceiling (events inserted + marked dirty, never folded).
	var wCancel context.CancelFunc
	if os.Getenv("HUB_LOADTEST_WORKER") != "" {
		var wctx context.Context
		wctx, wCancel = context.WithCancel(context.Background())
		go c.s.runDigestFold(wctx)
	}

	deadline := time.Now().Add(time.Duration(durSec) * time.Second)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < nAgents; i++ {
		wg.Add(1)
		go func(agentID, team, tok string) {
			defer wg.Done()
			reqBody := body(agentID)
			path := "/v1/teams/" + team + "/agents/" + agentID + "/events"
			local := make([]time.Duration, 0, 4096)
			localStatus := map[int]int64{}
			for time.Now().Before(deadline) {
				req := httptest.NewRequest("POST", path, bytes.NewReader(reqBody))
				req.Header.Set("Authorization", "Bearer "+tok)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				t0 := time.Now()
				c.s.router.ServeHTTP(rec, req)
				local = append(local, time.Since(t0))
				localStatus[rec.Code]++
				if rec.Code == 201 {
					atomic.AddInt64(&okN, 1)
				} else {
					atomic.AddInt64(&errN, 1)
					if sampleErr.Load() == nil {
						sampleErr.Store(fmt.Sprintf("status=%d body=%s",
							rec.Code, strings.TrimSpace(rec.Body.String())))
					}
				}
				if thinkMs > 0 {
					time.Sleep(time.Duration(thinkMs) * time.Millisecond)
				}
			}
			latsMu.Lock()
			allLats = append(allLats, local...)
			latsMu.Unlock()
			statusMu.Lock()
			for code, n := range localStatus {
				statuses[code] += n
			}
			statusMu.Unlock()
		}(agentIDs[i], agentTeams[i], teamTok[agentTeams[i]])
	}
	wg.Wait()
	elapsed := time.Since(start)
	if wCancel != nil {
		wCancel()
	}

	total := okN + errN
	evps := float64(total) / elapsed.Seconds()
	sort.Slice(allLats, func(i, j int) bool { return allLats[i] < allLats[j] })

	// Row + fold + storage totals summed across every team shard (ADR-045 P2).
	var rows, foldedEvents, digestAgents, dbBytes int64
	for _, team := range teams {
		var r, fe, da int64
		_ = evRForTeam(t, c.s, team).QueryRow(`SELECT COUNT(*) FROM agent_events`).Scan(&r)
		_ = dgRForTeam(t, c.s, team).QueryRow(`SELECT COALESCE(SUM(watermark_seq),0), COUNT(*) FROM agent_event_digests`).
			Scan(&fe, &da)
		rows += r
		foldedEvents += fe
		digestAgents += da
		for _, p := range []string{"events.db", "digest.db"} {
			if fi, err := os.Stat(filepath.Join(c.dataRoot, "teams", team, p)); err == nil {
				dbBytes += fi.Size()
			}
		}
	}
	if fi, err := os.Stat(filepath.Join(c.dataRoot, "hub.db")); err == nil {
		dbBytes += fi.Size()
	}

	t.Logf("──────── hub event-ingest load result ────────")
	t.Logf("agents=%d  teams=%d  duration=%s  payload≈%dB  think=%dms  GOMAXPROCS=%d",
		nAgents, len(teams), elapsed.Round(time.Millisecond), payloadBytes, thinkMs, runtime.GOMAXPROCS(0))
	t.Logf("events: total=%d  ok(201)=%d  err=%d", total, okN, errN)
	t.Logf("THROUGHPUT: %.0f events/sec", evps)
	t.Logf("latency: p50=%s  p90=%s  p99=%s  max=%s",
		pctl(allLats, 0.50), pctl(allLats, 0.90), pctl(allLats, 0.99), pctl(allLats, 1.0))
	t.Logf("status codes: %s", fmtStatuses(statuses))
	if s, _ := sampleErr.Load().(string); s != "" {
		t.Logf("first error: %s", s)
	}
	t.Logf("storage: agent_events rows=%d  stores(hub+events+digest)=%.1f MB  (=%.2f KB/event)",
		rows, float64(dbBytes)/1e6, float64(dbBytes)/1024/float64(maxInt64(1, rows)))
	t.Logf("fold: worker=%v  folded=%d/%d events  digests=%d  lag=%d (%.1f%%)",
		os.Getenv("HUB_LOADTEST_WORKER") != "", foldedEvents, rows, digestAgents,
		rows-foldedEvents, 100*float64(rows-foldedEvents)/float64(maxInt64(1, rows)))
	t.Logf("──────────────────────────────────────────────")
}

func loadEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func pctl(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(q * float64(len(sorted)-1))
	return sorted[idx].Round(time.Microsecond)
}

func fmtStatuses(m map[int]int64) string {
	codes := make([]int, 0, len(m))
	for c := range m {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	parts := make([]string, 0, len(codes))
	for _, c := range codes {
		parts = append(parts, fmt.Sprintf("%d×%d", c, m[c]))
	}
	return strings.Join(parts, " ")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
