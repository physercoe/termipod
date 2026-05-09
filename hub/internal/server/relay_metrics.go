package server

import (
	"sync"
	"time"
)

// RelayMetrics is the lightweight throughput counter for /a2a/relay/...
// surfaced by /v1/hub/stats (insights-phase-1 W3). It tracks:
//
//   - Active in-flight relay calls (instantaneous gauge).
//   - Aggregate bytes/sec averaged over a 30-second rolling window.
//   - Per-destination bytes/sec — keyed on (host_id, agent_id). The
//     A2A relay is token-less so we don't know the *from* agent at
//     this layer; the plan's optimistic from/to pair-table is downgraded
//     to to-only tracking.
//   - Cumulative dropped count (timeouts, tunnel errors).
//
// All operations are O(1) — the bucket array indexes by `unix_second %
// relayWindowSeconds`, which makes both record and read constant-time
// regardless of how many pairs we've seen.
type RelayMetrics struct {
	mu      sync.Mutex
	active  int64
	dropped int64
	now     func() time.Time

	agg   relayWindow
	pairs map[relayPairKey]*relayWindow
}

// relayWindowSeconds is the rolling window. 30s matches the hub-stats
// 30s response cache, so the value the mobile tile renders is at least
// internally consistent — a panel refresh and a metrics tick share the
// same horizon.
const relayWindowSeconds = 30

// relayWindow buckets bytes by unix-second mod relayWindowSeconds. The
// timestamp slot tracks which second a bucket was last written for, so
// stale buckets (older than the window) are skipped on read.
type relayWindow struct {
	bytes      [relayWindowSeconds]int64
	timestamps [relayWindowSeconds]int64
}

type relayPairKey struct {
	host  string
	agent string
}

// NewRelayMetrics returns an empty metrics struct using time.Now as the
// clock. Tests override `now` directly to drive deterministic windows.
func NewRelayMetrics() *RelayMetrics {
	return &RelayMetrics{
		now:   time.Now,
		pairs: map[relayPairKey]*relayWindow{},
	}
}

// Begin marks a relay call as in-flight. Returned closer must be called
// when the call returns (success or failure) to keep the active gauge
// accurate; idempotent if the caller defers it.
func (m *RelayMetrics) Begin() func() {
	m.mu.Lock()
	m.active++
	m.mu.Unlock()
	var done sync.Once
	return func() {
		done.Do(func() {
			m.mu.Lock()
			if m.active > 0 {
				m.active--
			}
			m.mu.Unlock()
		})
	}
}

// Record adds bytes for one relay round-trip's destination. Both request
// and response body lengths should be summed by the caller; we don't
// distinguish directions in the bucket.
func (m *RelayMetrics) Record(host, agent string, bytes int64) {
	if bytes <= 0 {
		return
	}
	now := m.now()
	sec := now.Unix()
	m.mu.Lock()
	defer m.mu.Unlock()
	addToWindow(&m.agg, sec, bytes)
	if host == "" && agent == "" {
		return
	}
	k := relayPairKey{host: host, agent: agent}
	w, ok := m.pairs[k]
	if !ok {
		w = &relayWindow{}
		m.pairs[k] = w
	}
	addToWindow(w, sec, bytes)
}

// Dropped increments the timeout/error counter. Callers bump this when
// enqueueAndWait returns context.DeadlineExceeded or when the host-runner
// posts a tunnelResponse for a request that has no waiter (tunnelManager
// errors). It is monotonic — never reset over the hub's lifetime so a
// trend can be observed even after the response cache expires.
func (m *RelayMetrics) Dropped() {
	m.mu.Lock()
	m.dropped++
	m.mu.Unlock()
}

// Snapshot is the point-in-time read used by the /v1/hub/stats handler.
// Computes bytes/sec by summing buckets within the last 30s and dividing
// by the window. Pairs with zero current rate are dropped — tile space
// on mobile is precious; an idle pair adds noise.
func (m *RelayMetrics) Snapshot() RelaySnapshot {
	now := m.now()
	cutoff := now.Unix() - relayWindowSeconds
	m.mu.Lock()
	defer m.mu.Unlock()
	out := RelaySnapshot{
		Active:       m.active,
		Dropped:      m.dropped,
		BytesPerSec:  windowRate(&m.agg, cutoff),
	}
	for k, w := range m.pairs {
		rate := windowRate(w, cutoff)
		if rate == 0 {
			continue
		}
		out.Pairs = append(out.Pairs, RelayPair{
			Host:        k.host,
			Agent:       k.agent,
			BytesPerSec: rate,
		})
	}
	return out
}

// RelaySnapshot is a wire-shaped struct — JSON-encodable directly into
// the live block of /v1/hub/stats. Only ships when a relay has seen
// traffic (per ADR-022 D2 the block is omitted on quiet hubs).
type RelaySnapshot struct {
	Active      int64       `json:"active"`
	Dropped     int64       `json:"dropped"`
	BytesPerSec int64       `json:"bytes_per_sec"`
	Pairs       []RelayPair `json:"pairs,omitempty"`
}

type RelayPair struct {
	Host        string `json:"host"`
	Agent       string `json:"agent"`
	BytesPerSec int64  `json:"bytes_per_sec"`
}

func addToWindow(w *relayWindow, sec, bytes int64) {
	i := int(sec % relayWindowSeconds)
	if i < 0 {
		i += relayWindowSeconds
	}
	if w.timestamps[i] != sec {
		w.bytes[i] = 0
		w.timestamps[i] = sec
	}
	w.bytes[i] += bytes
}

func windowRate(w *relayWindow, cutoff int64) int64 {
	// Include the cutoff second itself (`>=`); the half-open interval is
	// [now-30, now], exactly 30 distinct seconds. Using `>` would clip to
	// 29 and bias the rate down by ~3%.
	var sum int64
	for i := 0; i < relayWindowSeconds; i++ {
		if w.timestamps[i] >= cutoff {
			sum += w.bytes[i]
		}
	}
	return sum / relayWindowSeconds
}
