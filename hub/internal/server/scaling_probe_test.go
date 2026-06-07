package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// scaling_probe_test.go — three capacity probes that complement the
// ingest-only TestLoad_AgentEventIngest (load_test.go):
//
//	A  TestProbe_ReadUnderWrite      — read p99 (insights fan-out / digest /
//	                                    event pages) while writers contend.
//	B  TestProbe_SSEFanout           — eventBus subscriber scaling + drop rate
//	                                    (the 32-deep, drop-on-overflow bus).
//	C  TestProbe_StoreMemoryFootprint — process RSS vs. open per-team store
//	                                    count (the unproven half of "1k agents
//	                                    on 2 GB": throughput we measured, memory
//	                                    we did not).
//
// All three are skipped unless HUB_SCALEPROBE=1, so they never run in CI.
//
// SHARED-BOX SAFETY. This harness is meant to run on a box that hosts other
// processes with < 2 GB free. Every probe calls memGuard(t) before and during
// allocation; if MemAvailable drops below HUB_SCALEPROBE_MEM_FLOOR_MIB (default
// 300) the probe stops immediately and reports what it gathered rather than
// pushing the box into swap. Defaults are deliberately small; scale up via the
// env knobs only with headroom to spare.

func probeEnabled(t *testing.T) {
	if os.Getenv("HUB_SCALEPROBE") == "" {
		t.Skip("set HUB_SCALEPROBE=1 to run the scaling probes")
	}
}

// memAvailableMiB reads /proc/meminfo MemAvailable (the kernel's own estimate
// of allocatable-without-swap memory). Returns -1 if unreadable (non-Linux).
func memAvailableMiB() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return -1
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(ln, "MemAvailable:") {
			f := strings.Fields(ln) // ["MemAvailable:", "853604", "kB"]
			if len(f) >= 2 {
				kb, _ := strconv.ParseInt(f[1], 10, 64)
				return kb / 1024
			}
		}
	}
	return -1
}

// rssMiB reads this process's resident set size from /proc/self/status.
func rssMiB() int64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(ln, "VmRSS:") {
			f := strings.Fields(ln)
			if len(f) >= 2 {
				kb, _ := strconv.ParseInt(f[1], 10, 64)
				return kb / 1024
			}
		}
	}
	return -1
}

func memFloorMiB() int64 { return int64(loadEnvInt("HUB_SCALEPROBE_MEM_FLOOR_MIB", 300)) }

// memGuard aborts the test if available memory has fallen below the floor —
// the kill-switch that keeps a probe from choking co-tenants. Returns the
// current MemAvailable for logging.
func memGuard(t *testing.T) int64 {
	t.Helper()
	avail := memAvailableMiB()
	if avail >= 0 && avail < memFloorMiB() {
		t.Fatalf("ABORT: MemAvailable %d MiB < floor %d MiB — refusing to pressure the box",
			avail, memFloorMiB())
	}
	return avail
}

// heapMiB is the Go-managed heap (a subset of RSS; the rest is the SQLite
// page cache, mmap-faulted file pages, goroutine stacks, and the binary).
func heapMiB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1 << 20)
}

// ───────────────────────── Probe A: read under write ─────────────────────────

// TestProbe_ReadUnderWrite drives writers flat-out on N agents across T team
// shards while reader goroutines hammer three read shapes, and reports each
// read shape's latency distribution separately:
//
//	insights/engine — GET /v1/insights?engine=claude-code  (CROSS-TEAM fan-out;
//	                  the per-team shard merge added in ADR-045 P2)
//	insights/team   — GET /v1/insights?team_id=<default>   (single shard)
//	events/page     — GET .../agents/<id>/events?limit=50  (keyset page read)
//
// The engine-vs-team split is the direct measure of whether the fan-out+merge
// stays cheap as shard count grows. Knobs:
//
//	HUB_SCALEPROBE_TEAMS    team shards          (default 4)
//	HUB_SCALEPROBE_AGENTS   writer agents        (default 40)
//	HUB_SCALEPROBE_SECONDS  duration             (default 5)
//	HUB_SCALEPROBE_READERS  reader goroutines    (default 6)
func TestProbe_ReadUnderWrite(t *testing.T) {
	probeEnabled(t)
	memGuard(t)

	nTeams := loadEnvInt("HUB_SCALEPROBE_TEAMS", 4)
	nAgents := loadEnvInt("HUB_SCALEPROBE_AGENTS", 40)
	durSec := loadEnvInt("HUB_SCALEPROBE_SECONDS", 5)
	nReaders := loadEnvInt("HUB_SCALEPROBE_READERS", 6)
	// HUB_SCALEPROBE_NOCACHE=1 appends a unique &until to each insights read so
	// every call misses the 30s response cache — isolates raw fan-out latency
	// (Probe-fix #3) from the cache hit-rate fix (#1). Default off measures the
	// realistic mix (cache fires for repeated param-less reads).
	noCache := os.Getenv("HUB_SCALEPROBE_NOCACHE") != ""

	c := newE2E(t)
	teams := []string{c.teamID}
	teamTok := map[string]string{c.teamID: c.token}
	for i := 1; i < nTeams; i++ {
		tid := fmt.Sprintf("probeteam-%d", i)
		tok, _, _, err := ProvisionTeam(context.Background(), c.s.writeDB, tid, tid, "")
		if err != nil {
			t.Fatalf("provision team %s: %v", tid, err)
		}
		teams = append(teams, tid)
		teamTok[tid] = tok
	}

	// Seed agents round-robin; keep a default-team agent for the events read.
	agentIDs := make([]string, nAgents)
	agentTeams := make([]string, nAgents)
	var defaultAgent string
	for i := 0; i < nAgents; i++ {
		id := NewID()
		agentIDs[i] = id
		team := teams[i%len(teams)]
		agentTeams[i] = team
		if team == c.teamID && defaultAgent == "" {
			defaultAgent = id
		}
		if _, err := c.s.db.Exec(
			`INSERT INTO agents (id, team_id, handle, kind, status, created_at)
			 VALUES (?, ?, ?, ?, 'running', ?)`,
			id, team, fmt.Sprintf("probe-%d", i), "claude-code", NowUTC(),
		); err != nil {
			t.Fatalf("seed agent %d: %v", i, err)
		}
	}

	reqBody, _ := json.Marshal(map[string]any{
		"kind": "text", "producer": "agent",
		"payload": map[string]any{"text": strings.Repeat("x", 200)},
	})

	deadline := time.Now().Add(time.Duration(durSec) * time.Second)
	var wOK, wErr int64
	var wg sync.WaitGroup

	// Writers: flat-out POST events (the contention the readers fight).
	for i := 0; i < nAgents; i++ {
		wg.Add(1)
		go func(agentID, team, tok string) {
			defer wg.Done()
			path := "/v1/teams/" + team + "/agents/" + agentID + "/events"
			for time.Now().Before(deadline) {
				req := httptest.NewRequest("POST", path, bytes.NewReader(reqBody))
				req.Header.Set("Authorization", "Bearer "+tok)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				c.s.router.ServeHTTP(rec, req)
				if rec.Code == 201 {
					atomic.AddInt64(&wOK, 1)
				} else {
					atomic.AddInt64(&wErr, 1)
				}
			}
		}(agentIDs[i], agentTeams[i], teamTok[agentTeams[i]])
	}

	// Readers: three shapes, round-robin by index. Each records its own slice.
	type readKind struct {
		name   string
		method string
		path   string
		tok    string
	}
	kinds := []readKind{
		{"insights/engine", "GET", "/v1/insights?engine=claude-code", c.token},
		{"insights/team", "GET", "/v1/insights?team_id=" + c.teamID, c.token},
		{"events/page", "GET",
			"/v1/teams/" + c.teamID + "/agents/" + defaultAgent + "/events?limit=50", c.token},
	}
	lat := make([][]time.Duration, len(kinds))
	status := make([]map[int]int64, len(kinds))
	for i := range kinds {
		status[i] = map[int]int64{}
	}
	var latMu sync.Mutex

	for r := 0; r < nReaders; r++ {
		wg.Add(1)
		go func(rIdx int) {
			defer wg.Done()
			k := rIdx % len(kinds)
			rk := kinds[k]
			local := make([]time.Duration, 0, 1024)
			localStatus := map[int]int64{}
			isInsights := strings.Contains(rk.path, "/v1/insights")
			for time.Now().Before(deadline) {
				path := rk.path
				if noCache && isInsights {
					// Unique until per call → forces a cache miss → raw fan-out.
					path += "&until=" + time.Now().UTC().Format(time.RFC3339Nano)
				}
				req := httptest.NewRequest(rk.method, path, nil)
				req.Header.Set("Authorization", "Bearer "+rk.tok)
				rec := httptest.NewRecorder()
				t0 := time.Now()
				c.s.router.ServeHTTP(rec, req)
				local = append(local, time.Since(t0))
				localStatus[rec.Code]++
			}
			latMu.Lock()
			lat[k] = append(lat[k], local...)
			for code, n := range localStatus {
				status[k][code] += n
			}
			latMu.Unlock()
		}(r)
	}

	wg.Wait()
	elapsed := time.Since(deadline.Add(-time.Duration(durSec) * time.Second))

	t.Logf("──────── Probe A: read latency under write contention ────────")
	t.Logf("teams=%d  writer-agents=%d  readers=%d  duration=%s  GOMAXPROCS=%d  MemAvail=%dMiB",
		nTeams, nAgents, nReaders, elapsed.Round(time.Millisecond),
		runtime.GOMAXPROCS(0), memAvailableMiB())
	t.Logf("WRITE background: ok=%d err=%d (%.0f ev/s)",
		wOK, wErr, float64(wOK+wErr)/elapsed.Seconds())
	for i, rk := range kinds {
		s := lat[i]
		sort.Slice(s, func(a, b int) bool { return s[a] < s[b] })
		t.Logf("READ %-15s n=%-5d p50=%-9s p90=%-9s p99=%-9s max=%-9s  status=%s",
			rk.name, len(s), pctl(s, 0.50), pctl(s, 0.90), pctl(s, 0.99),
			pctl(s, 1.0), fmtStatuses(status[i]))
	}
	t.Logf("note: insights/engine fans out across all %d shards + merges;"+
		" compare its p99 to insights/team (single shard).", nTeams)
	t.Logf("──────────────────────────────────────────────────────────────")
}

// ───────────────────────── Probe B: SSE fan-out ─────────────────────────

// TestProbe_SSEFanout stresses the in-process eventBus (eventbus.go): a 32-deep
// per-subscriber buffer that DROPS on overflow. It measures, per subscriber
// count, (1) the drop rate split by drainer speed and (2) Publish() fan-out
// latency as N grows. All subscribers share ONE channel — the thundering-herd
// case of many directors tailing one busy run. Knobs:
//
//	HUB_SCALEPROBE_SUBS       comma list of subscriber counts (default "50,200,800")
//	HUB_SCALEPROBE_PUBLISH    events published per N           (default 3000)
//	HUB_SCALEPROBE_SLOW_FRAC  fraction of subs that drain slowly (default 0.5)
//	HUB_SCALEPROBE_SLOW_MS    slow-drainer per-event delay ms  (default 2)
func TestProbe_SSEFanout(t *testing.T) {
	probeEnabled(t)
	memGuard(t)

	subsList := os.Getenv("HUB_SCALEPROBE_SUBS")
	if subsList == "" {
		subsList = "50,200,800"
	}
	nPublish := loadEnvInt("HUB_SCALEPROBE_PUBLISH", 3000)
	slowFracMilli := loadEnvInt("HUB_SCALEPROBE_SLOW_FRAC_MILLI", 500) // 0.5 as ‰ to stay int
	slowMs := loadEnvInt("HUB_SCALEPROBE_SLOW_MS", 2)
	// Publish pacing. 0 = flat-out (worst case, ~100s of k ev/s — far above the
	// real ingest ceiling, so drops are pessimistic). Set to a realistic rate
	// (e.g. 1000) to see drops at the rate the write path actually sustains.
	pubRate := loadEnvInt("HUB_SCALEPROBE_PUB_RATE", 0)

	t.Logf("──────── Probe B: SSE eventBus fan-out + drop ────────")
	rateLabel := "flat-out"
	if pubRate > 0 {
		rateLabel = fmt.Sprintf("%d ev/s (paced)", pubRate)
	}
	t.Logf("publish=%d/run  rate=%s  slowFrac=%.2f  slowDelay=%dms  bufferDepth=32 (drop-on-overflow)",
		nPublish, rateLabel, float64(slowFracMilli)/1000, slowMs)

	for _, tok := range strings.Split(subsList, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(tok))
		if err != nil || n <= 0 {
			continue
		}
		memGuard(t)

		bus := newEventBus()
		const ch = "probe-channel"
		nSlow := n * slowFracMilli / 1000

		type subState struct {
			c     chan map[string]any
			slow  bool
			recvd int64
		}
		subs := make([]*subState, n)
		var drainWG sync.WaitGroup
		done := make(chan struct{})
		for i := 0; i < n; i++ {
			ss := &subState{c: bus.Subscribe(ch), slow: i < nSlow}
			subs[i] = ss
			drainWG.Add(1)
			go func(ss *subState) {
				defer drainWG.Done()
				for {
					select {
					case <-ss.c:
						atomic.AddInt64(&ss.recvd, 1)
						if ss.slow && slowMs > 0 {
							time.Sleep(time.Duration(slowMs) * time.Millisecond)
						}
					case <-done:
						// drain whatever is buffered, then exit
						for {
							select {
							case <-ss.c:
								atomic.AddInt64(&ss.recvd, 1)
							default:
								return
							}
						}
					}
				}
			}(ss)
		}

		// Publish flat-out, timing each fan-out call.
		pubLat := make([]time.Duration, 0, nPublish)
		evt := map[string]any{"kind": "text", "seq": 0}
		var gap time.Duration
		if pubRate > 0 {
			gap = time.Second / time.Duration(pubRate)
		}
		next := time.Now()
		for i := 0; i < nPublish; i++ {
			if gap > 0 {
				next = next.Add(gap)
				if d := time.Until(next); d > 0 {
					time.Sleep(d)
				}
			}
			evt["seq"] = i
			t0 := time.Now()
			bus.Publish(ch, evt)
			pubLat = append(pubLat, time.Since(t0))
		}
		// Let drainers catch up briefly, then stop.
		time.Sleep(time.Duration(maxInt(50, slowMs*10)) * time.Millisecond)
		close(done)
		drainWG.Wait()

		var fastRecv, slowRecv, fastN, slowN int64
		for _, ss := range subs {
			if ss.slow {
				slowRecv += ss.recvd
				slowN++
			} else {
				fastRecv += ss.recvd
				fastN++
			}
			bus.Unsubscribe(ch, ss.c)
		}
		sort.Slice(pubLat, func(a, b int) bool { return pubLat[a] < pubLat[b] })
		dropPct := func(recv, subN int64) float64 {
			want := subN * int64(nPublish)
			if want == 0 {
				return 0
			}
			return 100 * float64(want-recv) / float64(want)
		}
		t.Logf("N=%-4d  publish p50=%-8s p99=%-8s max=%-8s | fast subs=%d drop=%.1f%%  slow subs=%d drop=%.1f%%",
			n, pctl(pubLat, 0.50), pctl(pubLat, 0.99), pctl(pubLat, 1.0),
			fastN, dropPct(fastRecv, fastN), slowN, dropPct(slowRecv, slowN))
	}
	t.Logf("note: drops are silent; each dropped event pushes that client to" +
		" backfill via ?since= → converts into read load (Probe A).")
	t.Logf("──────────────────────────────────────────────────────")
}

// ───────────────────────── Probe C: store memory footprint ─────────────────────────

// TestProbe_StoreMemoryFootprint opens per-team store shards one at a time and
// samples process RSS after each, to fit the per-open-store fixed overhead —
// the term that scales with the number of concurrently-active teams (bounded by
// the LRU cap, HUB_MAX_OPEN_TEAM_STORES, default 128). This is the memory half
// of the capacity question the throughput load test never answered.
//
// SAFE BY CONSTRUCTION: it touches each store only enough to realize the schema
// + pools (a COUNT(*), no bulk insert), so the demand-committed SQLite page
// cache stays small and RSS reflects fixed overhead, not the 64 MiB/writer-pool
// cache ceiling (reported analytically). memGuard runs every iteration. Knobs:
//
//	HUB_SCALEPROBE_STORES        teams to open (default 16; keep modest on 2 GB)
//	HUB_SCALEPROBE_MEM_FLOOR_MIB abort floor   (default 300)
func TestProbe_StoreMemoryFootprint(t *testing.T) {
	probeEnabled(t)

	nStores := loadEnvInt("HUB_SCALEPROBE_STORES", 16)
	c := newE2E(t)

	touch := func(team string) {
		er, err := c.s.eventsReader(team)
		if err != nil {
			t.Fatalf("eventsReader %s: %v", team, err)
		}
		ew, err := c.s.eventsWriter(team)
		if err != nil {
			t.Fatalf("eventsWriter %s: %v", team, err)
		}
		dr, err := c.s.digestReader(team)
		if err != nil {
			t.Fatalf("digestReader %s: %v", team, err)
		}
		dw, err := c.s.digestWriter(team)
		if err != nil {
			t.Fatalf("digestWriter %s: %v", team, err)
		}
		// Realize schema + a page read on every pool (reader and writer pools
		// are distinct connections, so touch both).
		var n int64
		_ = er.QueryRow(`SELECT count(*) FROM agent_events`).Scan(&n)
		_ = ew.QueryRow(`SELECT count(*) FROM agent_events`).Scan(&n)
		_ = dr.QueryRow(`SELECT count(*) FROM agent_event_digests`).Scan(&n)
		_ = dw.QueryRow(`SELECT count(*) FROM agent_event_digests`).Scan(&n)
	}

	runtime.GC()
	baseRSS := rssMiB()
	baseHeap := heapMiB()
	startAvail := memGuard(t)

	t.Logf("──────── Probe C: RSS vs. open per-team store count ────────")
	t.Logf("baseline: RSS=%dMiB heap=%.1fMiB  MemAvail=%dMiB  floor=%dMiB  LRU cap=%d",
		baseRSS, baseHeap, startAvail, memFloorMiB(), defaultMaxOpenTeamStores)

	// Default team is already open from newE2E; start the curve from there.
	touch(c.teamID)
	opened := 0
	var lastRSS int64 = baseRSS
	for i := 1; i <= nStores; i++ {
		avail := memGuard(t)
		tid := fmt.Sprintf("memteam-%d", i)
		if _, _, _, err := ProvisionTeam(context.Background(), c.s.writeDB, tid, tid, ""); err != nil {
			t.Fatalf("provision %s: %v", tid, err)
		}
		touch(tid)
		opened = i
		if i%4 == 0 || i == nStores {
			runtime.GC()
			rss := rssMiB()
			t.Logf("opened=%2d stores  RSS=%dMiB (+%dMiB since base, +%dMiB last step)  heap=%.1fMiB  MemAvail=%dMiB",
				i, rss, rss-baseRSS, rss-lastRSS, heapMiB(), avail)
			lastRSS = rss
		}
	}

	runtime.GC()
	finalRSS := rssMiB()
	perStore := 0.0
	if opened > 0 {
		perStore = float64(finalRSS-baseRSS) / float64(opened)
	}
	t.Logf("──────")
	t.Logf("opened %d stores: total RSS growth %dMiB → ~%.2f MiB/store (fixed overhead, light load)",
		opened, finalRSS-baseRSS, perStore)
	t.Logf("PROJECTION at LRU cap (%d stores): fixed ≈ base %dMiB + %.0f MiB = ~%.0f MiB RSS",
		defaultMaxOpenTeamStores, baseRSS, perStore*float64(defaultMaxOpenTeamStores),
		float64(baseRSS)+perStore*float64(defaultMaxOpenTeamStores))
	t.Logf("CACHE CEILING (separate, demand-committed): each team has 2 writer pools" +
		" (events+digest) × HUB_SQLITE_WRITER_CACHE_KB (default 64MiB) → up to 128MiB/team")
	t.Logf("  IF every open team's writer set is hot. Readers are uncapped-pool but")
	t.Logf("  default cache. This is why the cap matters on a 2GB box — tune")
	t.Logf("  HUB_MAX_OPEN_TEAM_STORES / HUB_SQLITE_WRITER_CACHE_KB to the RAM budget.")
	t.Logf("────────────────────────────────────────────────────────────")
}
