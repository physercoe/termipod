// Package events defines the canonical event model (plan §12).
// Wire-format adapters (MCP, markers, future A2A/ACP) translate
// to/from these types.
package events

import (
	"encoding/json"
	"time"
)

type Event struct {
	ID            string          `json:"id"`
	SchemaVersion int             `json:"schema_version"`
	Ts            time.Time       `json:"ts"`
	ReceivedTs    time.Time       `json:"received_ts"`
	ChannelID     string          `json:"channel_id"`
	Type          string          `json:"type"`
	FromID        string          `json:"from_id,omitempty"`
	ToIDs         []string        `json:"to_ids,omitempty"`
	Parts         []Part          `json:"parts"`
	TaskID        *string         `json:"task_id,omitempty"`
	CorrelationID *string         `json:"correlation_id,omitempty"`
	PaneRef       *PaneRef        `json:"pane_ref,omitempty"`
	UsageTokens   *Usage          `json:"usage_tokens,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
}

type Part struct {
	Kind    string          `json:"kind"`
	Text    string          `json:"text,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	File    *BlobRef        `json:"file,omitempty"`
	Image   *BlobRef        `json:"image,omitempty"`
	Excerpt *PaneExcerpt    `json:"excerpt,omitempty"`
}

type BlobRef struct {
	URI  string `json:"uri"`
	Mime string `json:"mime,omitempty"`
	Size int64  `json:"size,omitempty"`
}

type PaneRef struct {
	HostID   string    `json:"host_id"`
	Session  string    `json:"session"`
	Window   int       `json:"window"`
	PaneID   string    `json:"pane_id"`
	TsAnchor time.Time `json:"ts_anchor"`
}

type PaneExcerpt struct {
	PaneRef  PaneRef `json:"pane_ref"`
	LineFrom int     `json:"line_from"`
	LineTo   int     `json:"line_to"`
	Content  string  `json:"content,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	CacheRead    int `json:"cache_read,omitempty"`
	CacheWrite   int `json:"cache_write,omitempty"`
	CostCents    int `json:"cost_cents,omitempty"`
}

// MVP event-type vocabulary.
const (
	TypeMessage          = "message"
	TypeDelegate         = "delegate"
	TypeStatus           = "status"
	TypeDecisionRequest  = "decision_request"
	TypeApprovalRequest  = "approval_request"
	TypeAttach           = "attach"
	TypeInbound          = "inbound"
	TypeSpawn            = "spawn"
	TypeTerminate        = "terminate"
	TypeTaskProposal     = "task_proposal"
	TypeTaskUpdate       = "task_update"
	TypeTemplateProposal = "template_proposal"
	TypeIntervention     = "intervention"
)
