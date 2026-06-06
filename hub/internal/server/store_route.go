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
// keyed by the owning team. Each resolves the team's shard from the per-team
// registry (store_registry.go): a hub with many teams keeps a bounded LRU set of
// open files, and the first access to a team lazily opens (and schema-ensures)
// its events.db / digest.db under dataRoot/teams/<team>/ (ADR-045 D2).
//
// The accessors return an error because the lazy open can fail (an invalid team
// slug, or disk I/O on the team dir) and the fold worker / OTLP loop run in
// background goroutines where a panic would crash the process rather than be
// caught by the HTTP recover middleware. A team that exists as a control row is
// always a valid slug, so at runtime the error is effectively a disk fault.
//
// Two key shapes, because two kinds of call site exist:
//   - team-keyed (eventsReader(team) …) — handlers and aggregations that already
//     know the team (the /v1/teams/{team}/… URL, the fold worker's dirty-set).
//   - agent-keyed (eventsWriterForAgent(ctx, agentID) …) — the internal notify /
//     ingest paths that hold only an agent id. teamForAgent resolves the shard
//     key from the agent (cached for the agent's lifetime — an agent never
//     changes team).

func (s *Server) eventsReader(team string) (*sql.DB, error) {
	h, err := s.stores.get(team)
	if err != nil {
		return nil, err
	}
	return h.eventsR, nil
}

func (s *Server) eventsWriter(team string) (*sql.DB, error) {
	h, err := s.stores.get(team)
	if err != nil {
		return nil, err
	}
	return h.eventsW, nil
}

func (s *Server) digestReader(team string) (*sql.DB, error) {
	h, err := s.stores.get(team)
	if err != nil {
		return nil, err
	}
	return h.digestR, nil
}

func (s *Server) digestWriter(team string) (*sql.DB, error) {
	h, err := s.stores.get(team)
	if err != nil {
		return nil, err
	}
	return h.digestW, nil
}

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
	return s.eventsReader(team)
}

func (s *Server) eventsWriterForAgent(ctx context.Context, agentID string) (*sql.DB, error) {
	team, err := s.teamForAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return s.eventsWriter(team)
}

func (s *Server) digestReaderForAgent(ctx context.Context, agentID string) (*sql.DB, error) {
	team, err := s.teamForAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return s.digestReader(team)
}

func (s *Server) digestWriterForAgent(ctx context.Context, agentID string) (*sql.DB, error) {
	team, err := s.teamForAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return s.digestWriter(team)
}

// teamForProject resolves a project's owning team — the per-team shard key for a
// project-scoped event query (project_id is denormalized onto agent_events but
// the shard is keyed by team). Projects never move team, so this is a simple
// control lookup.
func (s *Server) teamForProject(ctx context.Context, projectID string) (string, error) {
	var team string
	if err := s.db.QueryRowContext(ctx,
		`SELECT team_id FROM projects WHERE id = ?`, projectID).Scan(&team); err != nil {
		return "", fmt.Errorf("resolve team for project %s: %w", projectID, err)
	}
	return team, nil
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

// allTeams enumerates every team id from the control store — the iteration set
// for the cross-team consumers (insights engine scope, the OTLP export scan)
// that must fan a query out across every per-team shard and merge.
func (s *Server) allTeams(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM teams ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
