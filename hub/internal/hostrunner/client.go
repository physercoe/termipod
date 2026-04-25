// Package hostrunner implements the host-side daemon that launches backend
// processes (claude-code, codex) in tmux panes on behalf of the hub.
//
// The client here is a thin HTTP wrapper — one function per hub endpoint
// host-runner cares about. Centralising it keeps retry / auth / JSON encoding
// in one place so the main loop stays small.
package hostrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/termipod/hub/internal/hostrunner/a2a"
)

type Client struct {
	BaseURL string
	Token   string
	Team    string
	HTTP    *http.Client
}

func NewClient(baseURL, token, team string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		Team:    team,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

type HostRegisterIn struct {
	Name         string          `json:"name"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
}

type HostRegisterOut struct {
	ID string `json:"id"`
}

func (c *Client) RegisterHost(ctx context.Context, name string, caps json.RawMessage) (string, error) {
	var out HostRegisterOut
	err := c.post(ctx, fmt.Sprintf("/v1/teams/%s/hosts", c.Team),
		HostRegisterIn{Name: name, Capabilities: caps}, &out)
	return out.ID, err
}

func (c *Client) Heartbeat(ctx context.Context, hostID string) error {
	return c.post(ctx, fmt.Sprintf("/v1/teams/%s/hosts/%s/heartbeat", c.Team, hostID), nil, nil)
}

// PutCapabilities uploads the latest capability probe to the hub. Body is any
// value that marshals to the shape handleUpdateHostCapabilities accepts;
// Capabilities{} from capabilities.go is the canonical caller.
func (c *Client) PutCapabilities(ctx context.Context, hostID string, caps any) error {
	return c.do(ctx, http.MethodPut,
		fmt.Sprintf("/v1/teams/%s/hosts/%s/capabilities", c.Team, hostID), caps, nil)
}

// AgentFamilyFromHub mirrors the wire shape the hub returns from
// GET /v1/teams/{team}/agent-families. Mirrors agentfamilies.Family one
// for one but lives here so the host-runner package doesn't import the
// hub-internal agentfamilies package — keeps the build dependency one-way.
type AgentFamilyFromHub struct {
	Family            string                 `json:"family"`
	Bin               string                 `json:"bin"`
	VersionFlag       string                 `json:"version_flag"`
	Supports          []string               `json:"supports"`
	Incompatibilities []AgentFamilyIncompat  `json:"incompatibilities,omitempty"`
	Source            string                 `json:"source,omitempty"`
}

type AgentFamilyIncompat struct {
	Mode    string `json:"mode"`
	Billing string `json:"billing"`
	Reason  string `json:"reason"`
}

// ListAgentFamilies fetches the merged registry from the hub. Probe sweeps
// call this so a hot edit on mobile lands in the next probe without a
// host-runner restart. On any error the caller should fall back to the
// embedded YAML (so a brief hub outage doesn't blank capabilities).
func (c *Client) ListAgentFamilies(ctx context.Context) ([]AgentFamilyFromHub, error) {
	path := fmt.Sprintf("/v1/teams/%s/agent-families", c.Team)
	var out struct {
		Families []AgentFamilyFromHub `json:"families"`
	}
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.Families, nil
}

type Spawn struct {
	SpawnID      string          `json:"spawn_id"`
	ChildID      string          `json:"child_agent_id"`
	Handle       string          `json:"handle"`
	Kind         string          `json:"kind"`
	HostID       string          `json:"host_id"`
	Status       string          `json:"status"`
	SpawnSpec    string          `json:"spawn_spec_yaml"`
	Authority    json.RawMessage `json:"spawn_authority"`
	Task         json.RawMessage `json:"task,omitempty"`
	WorktreePath string          `json:"worktree_path,omitempty"`
	SpawnedAt    string          `json:"spawned_at"`
	// Mode is the concrete driving mode the hub resolved for this spawn
	// (M1|M2|M4). Empty means the hub had no mode info and host-runner
	// falls back to M4 on launch.
	Mode string `json:"mode,omitempty"`
	// MCPToken is the per-agent bearer the spawned agent uses to call
	// /mcp/{token} on the hub. Surfaced by the hub only to host-kind
	// callers; host-runner writes it into the agent's local .mcp.json
	// at launch time so claude-code can resolve `mcp__termipod__*`
	// tools (notably permission_prompt). Empty for spawns issued before
	// the per-spawn token wedge (W2.2) — those agents still launch but
	// can't call hub MCP tools.
	MCPToken string `json:"mcp_token,omitempty"`
}

func (c *Client) ListPendingSpawns(ctx context.Context, hostID string) ([]Spawn, error) {
	path := fmt.Sprintf("/v1/teams/%s/agents/spawns?host_id=%s&status=pending", c.Team, hostID)
	var out []Spawn
	err := c.get(ctx, path, &out)
	return out, err
}

type Agent2 struct {
	ID         string `json:"id"`
	Handle     string `json:"handle"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	HostID     string `json:"host_id"`
	PaneID     string `json:"pane_id"`
	PauseState string `json:"pause_state"`
}

// ListRunningAgents returns the agents currently host-launched on this host.
// Used by the idle detector to know which panes to watch.
func (c *Client) ListRunningAgents(ctx context.Context, hostID string) ([]Agent2, error) {
	path := fmt.Sprintf("/v1/teams/%s/agents?host_id=%s&status=running", c.Team, hostID)
	var out []Agent2
	err := c.get(ctx, path, &out)
	return out, err
}

// ListHostAgents returns every agent row assigned to this host, regardless
// of status. Used by the reconcile loop which needs to see pending / stale
// rows to promote them (or demote running rows whose CLI died).
func (c *Client) ListHostAgents(ctx context.Context, hostID string) ([]Agent2, error) {
	path := fmt.Sprintf("/v1/teams/%s/agents?host_id=%s", c.Team, hostID)
	var out []Agent2
	err := c.get(ctx, path, &out)
	return out, err
}

type AttentionIn struct {
	ScopeKind string   `json:"scope_kind"`
	ScopeID   string   `json:"scope_id,omitempty"`
	Kind      string   `json:"kind"`
	Summary   string   `json:"summary"`
	Severity  string   `json:"severity,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
}

func (c *Client) PostAttention(ctx context.Context, in AttentionIn) error {
	return c.do(ctx, http.MethodPost,
		fmt.Sprintf("/v1/teams/%s/attention", c.Team), in, nil)
}

type AgentPatch struct {
	Status     *string `json:"status,omitempty"`
	PauseState *string `json:"pause_state,omitempty"`
	PaneID     *string `json:"pane_id,omitempty"`
}

func (c *Client) PatchAgent(ctx context.Context, agentID string, patch AgentPatch) error {
	return c.do(ctx, http.MethodPatch,
		fmt.Sprintf("/v1/teams/%s/agents/%s", c.Team, agentID), patch, nil)
}

type HostCommand struct {
	ID      string          `json:"id"`
	HostID  string          `json:"host_id"`
	AgentID string          `json:"agent_id,omitempty"`
	Kind    string          `json:"kind"`
	Args    json.RawMessage `json:"args"`
	Status  string          `json:"status"`
}

func (c *Client) ListPendingCommands(ctx context.Context, hostID string) ([]HostCommand, error) {
	path := fmt.Sprintf("/v1/teams/%s/hosts/%s/commands?status=pending", c.Team, hostID)
	var out []HostCommand
	err := c.get(ctx, path, &out)
	return out, err
}

type CommandPatch struct {
	Status string          `json:"status"` // 'done'|'failed'
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func (c *Client) PatchCommand(ctx context.Context, cmdID string, patch CommandPatch) error {
	return c.do(ctx, http.MethodPatch,
		fmt.Sprintf("/v1/teams/%s/commands/%s", c.Team, cmdID), patch, nil)
}

// EventIn is the minimum payload shape required by handlePostEvent.
// Parts is a slice of {type,text|file} objects; marker forwarding typically
// emits a single text part, or a file part for attach markers.
type EventIn struct {
	Type   string        `json:"type"`
	FromID string        `json:"from_id,omitempty"`
	Parts  []EventInPart `json:"parts,omitempty"`
}

type EventInPart struct {
	// Kind is the part discriminator ("text" | "file" | "image"). The hub's
	// events.Part reads this as the top-level kind field; matching the JSON
	// tag keeps marker forwarding round-trippable without translation.
	Kind string       `json:"kind"`
	Text string       `json:"text,omitempty"`
	File *BlobRefWire `json:"file,omitempty"`
}

// BlobRefWire mirrors events.BlobRef for the subset host-runner needs. Kept
// separate from the server's struct to avoid importing the server package.
type BlobRefWire struct {
	URI  string `json:"uri"`
	Mime string `json:"mime,omitempty"`
	Size int64  `json:"size,omitempty"`
}

// BlobUploadOut is the response from POST /v1/blobs.
type BlobUploadOut struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mime   string `json:"mime"`
}

// UploadBlob POSTs raw bytes to /v1/blobs and returns the content-addressed
// identifier. Duplicate bytes dedup server-side, so re-uploading the same
// file is cheap.
func (c *Client) UploadBlob(ctx context.Context, body []byte, mime string) (BlobUploadOut, error) {
	if mime == "" {
		mime = "application/octet-stream"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/blobs", bytes.NewReader(body))
	if err != nil {
		return BlobUploadOut{}, err
	}
	req.Header.Set("Content-Type", mime)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return BlobUploadOut{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return BlobUploadOut{}, fmt.Errorf("upload blob: %d %s", resp.StatusCode, string(b))
	}
	var out BlobUploadOut
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return BlobUploadOut{}, err
	}
	return out, nil
}

// PostEvent forwards a marker-derived event to the project/channel feed.
// Caller is responsible for resolving projectID/channelID from the spawn
// spec — see SpawnSpec.ProjectID / ChannelID.
func (c *Client) PostEvent(ctx context.Context, projectID, channelID string, in EventIn) error {
	path := fmt.Sprintf("/v1/teams/%s/projects/%s/channels/%s/events",
		c.Team, projectID, channelID)
	return c.do(ctx, http.MethodPost, path, in, nil)
}

// PostAgentInput delivers an input record to the hub so the local
// InputRouter dispatches it to the agent driver. Used by phone/web via
// the hub's handler and by the A2A Dispatcher for peer messages; both
// route through the same audit path. fields is the wire-shape body
// (kind plus per-kind fields like body / decision). When fields does
// not already set "producer", callers may pass it explicitly in the map
// before the call — the hub stamps the column accordingly. Defaults to
// "user" server-side. Separate from PostAgentEvent because the events
// endpoint is for driver output, not for user/peer input.
func (c *Client) PostAgentInput(ctx context.Context, agentID string, fields map[string]any) error {
	path := fmt.Sprintf("/v1/teams/%s/agents/%s/input", c.Team, agentID)
	return c.do(ctx, http.MethodPost, path, fields, nil)
}

// PostAgentEvent appends an event to the per-agent queue (P1.7). Used by the
// driver (P1.1) to stream pane-derived text and lifecycle events; payload is
// opaque to the hub so the driver owns its own vocabulary per kind.
func (c *Client) PostAgentEvent(ctx context.Context, agentID, kind, producer string, payload any) error {
	body := struct {
		Kind     string `json:"kind"`
		Producer string `json:"producer,omitempty"`
		Payload  any    `json:"payload,omitempty"`
	}{kind, producer, payload}
	path := fmt.Sprintf("/v1/teams/%s/agents/%s/events", c.Team, agentID)
	return c.do(ctx, http.MethodPost, path, body, nil)
}

// AgentEvent is the client-side projection of an agent_events row. Payload
// is a raw JSON object the caller decodes per-kind; keeping it opaque here
// means new event kinds don't need a client change.
type AgentEvent struct {
	ID       string          `json:"id"`
	AgentID  string          `json:"agent_id"`
	Seq      int64           `json:"seq"`
	TS       string          `json:"ts"`
	Kind     string          `json:"kind"`
	Producer string          `json:"producer"`
	Payload  json.RawMessage `json:"payload"`
}

// ListAgentEvents pulls a slice of events newer than sinceSeq. Used by the
// input router to pick up producer='user' rows and dispatch them to the
// driver; limit is server-capped at 1000. Passing 0 for sinceSeq backfills
// the whole queue — generally you want to initialise from a prior seq so
// the agent doesn't re-execute old user input on host-runner restart.
func (c *Client) ListAgentEvents(ctx context.Context, agentID string, sinceSeq int64, limit int) ([]AgentEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	path := fmt.Sprintf("/v1/teams/%s/agents/%s/events?since=%d&limit=%d",
		c.Team, agentID, sinceSeq, limit)
	var out []AgentEvent
	err := c.get(ctx, path, &out)
	return out, err
}

// A2ACardEntry is one row of the host's A2A directory payload. The hub
// rewrites Card.url to point at its own /a2a/relay/... endpoint once the
// reverse-tunnel relay lands (P3.3b); until then the card is stored
// verbatim and consumers route by (host_id, agent_id).
type A2ACardEntry struct {
	AgentID string          `json:"agent_id"`
	Handle  string          `json:"handle"`
	Card    json.RawMessage `json:"card"`
}

// PutA2ACards replaces the host's entire card set in the hub directory.
// Host-runner calls this on startup and whenever its live-agent list
// changes so steward lookups by handle stay fresh.
func (c *Client) PutA2ACards(ctx context.Context, hostID string, cards []A2ACardEntry) error {
	return c.do(ctx, http.MethodPut,
		fmt.Sprintf("/v1/teams/%s/hosts/%s/a2a/cards", c.Team, hostID),
		map[string]any{"cards": cards}, nil)
}

// NextTunnelRequest long-polls the hub for a queued A2A request on this
// host. Returns (nil, nil) on the hub's 204 (no work within wait window),
// which the caller should treat as a reason to reconnect immediately.
// waitMs caps at 60s hub-side.
func (c *Client) NextTunnelRequest(ctx context.Context, hostID string, waitMs int) (*a2a.TunnelEnvelope, error) {
	path := fmt.Sprintf("/v1/teams/%s/hosts/%s/a2a/tunnel/next?wait_ms=%d",
		c.Team, hostID, waitMs)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	// The long-poll wait dominates request time; override the default
	// 15s timeout with a slightly looser bound.
	cli := *c.HTTP
	cli.Timeout = time.Duration(waitMs)*time.Millisecond + 10*time.Second
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tunnel/next: %d %s", resp.StatusCode, string(b))
	}
	var out a2a.TunnelEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PostTunnelResponse ships the dispatched response back to the hub.
func (c *Client) PostTunnelResponse(ctx context.Context, hostID string, env *a2a.TunnelResponseEnvelope) error {
	return c.post(ctx,
		fmt.Sprintf("/v1/teams/%s/hosts/%s/a2a/tunnel/responses", c.Team, hostID),
		env, nil)
}

// Run is the trimmed projection of a /v1/teams/{team}/runs row that the
// trackio poller cares about. Fields the poller doesn't use are dropped
// to keep the wire struct honest — enrich it only when a caller needs
// something more.
type Run struct {
	ID            string `json:"id"`
	TrackioHostID string `json:"trackio_host_id,omitempty"`
	TrackioRunURI string `json:"trackio_run_uri,omitempty"`
	Status        string `json:"status"`
}

// ListRunsForHost returns every run in the team whose trackio_host_id
// matches the given host. Caller should pass its own HostID so the hub
// can filter server-side and skip rows this host wouldn't poll anyway.
func (c *Client) ListRunsForHost(ctx context.Context, hostID string) ([]Run, error) {
	path := fmt.Sprintf("/v1/teams/%s/runs?trackio_host=%s", c.Team, hostID)
	var out []Run
	err := c.get(ctx, path, &out)
	return out, err
}

// MetricPoints is one downsampled series plus the last-observed snapshot
// the mobile UI reads directly without having to parse points_json.
type MetricPoints struct {
	Name        string     `json:"name"`
	Points      [][2]any   `json:"points"` // [[step, value], ...]
	SampleCount int64      `json:"sample_count"`
	LastStep    *int64     `json:"last_step,omitempty"`
	LastValue   *float64   `json:"last_value,omitempty"`
}

// PutRunMetrics replaces the hub-side digest for one run with the given
// set of downsampled series. Atomic on the hub side (delete + insert in
// one tx).
func (c *Client) PutRunMetrics(ctx context.Context, runID string, metrics []MetricPoints) error {
	return c.do(ctx, http.MethodPut,
		fmt.Sprintf("/v1/teams/%s/runs/%s/metrics", c.Team, runID),
		map[string]any{"metrics": metrics}, nil)
}

// ---- low level ----

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	return c.do(ctx, http.MethodPost, path, in, out)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, string(b))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
