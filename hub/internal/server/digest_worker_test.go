package server

import (
	"testing"
	"time"
)

// The bounded-staleness fold trigger is pure in-memory accounting (no DB, no
// worker goroutine), so it pins directly: markDigestDirty updates the trigger
// inputs, and collectFoldable removes exactly the agents whose state is due
// per (a) turn close / (b) N events / (c) tau, leaving the rest to accumulate.

func newTriggerServer() *Server {
	return &Server{digestDirty: map[string]*digestPending{}}
}

func TestFoldDue_Triggers(t *testing.T) {
	now := time.Unix(1000, 0)
	const maxEvents = 32
	const maxAge = 750 * time.Millisecond

	cases := []struct {
		name string
		p    digestPending
		want bool
	}{
		{"turn close folds immediately", digestPending{count: 1, turnClosed: true, firstDirty: now}, true},
		{"N events folds", digestPending{count: maxEvents, firstDirty: now}, true},
		{"just under N does not", digestPending{count: maxEvents - 1, firstDirty: now}, false},
		{"tau elapsed folds", digestPending{count: 1, firstDirty: now.Add(-maxAge)}, true},
		{"under tau, under N does not", digestPending{count: 1, firstDirty: now.Add(-maxAge / 2)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := c.p
			if got := p.foldDue(now, maxEvents, maxAge); got != c.want {
				t.Fatalf("foldDue=%v want %v", got, c.want)
			}
		})
	}
}

func TestMarkDigestDirty_Accounting(t *testing.T) {
	s := newTriggerServer()
	s.markDigestDirty("team1", "agentA", "text")
	s.markDigestDirty("team1", "agentA", "tool_call")
	s.markDigestDirty("team1", "agentA", "turn.result")
	s.markDigestDirty("team1", "", "text") // empty agent is ignored

	p := s.digestDirty["agentA"]
	if p == nil {
		t.Fatal("agentA not tracked")
	}
	if p.count != 3 {
		t.Fatalf("count=%d want 3", p.count)
	}
	if !p.turnClosed {
		t.Fatal("turnClosed not set by turn.result")
	}
	if p.team != "team1" {
		t.Fatalf("team=%q want team1", p.team)
	}
	if _, ok := s.digestDirty[""]; ok {
		t.Fatal("empty agent should not be tracked")
	}
}

func TestCollectFoldable_RemovesOnlyDue(t *testing.T) {
	s := newTriggerServer()
	now := time.Unix(2000, 0)
	const maxEvents = 32
	const maxAge = 750 * time.Millisecond

	// due: turn closed
	s.digestDirty["closed"] = &digestPending{team: "t", count: 1, turnClosed: true, firstDirty: now}
	// due: N events
	s.digestDirty["full"] = &digestPending{team: "t", count: maxEvents, firstDirty: now}
	// due: aged out
	s.digestDirty["old"] = &digestPending{team: "t", count: 2, firstDirty: now.Add(-maxAge)}
	// NOT due: fresh, few events
	s.digestDirty["fresh"] = &digestPending{team: "t", count: 2, firstDirty: now}

	due := s.collectFoldable(now, maxEvents, maxAge)
	if len(due) != 3 {
		t.Fatalf("collected %d due, want 3 (%v)", len(due), due)
	}
	got := map[string]bool{}
	for _, ft := range due {
		got[ft.agent] = true
		if ft.team != "t" {
			t.Fatalf("agent %s team=%q want t", ft.agent, ft.team)
		}
	}
	for _, a := range []string{"closed", "full", "old"} {
		if !got[a] {
			t.Fatalf("expected %s due", a)
		}
		if _, still := s.digestDirty[a]; still {
			t.Fatalf("%s should be removed after collect", a)
		}
	}
	// the fresh agent stays to accumulate
	if _, ok := s.digestDirty["fresh"]; !ok {
		t.Fatal("fresh agent should remain in pending set")
	}
	if len(s.digestDirty) != 1 {
		t.Fatalf("pending size=%d want 1", len(s.digestDirty))
	}

	// empty set returns nil
	s2 := newTriggerServer()
	if got := s2.collectFoldable(now, maxEvents, maxAge); got != nil {
		t.Fatalf("empty collectFoldable=%v want nil", got)
	}
}
