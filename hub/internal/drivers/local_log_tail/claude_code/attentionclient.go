package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HubAttentionClient is the host-runner-side HTTP shim into the hub's
// attention_items API. ADR-027 W2i / W5e: the LocalLogTailDriver's
// parked hooks (PreCompact, PreToolUse(AskUserQuestion)) coordinate
// with mobile through this client — POST /attention to surface the
// approval card, GET /attention/{id} to poll for resolution.
//
// Construction: the W7 launch glue builds one of these per spawn
// holding the hub URL + per-spawn bearer token (same shape as the
// gateway's hubClient). The adapter calls Park() inside its
// parked-hook handlers; everything HTTP-shaped is encapsulated here.
type HubAttentionClient struct {
	// HubURL is the hub HTTP base. In production this is the
	// 127.0.0.1:41825 egress-proxy URL host-runner already feeds
	// into spawned-agent .mcp.json files, so the hub URL is hidden
	// from the agent process. Required.
	HubURL string
	// Team is the team id used for the /v1/teams/{team}/attention
	// path. Required.
	Team string
	// Token is the bearer the client sends in Authorization. Same
	// per-spawn token the gateway uses for hub.* forwards. Required.
	Token string
	// AgentHandle is stamped as actor_handle so mobile shows the
	// correct origin badge. Optional; empty leaves the attention
	// row unattributed.
	AgentHandle string
	// HTTP is an optional override (tests inject httptest.Server's
	// client). Nil → http.DefaultClient.
	HTTP *http.Client
	// PollInitial is the first poll interval (default 200ms).
	PollInitial time.Duration
	// PollMax is the cap for exponential backoff (default 2s).
	PollMax time.Duration
}

// ParkRequest is the host-runner-side ask: surface an approval card
// + poll until the user resolves it. Shape mirrors `attentionIn` on
// the hub side (handlers_attention.go).
type ParkRequest struct {
	Kind           string         // "permission_prompt"
	Summary        string         // mobile inbox row label
	Severity       string         // "minor" | "major" | "critical"
	SessionID      string         // optional
	PendingPayload map[string]any // structured detail (dialog_type, etc.)
}

// ParkResult is what the parked hook sees once mobile resolved.
type ParkResult struct {
	Decision string // "approve" | "reject"
	Reason   string // free text from the user (when present)
	OptionID string // for kind=select; not used by ADR-027 hooks today
}

// Park inserts a fresh attention_items row and long-polls until it's
// resolved or `timeout` elapses. On timeout the client returns the
// configured fail-closed default — the caller decides what that means
// for the hook (PreCompact deferred = {decision:block}, etc.).
//
// Returns the resolved ParkResult on success; ErrParkTimeout when the
// deadline fires before mobile decides.
func (c *HubAttentionClient) Park(ctx context.Context, req ParkRequest, timeout time.Duration) (*ParkResult, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	id, err := c.insert(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("insert attention: %w", err)
	}

	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.pollUntilResolved(pctx, id)
}

func (c *HubAttentionClient) validate() error {
	if c.HubURL == "" {
		return fmt.Errorf("HubAttentionClient: HubURL required")
	}
	if c.Team == "" {
		return fmt.Errorf("HubAttentionClient: Team required")
	}
	if c.Token == "" {
		return fmt.Errorf("HubAttentionClient: Token required")
	}
	return nil
}

func (c *HubAttentionClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// insert POSTs the attention_items row. Returns the assigned id.
func (c *HubAttentionClient) insert(ctx context.Context, req ParkRequest) (string, error) {
	if req.Kind == "" {
		req.Kind = "permission_prompt"
	}
	if req.Severity == "" {
		req.Severity = "minor"
	}
	pending, _ := json.Marshal(req.PendingPayload)
	body := map[string]any{
		"scope_kind": "team",
		"kind":       req.Kind,
		"summary":    req.Summary,
		"severity":   req.Severity,
		"actor_handle": c.AgentHandle,
		"pending_payload": json.RawMessage(pending),
	}
	if req.SessionID != "" {
		body["session_id"] = req.SessionID
	}
	out, err := c.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/v1/teams/%s/attention", c.Team), body)
	if err != nil {
		return "", err
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", fmt.Errorf("parse insert response: %w (body=%s)", err, out)
	}
	if parsed.ID == "" {
		return "", fmt.Errorf("insert: hub returned empty id (body=%s)", out)
	}
	return parsed.ID, nil
}

// pollUntilResolved fetches the attention row on an exponential
// backoff (PollInitial → PollMax) until status=='resolved' or ctx
// fires.
func (c *HubAttentionClient) pollUntilResolved(ctx context.Context, id string) (*ParkResult, error) {
	delay := c.PollInitial
	if delay <= 0 {
		delay = 200 * time.Millisecond
	}
	maxDelay := c.PollMax
	if maxDelay <= 0 {
		maxDelay = 2 * time.Second
	}
	path := fmt.Sprintf("/v1/teams/%s/attention/%s", c.Team, id)
	for {
		out, err := c.doJSON(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var row struct {
			Status    string          `json:"status"`
			Decisions json.RawMessage `json:"decisions"`
		}
		if err := json.Unmarshal(out, &row); err != nil {
			return nil, fmt.Errorf("parse poll response: %w", err)
		}
		if row.Status == "resolved" {
			return decodeLatestDecision(row.Decisions), nil
		}
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return nil, ErrParkTimeout
			}
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}

func decodeLatestDecision(raw json.RawMessage) *ParkResult {
	if len(raw) == 0 {
		return &ParkResult{Decision: "reject", Reason: "resolved without decision"}
	}
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil || len(arr) == 0 {
		return &ParkResult{Decision: "reject", Reason: "decisions parse failed"}
	}
	last := arr[len(arr)-1]
	out := &ParkResult{}
	if d, _ := last["decision"].(string); d != "" {
		out.Decision = d
	}
	if r, _ := last["reason"].(string); r != "" {
		out.Reason = r
	}
	if o, _ := last["option_id"].(string); o != "" {
		out.OptionID = o
	}
	return out
}

// doJSON sends a JSON request and returns the response body. Non-2xx
// responses are surfaced as errors carrying the hub's body so debugging
// against a real hub doesn't require log-fishing.
func (c *HubAttentionClient) doJSON(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	url := strings.TrimRight(c.HubURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("hub %d: %s", resp.StatusCode, bytes.TrimSpace(bodyBytes))
	}
	return bodyBytes, nil
}

// ErrParkTimeout signals that Park's per-call deadline fired before
// mobile resolved the attention. Callers translate this into the
// hook's safe-default response (e.g. PreCompact: {decision:block}).
var ErrParkTimeout = parkTimeoutErr{}

type parkTimeoutErr struct{}

func (parkTimeoutErr) Error() string { return "claude-code attention park: timeout" }
