package server

import (
	"context"
	"database/sql"
	"fmt"
)

// store_route.go — ADR-045 P2: the team-keyed store accessors.
//
// Every read or write of the event store (agent_events) or the digest store
// (agent_event_digests + agent_turns) goes through one of these accessors,
// keyed by the owning team. Today they return the single global P1 handles
// unchanged, so threading `team`/`agentID` through the call sites is
// behaviour-preserving (inc 2). Inc 3 flips the bodies to the per-team registry
// (store_registry.go) — a pure swap, because the call sites already hand in the
// shard key.
//
// Two key shapes, because two kinds of call site exist:
//   - team-keyed (eventsReader(team) …) — handlers and aggregations that already
//     know the team (the /v1/teams/{team}/… URL, the fold worker's dirty-set).
//   - agent-keyed (eventsWriterForAgent(ctx, agentID) …) — the internal notify /
//     ingest paths that hold only an agent id. teamForAgent resolves the shard
//     key from the agent (cached for the agent's lifetime — an agent never
//     changes team).

func (s *Server) eventsReader(team string) *sql.DB { return s.eventsDB }
func (s *Server) eventsWriter(team string) *sql.DB { return s.eventsWriteDB }
func (s *Server) digestReader(team string) *sql.DB { return s.digestDB }
func (s *Server) digestWriter(team string) *sql.DB { return s.digestWriteDB }

// teamForAgent resolves an agent's team — the per-team shard key. Cached in
// s.agentTeam for the agent's lifetime (the (agent → team) binding is immutable:
// an agent is spawned into one team and never moves). The first lookup per agent
// hits the control store; subsequent ones are a map load. An agent with no
// control row is an error — every real agent_events row belongs to a spawned
// agent, so an unresolvable team signals a bug rather than a routing default.
func (s *Server) teamForAgent(ctx context.Context, agentID string) (string, error) {
	if agentID == "" {
		return "", fmt.Errorf("cannot route store: empty agent id")
	}
	if v, ok := s.agentTeam.Load(agentID); ok {
		return v.(string), nil
	}
	var team string
	if err := s.db.QueryRowContext(ctx,
		`SELECT team_id FROM agents WHERE id = ?`, agentID).Scan(&team); err != nil {
		return "", fmt.Errorf("resolve team for agent %s: %w", agentID, err)
	}
	s.agentTeam.Store(agentID, team)
	return team, nil
}

// eventsReaderForAgent / eventsWriterForAgent / digestReaderForAgent /
// digestWriterForAgent resolve the agent's team, then return that shard's pool.
func (s *Server) eventsReaderForAgent(ctx context.Context, agentID string) (*sql.DB, error) {
	team, err := s.teamForAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return s.eventsReader(team), nil
}

func (s *Server) eventsWriterForAgent(ctx context.Context, agentID string) (*sql.DB, error) {
	team, err := s.teamForAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return s.eventsWriter(team), nil
}

func (s *Server) digestReaderForAgent(ctx context.Context, agentID string) (*sql.DB, error) {
	team, err := s.teamForAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return s.digestReader(team), nil
}

func (s *Server) digestWriterForAgent(ctx context.Context, agentID string) (*sql.DB, error) {
	team, err := s.teamForAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return s.digestWriter(team), nil
}

// teamForSession resolves a session's team from the control store (sessions are
// control-plane rows). Used to route a session-scoped event/digest query to the
// right shard. Returns sql.ErrNoRows (wrapped) for an unknown session so callers
// that tolerated an empty result can keep doing so.
func (s *Server) teamForSession(ctx context.Context, sessionID string) (string, error) {
	var team string
	if err := s.db.QueryRowContext(ctx,
		`SELECT team_id FROM sessions WHERE id = ?`, sessionID).Scan(&team); err != nil {
		return "", fmt.Errorf("resolve team for session %s: %w", sessionID, err)
	}
	return team, nil
}

// eventsReaderForAgents / digestReaderForAgents resolve the shard from the first
// agent id of a homogeneous-team id set (a team-scoped agent list, or the agents
// of one session — both share a team), so an `agent_id IN (…)` query reads the
// right store. The caller must short-circuit an empty set before calling (every
// current caller does).
func (s *Server) eventsReaderForAgents(ctx context.Context, ids []string) (*sql.DB, error) {
	return s.eventsReaderForAgent(ctx, ids[0])
}

func (s *Server) digestReaderForAgents(ctx context.Context, ids []string) (*sql.DB, error) {
	return s.digestReaderForAgent(ctx, ids[0])
}
