// Probe wedge for the new M4 (LocalLogTailDriver) claude-code adapter.
//
// Reads a claude-code session JSONL, extracts the last N turns, and
// streams the proposed AgentEvent mapping to stdout. Validates the
// observed schema against the design table before any production
// architecture commits.
//
// Throwaway-friendly: nothing else in the hub depends on this. Delete
// once the adapter ships and its tests cover the same ground.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Raw JSONL line — only the fields we read. Everything else passes
// through json.RawMessage so unknown shapes don't crash the probe.
type rawEvent struct {
	Type       string          `json:"type"`
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	Timestamp  string          `json:"timestamp"`
	SessionID  string          `json:"sessionId"`
	Subtype    string          `json:"subtype"`
	Message    json.RawMessage `json:"message"`
	Attachment json.RawMessage `json:"attachment"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	Thinking   string          `json:"thinking"`
	Signature  string          `json:"signature"`
	Name       string          `json:"name"`
	ID         string          `json:"id"`
	Input      json.RawMessage `json:"input"`
	ToolUseID  string          `json:"tool_use_id"`
	IsError    bool            `json:"is_error"`
	RawContent json.RawMessage `json:"content"`
}

// AgentEvent — the mapped output shape. Mirrors the kinds the existing
// agent_feed.dart already renders, so the new driver is a drop-in for
// M1/M2's surface.
type agentEvent struct {
	Kind     string                 `json:"kind"`
	ID       string                 `json:"id,omitempty"`
	ParentID string                 `json:"parent_id,omitempty"`
	Turn     int                    `json:"turn"`
	Ts       string                 `json:"ts,omitempty"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
}

// Noise types — never user-visible on mobile. permission-mode,
// custom-title, agent-name, last-prompt repeat once per "queue tick"
// (7000+ in this 200k-line file) and add zero signal.
var noiseTypes = map[string]bool{
	"permission-mode":       true,
	"custom-title":          true,
	"agent-name":            true,
	"last-prompt":           true,
	"file-history-snapshot": true,
	"queue-operation":       true,
}

// System subtypes we surface. compact_boundary is rendered as a divider;
// turn_duration / scheduled_task_fire / local_command are dropped.
var systemSubtypeAllow = map[string]bool{
	"compact_boundary": true,
}

func main() {
	var path string
	var turns int
	flag.StringVar(&path, "file", "", "claude-code session JSONL path")
	flag.IntVar(&turns, "turns", 5, "number of trailing turns to emit (MVP=5)")
	flag.Parse()
	if path == "" {
		fmt.Fprintln(os.Stderr, "usage: probe-claude-jsonl -file <path> [-turns N]")
		os.Exit(2)
	}

	starts, err := findTurnStarts(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan: %v\n", err)
		os.Exit(1)
	}
	if len(starts) == 0 {
		fmt.Fprintln(os.Stderr, "no user-typed turns found")
		os.Exit(1)
	}

	// "Turn" = window opened by a user-typed message. The last `turns`
	// starts give us the last N turns; if file has < N, take all.
	first := 0
	if len(starts) > turns {
		first = len(starts) - turns
	}
	startOffset := starts[first]

	emitted, stats, err := emitFrom(path, startOffset, first)
	if err != nil {
		fmt.Fprintf(os.Stderr, "emit: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\n=== probe summary ===\n")
	fmt.Fprintf(os.Stderr, "turns scanned:    %d (last %d emitted)\n", len(starts), len(starts)-first)
	fmt.Fprintf(os.Stderr, "events emitted:   %d\n", emitted)
	fmt.Fprintf(os.Stderr, "by kind:\n")
	for _, k := range []string{"user_input", "text", "thought", "tool_call", "tool_result", "system", "attachment"} {
		if stats[k] > 0 {
			fmt.Fprintf(os.Stderr, "  %-12s %d\n", k, stats[k])
		}
	}
	if stats["DROPPED_NOISE"] > 0 {
		fmt.Fprintf(os.Stderr, "noise dropped:    %d\n", stats["DROPPED_NOISE"])
	}
	if stats["UNKNOWN"] > 0 {
		fmt.Fprintf(os.Stderr, "unknown types:    %d  ← SCHEMA DRIFT, investigate\n", stats["UNKNOWN"])
	}
}

// findTurnStarts returns the byte offsets of every line whose event is
// a user-typed message (i.e. user.message.content is a string, not an
// array of tool_result blocks).
func findTurnStarts(path string) ([]int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var offsets []int64
	r := bufio.NewReaderSize(f, 1<<20)
	var pos int64
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if isUserTypedTurn(line) {
				offsets = append(offsets, pos)
			}
			pos += int64(len(line))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return offsets, nil
}

func isUserTypedTurn(line []byte) bool {
	var ev rawEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return false
	}
	if ev.Type != "user" || len(ev.Message) == 0 {
		return false
	}
	var msg rawMessage
	if err := json.Unmarshal(ev.Message, &msg); err != nil {
		return false
	}
	// content is a string when the user typed it; an array of blocks
	// when it's a tool_result response. Cheap discriminator: first
	// non-whitespace byte.
	c := strings.TrimLeft(string(msg.Content), " \t\r\n")
	return strings.HasPrefix(c, `"`)
}

// emitFrom seeks to startOffset and streams AgentEvents to stdout
// until EOF. Returns emitted count + per-kind stats.
func emitFrom(path string, startOffset int64, turnBase int) (int, map[string]int, error) {
	stats := map[string]int{}
	f, err := os.Open(path)
	if err != nil {
		return 0, stats, err
	}
	defer f.Close()
	if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
		return 0, stats, err
	}
	r := bufio.NewReaderSize(f, 1<<20)
	w := bufio.NewWriterSize(os.Stdout, 1<<20)
	defer w.Flush()

	// Turn opens on each user-typed message and covers every event
	// until the next user-typed message. Bump BEFORE mapping so the
	// user_input and its response share one turn number.
	turn := turnBase - 1
	emitted := 0
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if isUserTypedTurn(line) {
				turn++
			}
			n, kindsSeen := mapLine(line, turn, w)
			if n > 0 {
				emitted += n
				for k, c := range kindsSeen {
					stats[k] += c
				}
			} else {
				stats["DROPPED_NOISE"]++
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return emitted, stats, err
		}
	}
	return emitted, stats, nil
}

// mapLine applies the adapter rules to one JSONL line. Writes 0..N
// AgentEvents to w; returns count + per-kind tally.
func mapLine(line []byte, turn int, w *bufio.Writer) (int, map[string]int) {
	tally := map[string]int{}
	var ev rawEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return 0, tally
	}
	if noiseTypes[ev.Type] {
		return 0, tally
	}

	switch ev.Type {
	case "user":
		return mapUser(ev, turn, w, tally)
	case "assistant":
		return mapAssistant(ev, turn, w, tally)
	case "system":
		if !systemSubtypeAllow[ev.Subtype] {
			return 0, tally
		}
		emit(w, agentEvent{
			Kind: "system", ID: ev.UUID, ParentID: ev.ParentUUID,
			Turn: turn, Ts: ev.Timestamp,
			Payload: map[string]interface{}{"subtype": ev.Subtype},
		})
		tally["system"]++
		return 1, tally
	case "attachment":
		emit(w, agentEvent{
			Kind: "attachment", ID: ev.UUID, ParentID: ev.ParentUUID,
			Turn: turn, Ts: ev.Timestamp,
			Payload: map[string]interface{}{"raw": string(ev.Attachment)},
		})
		tally["attachment"]++
		return 1, tally
	default:
		tally["UNKNOWN"]++
		// Emit a debug marker so the operator sees what slipped through.
		emit(w, agentEvent{
			Kind: "system", Turn: turn,
			Payload: map[string]interface{}{
				"subtype":      "unknown_type",
				"unknown_type": ev.Type,
			},
		})
		return 1, tally
	}
}

func mapUser(ev rawEvent, turn int, w *bufio.Writer, tally map[string]int) (int, map[string]int) {
	var msg rawMessage
	if err := json.Unmarshal(ev.Message, &msg); err != nil {
		return 0, tally
	}
	// String content → user-typed prompt.
	c := strings.TrimLeft(string(msg.Content), " \t\r\n")
	if strings.HasPrefix(c, `"`) {
		var text string
		_ = json.Unmarshal(msg.Content, &text)
		emit(w, agentEvent{
			Kind: "user_input", ID: ev.UUID, ParentID: ev.ParentUUID,
			Turn: turn, Ts: ev.Timestamp,
			Payload: map[string]interface{}{"text": text},
		})
		tally["user_input"]++
		return 1, tally
	}
	// Array content → tool_result blocks.
	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return 0, tally
	}
	n := 0
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		// tool_result.content can be a string or an array of {type,text}
		// blocks. Normalize to a single string for the probe.
		content := normalizeToolResultContent(b.RawContent)
		emit(w, agentEvent{
			Kind: "tool_result", ID: ev.UUID, ParentID: ev.ParentUUID,
			Turn: turn, Ts: ev.Timestamp,
			Payload: map[string]interface{}{
				"tool_use_id":    b.ToolUseID,
				"is_error":       b.IsError,
				"content":        content,
				"content_length": len(content),
			},
		})
		tally["tool_result"]++
		n++
	}
	return n, tally
}

func mapAssistant(ev rawEvent, turn int, w *bufio.Writer, tally map[string]int) (int, map[string]int) {
	var msg rawMessage
	if err := json.Unmarshal(ev.Message, &msg); err != nil {
		return 0, tally
	}
	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return 0, tally
	}
	n := 0
	for _, b := range blocks {
		switch b.Type {
		case "text":
			emit(w, agentEvent{
				Kind: "text", ID: ev.UUID, ParentID: ev.ParentUUID,
				Turn: turn, Ts: ev.Timestamp,
				Payload: map[string]interface{}{"text": b.Text},
			})
			tally["text"]++
			n++
		case "thinking":
			// Marker only. Plaintext is empty on this build (signed for
			// API verification, not for human display).
			emit(w, agentEvent{
				Kind: "thought", ID: ev.UUID, ParentID: ev.ParentUUID,
				Turn: turn, Ts: ev.Timestamp,
				Payload: map[string]interface{}{
					"text":              "Thinking…",
					"marker_only":       true,
					"signature_present": b.Signature != "",
				},
			})
			tally["thought"]++
			n++
		case "tool_use":
			var input interface{}
			_ = json.Unmarshal(b.Input, &input)
			emit(w, agentEvent{
				Kind: "tool_call", ID: ev.UUID, ParentID: ev.ParentUUID,
				Turn: turn, Ts: ev.Timestamp,
				Payload: map[string]interface{}{
					"tool_use_id": b.ID,
					"name":        b.Name,
					"input":       input,
				},
			})
			tally["tool_call"]++
			n++
		}
	}
	return n, tally
}

func normalizeToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// String path
	c := strings.TrimLeft(string(raw), " \t\r\n")
	if strings.HasPrefix(c, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	// Array of {type,text} blocks
	var arr []contentBlock
	if err := json.Unmarshal(raw, &arr); err == nil {
		var sb strings.Builder
		for _, b := range arr {
			if b.Text != "" {
				sb.WriteString(b.Text)
			}
		}
		return sb.String()
	}
	return string(raw)
}

func emit(w *bufio.Writer, ae agentEvent) {
	b, err := json.Marshal(ae)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		return
	}
	_, _ = w.Write(b)
	_ = w.WriteByte('\n')
}
