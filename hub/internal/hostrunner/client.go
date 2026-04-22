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
