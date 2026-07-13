package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// mcp_reference_annotations.go — agent-facing MCP surface for PDF annotations on
// a reference (ADR-053 companion). Lets a steward or worker read the director's
// highlights/notes and add its own (e.g. mark the passages it cited). Backed by
// the same store methods as the REST handlers (handlers_reference_annotations.go).
// Registered in native_tools.go via reference_annotation_*.

type annotationListArgs struct {
	ReferenceID string `json:"reference_id"`
}

func (s *Server) mcpAnnotationList(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a annotationListArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ReferenceID == "" {
		return nil, &jrpcError{Code: -32602, Message: "reference_id required"}
	}
	ok, err := s.referenceExists(ctx, team, a.ReferenceID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	if !ok {
		return nil, &jrpcError{Code: -32602, Message: "reference not found"}
	}
	out, err := s.listAnnotations(ctx, team, a.ReferenceID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(out), nil
}

// annotationCreateArgs embeds the mutable body and adds the parent reference id.
type annotationCreateArgs struct {
	ReferenceID string `json:"reference_id"`
	annotationBody
}

func (s *Server) mcpAnnotationCreate(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a annotationCreateArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ReferenceID == "" {
		return nil, &jrpcError{Code: -32602, Message: "reference_id required"}
	}
	out, err := s.createAnnotation(ctx, team, a.ReferenceID, a.annotationBody)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &jrpcError{Code: -32602, Message: "reference not found"}
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(out), nil
}

type annotationIDArgs struct {
	ReferenceID string `json:"reference_id"`
	ID          string `json:"id"`
}

func (s *Server) mcpAnnotationUpdate(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a annotationIDArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ReferenceID == "" || a.ID == "" {
		return nil, &jrpcError{Code: -32602, Message: "reference_id and id required"}
	}
	out, err := s.patchAnnotation(ctx, team, a.ReferenceID, a.ID, raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &jrpcError{Code: -32602, Message: "annotation not found"}
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(out), nil
}

func (s *Server) mcpAnnotationDelete(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a annotationIDArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ReferenceID == "" || a.ID == "" {
		return nil, &jrpcError{Code: -32602, Message: "reference_id and id required"}
	}
	ok, err := s.deleteAnnotation(ctx, team, a.ReferenceID, a.ID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	if !ok {
		return nil, &jrpcError{Code: -32602, Message: "annotation not found"}
	}
	return mcpResultJSON(map[string]any{"deleted": true, "id": a.ID}), nil
}
