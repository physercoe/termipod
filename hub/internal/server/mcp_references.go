package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// mcp_references.go — the agent-facing MCP surface for the hub reference library
// (ADR-053). Full CRUD so a steward or worker can read the director's library,
// add papers it finds, annotate them, and prune. Backed by the same store
// methods as the REST handlers (handlers_references.go), so agents and the
// desktop operate one library. Registered in native_tools.go via reference_*.

type referenceListArgs struct {
	Collection string `json:"collection"`
	Tag        string `json:"tag"`
	Source     string `json:"source"`
	Q          string `json:"q"`
	Limit      int    `json:"limit"`
}

func (s *Server) mcpReferenceList(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a referenceListArgs
	_ = json.Unmarshal(raw, &a)
	out, err := s.listReferences(ctx, team, referenceFilter{
		Collection: a.Collection, Tag: a.Tag, Source: a.Source, Q: a.Q, Limit: a.Limit,
	})
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(out), nil
}

type referenceIDArgs struct {
	ID string `json:"id"`
}

func (s *Server) mcpReferenceGet(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a referenceIDArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ID == "" {
		return nil, &jrpcError{Code: -32602, Message: "id required"}
	}
	out, err := s.getReferenceByID(ctx, team, a.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &jrpcError{Code: -32602, Message: "reference not found"}
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(out), nil
}

func (s *Server) mcpReferenceCreate(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var b referenceBody
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, &jrpcError{Code: -32602, Message: "invalid reference body"}
	}
	if b.Title == "" && b.ExternalID == "" {
		return nil, &jrpcError{Code: -32602, Message: "title or external_id required"}
	}
	out, err := s.createReference(ctx, team, b)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(out), nil
}

// mcpReferenceUpdate patches by id — fields present in args override, the rest
// keep their stored value. The id itself is ignored by the body decode.
func (s *Server) mcpReferenceUpdate(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a referenceIDArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ID == "" {
		return nil, &jrpcError{Code: -32602, Message: "id required"}
	}
	out, err := s.patchReference(ctx, team, a.ID, raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &jrpcError{Code: -32602, Message: "reference not found"}
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(out), nil
}

func (s *Server) mcpReferenceDelete(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a referenceIDArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ID == "" {
		return nil, &jrpcError{Code: -32602, Message: "id required"}
	}
	ok, err := s.deleteReference(ctx, team, a.ID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	if !ok {
		return nil, &jrpcError{Code: -32602, Message: "reference not found"}
	}
	return mcpResultJSON(map[string]any{"deleted": true, "id": a.ID}), nil
}
