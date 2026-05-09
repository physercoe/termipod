package server

import (
	"testing"
	"time"
)

func TestRelayMetrics_RecordsAndAverages(t *testing.T) {
	m := NewRelayMetrics()
	// Pin the clock so bucket-modulo math is deterministic; advance by
	// hand so a 30s window is exercised end-to-end.
	clock := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return clock }

	// 30 seconds of 1000 bytes each → 30000 / 30 = 1000 B/s avg.
	for i := 0; i < 30; i++ {
		m.Record("host-gpu", "agent-w", 1000)
		clock = clock.Add(1 * time.Second)
	}

	snap := m.Snapshot()
	if snap.BytesPerSec != 1000 {
		t.Errorf("aggregate bytes_per_sec = %d, want 1000", snap.BytesPerSec)
	}
	if len(snap.Pairs) != 1 {
		t.Fatalf("pairs len = %d, want 1", len(snap.Pairs))
	}
	if snap.Pairs[0].Host != "host-gpu" || snap.Pairs[0].Agent != "agent-w" {
		t.Errorf("pair key = %+v, want host-gpu/agent-w", snap.Pairs[0])
	}
	if snap.Pairs[0].BytesPerSec != 1000 {
		t.Errorf("pair bytes_per_sec = %d, want 1000", snap.Pairs[0].BytesPerSec)
	}
}

func TestRelayMetrics_WindowDecay(t *testing.T) {
	m := NewRelayMetrics()
	clock := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return clock }

	// Single burst, then advance the clock past the window.
	m.Record("host-gpu", "agent-w", 30_000)
	if got := m.Snapshot().BytesPerSec; got != 1000 {
		t.Errorf("immediate avg = %d, want 1000", got)
	}

	// 31s later — every bucket from the burst is now stale.
	clock = clock.Add(31 * time.Second)
	snap := m.Snapshot()
	if snap.BytesPerSec != 0 {
		t.Errorf("post-decay agg = %d, want 0", snap.BytesPerSec)
	}
	// Idle pair pruned from the snapshot — empty slice is fine.
	if len(snap.Pairs) != 0 {
		t.Errorf("post-decay pairs = %v, want []", snap.Pairs)
	}
}

func TestRelayMetrics_ActiveGauge(t *testing.T) {
	m := NewRelayMetrics()
	end1 := m.Begin()
	end2 := m.Begin()
	if got := m.Snapshot().Active; got != 2 {
		t.Errorf("active = %d, want 2", got)
	}
	end1()
	if got := m.Snapshot().Active; got != 1 {
		t.Errorf("after one close active = %d, want 1", got)
	}
	end1() // second close on the same closer must be a no-op
	if got := m.Snapshot().Active; got != 1 {
		t.Errorf("after duplicate close active = %d, want 1", got)
	}
	end2()
	if got := m.Snapshot().Active; got != 0 {
		t.Errorf("after all closed active = %d, want 0", got)
	}
}

func TestRelayMetrics_DroppedMonotonic(t *testing.T) {
	m := NewRelayMetrics()
	for i := 0; i < 5; i++ {
		m.Dropped()
	}
	if got := m.Snapshot().Dropped; got != 5 {
		t.Errorf("dropped = %d, want 5", got)
	}
	// Reads don't reset.
	_ = m.Snapshot()
	if got := m.Snapshot().Dropped; got != 5 {
		t.Errorf("dropped after read = %d, want 5", got)
	}
}

func TestRelayMetrics_MultiPairIsolation(t *testing.T) {
	m := NewRelayMetrics()
	clock := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return clock }

	// 30 seconds of traffic to two destinations at different rates.
	for i := 0; i < 30; i++ {
		m.Record("host-a", "agent-1", 600)
		m.Record("host-b", "agent-2", 300)
		clock = clock.Add(1 * time.Second)
	}

	snap := m.Snapshot()
	if snap.BytesPerSec != 900 {
		t.Errorf("aggregate = %d, want 900", snap.BytesPerSec)
	}
	byKey := map[string]int64{}
	for _, p := range snap.Pairs {
		byKey[p.Host+"/"+p.Agent] = p.BytesPerSec
	}
	if byKey["host-a/agent-1"] != 600 {
		t.Errorf("host-a/agent-1 = %d, want 600", byKey["host-a/agent-1"])
	}
	if byKey["host-b/agent-2"] != 300 {
		t.Errorf("host-b/agent-2 = %d, want 300", byKey["host-b/agent-2"])
	}
}

func TestRelayMetrics_RecordZeroBytesIgnored(t *testing.T) {
	m := NewRelayMetrics()
	m.Record("host", "agent", 0)
	m.Record("host", "agent", -7)
	if got := m.Snapshot().BytesPerSec; got != 0 {
		t.Errorf("after zero/negative records bytes_per_sec = %d, want 0", got)
	}
	if len(m.Snapshot().Pairs) != 0 {
		t.Errorf("pairs nonzero after zero-byte records")
	}
}
