// Package hostagent implements the host-side daemon that launches backend
// processes (claude-code, codex) in tmux panes on behalf of the hub.
//
// The client here is a thin HTTP wrapper — one function per hub endpoint
// host-agent cares about. Centralising it keeps retry / auth / JSON encoding
// in one place so the main loop stays small.
package hostagent

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
}

func (c *Client) ListPendingSpawns(ctx context.Context, hostID string) ([]Spawn, error) {
	path := fmt.Sprintf("/v1/teams/%s/agents/spawns?host_id=%s&status=pending", c.Team, hostID)
	var out []Spawn
	err := c.get(ctx, path, &out)
	return out, err
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
