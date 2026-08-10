package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/termipod/hub/internal/auth"
	"github.com/termipod/hub/internal/panestate"
)

// The pane-state explain endpoint (plan P4) — herdr's `explain` command, as an
// API. Two modes behind one route, because they answer the same question:
//
//   - **live**: `{"agent_id": "..."}` — capture that agent's pane on its host
//     and evaluate it. The screen never reaches the hub; the host answers with
//     the record (host.pane_explain).
//   - **supplied**: `{"family": "codex", "screen": "..."}` — evaluate text the
//     caller already has. This is herdr's `--file` mode: no host, no pane, no
//     agent, and therefore testable in CI and usable for authoring a rule
//     against a screen someone pasted from a bug report.
//
// The modes are exclusive rather than layered on purpose. A body carrying both
// would have to pick one silently, and the two answer about different things —
// "what is that agent doing" versus "what would the rules say about this text".

const paneExplainVerbTimeout = 10 * time.Second

// paneExplainIn is the request body. Exactly one of AgentID / Screen.
type paneExplainIn struct {
	AgentID string `json:"agent_id,omitempty"`

	Family   string `json:"family,omitempty"`
	Screen   string `json:"screen,omitempty"`
	OSCTitle string `json:"osc_title,omitempty"`
}

// paneExplainScreenCap bounds the supplied-screen mode's input. A pane is a
// few KB; anything past this is not a screen, and the evaluator would happily
// run 58 regexes over it. Generous enough for a very tall pane with wide rows.
const paneExplainScreenCap = 256 * 1024

// hubPaneRegistry lazily loads the embedded manifests for the supplied-screen
// mode. The hub is not a classifier — the tick that matters runs on hosts —
// but the registry is embedded in the same module, so refusing to evaluate a
// screen the caller already handed over would be a self-inflicted round trip
// to a host that has nothing to do with it.
var (
	hubPaneRegistryOnce sync.Once
	hubPaneRegistry     *panestate.Registry
	hubPaneRegistryErr  error
)

func paneRegistry() (*panestate.Registry, error) {
	hubPaneRegistryOnce.Do(func() {
		hubPaneRegistry, hubPaneRegistryErr = panestate.Load()
	})
	return hubPaneRegistry, hubPaneRegistryErr
}

// handlePaneCoverage answers "which engines does pane-state classify at all",
// which is D-3's mapping table and is otherwise readable only by opening a
// YAML compiled into the binary.
//
// The supplied-screen mode needs it to offer a family picker, and the answer to
// "why is my agent never classified" is usually right here — the family is not
// in this list. Unmapped families are deliberately absent rather than listed as
// false: an engine nobody wrote rules for has no row, and inventing one would
// suggest a manifest exists.
func (s *Server) handlePaneCoverage(w http.ResponseWriter, r *http.Request) {
	reg, err := paneRegistry()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "pane-state manifests failed to load: "+err.Error())
		return
	}
	families := reg.Families()
	out := make([]map[string]any, 0, len(families))
	for _, f := range families {
		id, _ := reg.ManifestForFamily(f)
		row := map[string]any{"family": f, "manifest_id": id}
		if m, ok := reg.Manifest(id); ok {
			row["manifest_version"] = m.Version
			row["source"] = m.Source
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"families": out})
}

// handlePaneExplain answers "why does pane-state think that".
func (s *Server) handlePaneExplain(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")

	// An agent must not read another agent's screen. The record carries
	// bounded region previews — that is the whole point of the verb — so the
	// cost of an unwanted call is another agent's terminal contents.
	//
	// **This is defence in depth, and the layer it depends on is named here
	// rather than assumed.** From the network an agent-kind bearer never
	// reaches any team route: `auth.Middleware` allowlists bearer kinds to
	// operator/owner/user/host and refuses the rest with a 403
	// (`internal/auth/token.go:151-167`). But that allowlist deliberately
	// EXEMPTS `isInProcessDispatch` — the hub's own authority-tool self-call,
	// where an agent token is the legitimate credential. So the day someone
	// adds an MCP tool that dispatches to this route, the network guard is not
	// in the path and this one is the only thing left.
	//
	// A mutation deleting this line therefore survives the suite via the
	// network-level test below; `TestPaneExplainRefusesAgentTokensInProcess`
	// is the one that reaches it, by marking the context the way the self-call
	// does.
	//
	// Host tokens pass: a host-runner has the screen already.
	if tok, ok := auth.FromContext(r.Context()); ok && tok != nil && tok.Kind == "agent" {
		writeErr(w, http.StatusForbidden,
			"agent tokens may not read pane contents; pane_explain is a director tool")
		return
	}

	var in paneExplainIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	switch {
	case in.AgentID != "" && in.Screen != "":
		writeErr(w, http.StatusBadRequest,
			"agent_id and screen are exclusive: one asks about a live pane, the other about supplied text")
		return
	case in.AgentID == "" && in.Screen == "":
		writeErr(w, http.StatusBadRequest, "agent_id or screen is required")
		return
	case in.Screen != "":
		s.paneExplainSupplied(w, in)
	default:
		s.paneExplainLive(w, r, team, in.AgentID)
	}
}

// paneExplainSupplied evaluates caller-provided text — herdr's `--file` mode.
func (s *Server) paneExplainSupplied(w http.ResponseWriter, in paneExplainIn) {
	if in.Family == "" {
		writeErr(w, http.StatusBadRequest, "family is required with screen")
		return
	}
	if len(in.Screen) > paneExplainScreenCap {
		writeErr(w, http.StatusRequestEntityTooLarge, "screen is too large to be a pane")
		return
	}
	reg, err := paneRegistry()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "pane-state manifests failed to load: "+err.Error())
		return
	}
	manifestID, mapped := reg.ManifestForFamily(in.Family)
	if !mapped {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":  "unmapped_family",
			"family": in.Family,
			"detail": "no pane-state manifest is mapped for agent family " + in.Family,
		})
		return
	}
	input := panestate.Input{Screen: in.Screen, OSCTitle: in.OSCTitle}
	ex, err := reg.EvaluateManifest(manifestID, input)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "evaluate: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, panestate.NewExplainResult("supplied", in.Family, input, ex))
}

// paneExplainLive routes to the agent's host and relays what it answers.
func (s *Server) paneExplainLive(w http.ResponseWriter, r *http.Request, team, agentID string) {
	var hostID, paneID, kind, status string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(host_id, ''), COALESCE(pane_id, ''), COALESCE(kind, ''), status
		   FROM agents WHERE id = ? AND team_id = ?`, agentID, team).
		Scan(&hostID, &paneID, &kind, &status)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	// Each refusal names the missing precondition rather than collapsing into
	// one "cannot explain": they have different fixes, and the caller is a
	// human debugging a detector.
	if hostID == "" {
		writeErr(w, http.StatusConflict, "agent has no host; there is no pane to read")
		return
	}
	if paneID == "" {
		writeErr(w, http.StatusConflict,
			"agent has no tmux pane (a paneless driving mode, or it never launched)")
		return
	}
	// A terminal agent still carries the pane id it died with, and tmux may
	// even still show the pane under remain-on-exit. Classifying that screen
	// would answer confidently about an agent that is not there — so refuse by
	// name, rather than return `idle` for a corpse.
	switch status {
	case "terminated", "crashed", "failed", "archived":
		writeErr(w, http.StatusConflict,
			"agent is "+status+"; its pane no longer reflects a running engine")
		return
	}

	payload, err := json.Marshal(map[string]any{
		"agent_id": agentID,
		"pane_id":  paneID,
		"family":   kind,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(r.Context(), paneExplainVerbTimeout)
	defer cancel()
	resp, err := s.tunnel.enqueueHostVerb(callCtx, hostID, "host.pane_explain", payload)
	if err != nil {
		writeErr(w, http.StatusGatewayTimeout, "host did not answer: "+err.Error())
		return
	}
	body, err := base64.StdEncoding.DecodeString(resp.BodyB64)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "host returned an undecodable body")
		return
	}
	code := resp.Status
	if code == 0 {
		code = http.StatusOK
	}
	// Relayed verbatim, including the host's own error shapes — the host is
	// the authority on its panes, and re-wrapping its 422 would lose the
	// family name the caller needs.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}
