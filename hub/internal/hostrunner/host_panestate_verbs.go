package hostrunner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/termipod/hub/internal/hostrunner/a2a"
)

// paneExplainTimeout bounds the two tmux round-trips this verb makes. The
// tunnel contract asks handlers not to block "longer than a few seconds"
// (a2a/tunnel.go), and a `capture-pane` against a wedged server is exactly the
// way that promise gets broken.
const paneExplainTimeout = 5 * time.Second

// The pane-state explain verb (plan P4). The screen stays on this side; what
// crosses is the evaluation record — the classification, every rule's outcome,
// and a bounded preview of the region each rule looked at.
//
// That is a deliberate, narrow exception to the rule P3 set for the automatic
// path, where an attention row carries a rule id and NEVER pane text. The
// difference is who asked: the tick publishes to everyone watching a feed,
// this answers one human who typed "why". Consent is about what an unwanted
// call costs, and the hub refuses agent-kind callers on the route in front of
// this verb for exactly that reason — an agent must not read another agent's
// screen through a debugging tool.

// paneExplainPayload is the verb-args schema for host.pane_explain.
//
// The hub resolves the agent row and sends the three facts this side cannot
// look up: which pane to capture, which family to map, and which agent id to
// stamp on the answer. Host-runner does not re-query the hub for them — the
// caller already had them, and a second lookup could disagree with the first.
type paneExplainPayload struct {
	AgentID string `json:"agent_id"`
	PaneID  string `json:"pane_id"`
	Family  string `json:"family"`
}

// handleHostPaneExplain captures one pane and answers with its full evaluation.
func (r *Runner) handleHostPaneExplain(ctx context.Context, env *a2a.TunnelEnvelope) *a2a.TunnelResponseEnvelope {
	var p paneExplainPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return verbError(env, http.StatusBadRequest, "bad_payload", err.Error())
	}
	if p.PaneID == "" {
		return verbError(env, http.StatusBadRequest, "bad_payload", "pane_id is required")
	}
	if p.Family == "" {
		return verbError(env, http.StatusBadRequest, "bad_payload", "family is required")
	}

	callCtx, cancel := context.WithTimeout(ctx, paneExplainTimeout)
	defer cancel()

	res, err := r.paneStates.explain(callCtx, p.AgentID, p.PaneID, p.Family)
	switch {
	case err == nil:
	case errors.Is(err, errPaneStateDisabled):
		// A host whose embedded manifests failed to load supervises agents
		// without classification (newPaneStateWatch's deliberate degradation).
		// Say so plainly — the alternative is a caller concluding the RULES
		// are wrong when the loader never ran.
		return verbError(env, http.StatusServiceUnavailable, "detection_disabled", err.Error())
	default:
		var ufe *UnmappedFamilyError
		if errors.As(err, &ufe) {
			// A definite answer about an unclassifiable engine, not a failure.
			return verbJSON(env, http.StatusUnprocessableEntity, map[string]any{
				"error":  "unmapped_family",
				"family": ufe.Family,
				"detail": ufe.Error(),
			})
		}
		// Everything left is a capture that did not happen: the pane is gone,
		// tmux is down, the context expired.
		return verbError(env, http.StatusBadGateway, "capture_failed", err.Error())
	}
	res.HostID = r.HostID
	return verbJSON(env, http.StatusOK, res)
}
