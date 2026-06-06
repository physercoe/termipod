package server

import (
	"context"
	"database/sql"
	"testing"
)

// store_route_testhelpers_test.go — per-team shard handle accessors for tests
// (ADR-045 P2). The event + digest stores are sharded per team
// (dataRoot/teams/<team>/), so a test that seeds or verifies raw rows must reach
// the owning team's shard rather than a single global handle.
//
// The agent-keyed helpers resolve the shard from the agent whose rows the test
// touches — its control row (seeded first) is authoritative for its team, so
// they route correctly for default- and custom-team tests alike. The team-keyed
// helpers serve the few sites that have a team but no single agent (an aggregate
// across a team, or a seed that runs before the agent row exists). All fatal on
// an unroutable team, which only happens on a test bug.

func evRForAgent(t *testing.T, s *Server, agentID string) *sql.DB {
	t.Helper()
	db, err := s.eventsReaderForAgent(context.Background(), agentID)
	if err != nil {
		t.Fatalf("events reader for agent %q: %v", agentID, err)
	}
	return db
}

func evWForAgent(t *testing.T, s *Server, agentID string) *sql.DB {
	t.Helper()
	db, err := s.eventsWriterForAgent(context.Background(), agentID)
	if err != nil {
		t.Fatalf("events writer for agent %q: %v", agentID, err)
	}
	return db
}

func dgRForAgent(t *testing.T, s *Server, agentID string) *sql.DB {
	t.Helper()
	db, err := s.digestReaderForAgent(context.Background(), agentID)
	if err != nil {
		t.Fatalf("digest reader for agent %q: %v", agentID, err)
	}
	return db
}

func evRForTeam(t *testing.T, s *Server, team string) *sql.DB {
	t.Helper()
	db, err := s.eventsReader(team)
	if err != nil {
		t.Fatalf("events reader for team %q: %v", team, err)
	}
	return db
}

func evWForTeam(t *testing.T, s *Server, team string) *sql.DB {
	t.Helper()
	db, err := s.eventsWriter(team)
	if err != nil {
		t.Fatalf("events writer for team %q: %v", team, err)
	}
	return db
}

func dgRForTeam(t *testing.T, s *Server, team string) *sql.DB {
	t.Helper()
	db, err := s.digestReader(team)
	if err != nil {
		t.Fatalf("digest reader for team %q: %v", team, err)
	}
	return db
}

func dgWForTeam(t *testing.T, s *Server, team string) *sql.DB {
	t.Helper()
	db, err := s.digestWriter(team)
	if err != nil {
		t.Fatalf("digest writer for team %q: %v", team, err)
	}
	return db
}
