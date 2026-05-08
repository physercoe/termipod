package server

import (
	"context"
	"errors"
)

// errRespawnSpecMutationNotImplemented is returned by
// respawnWithSpecMutation while the actual mutator is unimplemented
// (ADR-021 W2.3 fills it in). The handler maps this to HTTP 501 so
// mobile sees a clear "not yet wired" surface during the wedge gap
// rather than silently swallowing the picker selection.
var errRespawnSpecMutationNotImplemented = errors.New(
	"respawn-with-spec-mutation not yet implemented (ADR-021 W2.3)")

// respawnWithSpecMutation reads the active spawn spec, mutates the
// requested mode/model field, stops the agent, and spawns a fresh one
// with the new flags. The new agent re-attaches to the same session row
// via the engine_session_id resume cursor (ADR-014) so the transcript
// stays continuous.
//
// W2.1 ships the routing wedge with this stubbed body so the routing
// table is exercisable end-to-end. W2.3 lands the real string-edit
// helper for `--model X` / `--permission-mode X` argv shapes plus the
// pause/spawn orchestration. Until then, the `respawn` family route
// returns 501 to mobile — clearer than 422 (which means
// "engine doesn't support") because respawn IS supported, just
// pending implementation.
func (s *Server) respawnWithSpecMutation(
	ctx context.Context,
	agentID string,
	field string, // "mode" or "model"
	value string,
) error {
	_ = s
	_ = ctx
	_ = agentID
	_ = field
	_ = value
	return errRespawnSpecMutationNotImplemented
}
