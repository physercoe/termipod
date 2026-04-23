package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// A2A v0.3 task primitives.
//
// Spec: https://a2a-protocol.org/latest/specification/
// All A2A calls are JSON-RPC 2.0 POSTs to the agent's base URL (the same
// URL the card advertises). This file covers the three methods a caller
// needs to drive remote work:
//
//   - message/send  — submit a user message, returns a Task stub.
//   - tasks/get     — poll task state by id.
//   - tasks/cancel  — request cancellation; terminal state "canceled".
//
// Dispatch to the actual agent (Claude Code in a pane, etc.) is plugged
// in via the Dispatcher interface. The server keeps a small in-memory
// task store so the caller can poll between dispatch and completion;
// persistence is a hub concern, not a host-runner concern.

// TaskState is the A2A-v0.3 lifecycle enum. submitted / working are
// non-terminal; completed / failed / canceled are terminal.
type TaskState string

const (
	TaskStateSubmitted     TaskState = "submitted"
	TaskStateWorking       TaskState = "working"
	TaskStateInputRequired TaskState = "input-required"
	TaskStateCompleted     TaskState = "completed"
	TaskStateFailed        TaskState = "failed"
	TaskStateCanceled      TaskState = "canceled"
)

// Message is the A2A user-or-agent message envelope. We model the fields
// the MVP actually uses; parts is kept as raw JSON so callers can ship
// arbitrary kinds (text, file, data) without this package needing a type
// per kind.
type Message struct {
	MessageID string          `json:"messageId"`
	Role      string          `json:"role"` // "user" | "agent"
	Parts     json.RawMessage `json:"parts"`
}

// TaskStatus is the A2A status wrapper.
type TaskStatus struct {
	State   TaskState `json:"state"`
	Message *Message  `json:"message,omitempty"`
}

// Task is the A2A task envelope returned by message/send and tasks/get.
type Task struct {
	ID        string     `json:"id"`
	Status    TaskStatus `json:"status"`
	History   []Message  `json:"history,omitempty"`
	CreatedAt time.Time  `json:"-"`
	UpdatedAt time.Time  `json:"-"`
}

// Dispatcher hands a newly-submitted message off to the agent's execution
// layer. Implementations run asynchronously — Dispatch should not block
// the JSON-RPC caller. The dispatcher reports progress back via
// TaskStore.Update.
//
// The host-runner runtime supplies a concrete dispatcher that posts the
// message into the agent's InputRouter (producer="a2a"). The default
// implementation here is a no-op that leaves the task in "submitted" so
// tests can observe the wire shape without needing a real agent.
type Dispatcher interface {
	Dispatch(ctx context.Context, agentID string, msg Message, taskID string, store *TaskStore) error
}

// NoopDispatcher records the dispatch call but never advances the task.
// Useful in tests and as the server's default until a real driver-backed
// dispatcher is wired in.
type NoopDispatcher struct{}

func (NoopDispatcher) Dispatch(ctx context.Context, agentID string, msg Message, taskID string, store *TaskStore) error {
	return nil
}

// TaskStore is an in-memory per-agent task index. Tasks are scoped by
// agentID so two agents on the same host-runner can't trip over each
// other's ids; ids themselves are generated here (caller-supplied ids
// are accepted but namespaced under agentID).
//
// No eviction today — tasks live until host-runner restart. For the MVP
// demo this is fine (O(hundreds) of tasks per host); a time-based sweep
// is a follow-up when the volume warrants it.
type TaskStore struct {
	mu    sync.Mutex
	tasks map[string]map[string]*Task // agentID -> taskID -> task
}

func NewTaskStore() *TaskStore {
	return &TaskStore{tasks: map[string]map[string]*Task{}}
}

// Create inserts a fresh task in "submitted" state. Returns a copy so
// the caller can't mutate store state through the returned pointer.
func (s *TaskStore) Create(agentID, taskID string, userMsg Message) *Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks[agentID] == nil {
		s.tasks[agentID] = map[string]*Task{}
	}
	now := time.Now().UTC()
	t := &Task{
		ID:        taskID,
		Status:    TaskStatus{State: TaskStateSubmitted},
		History:   []Message{userMsg},
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.tasks[agentID][taskID] = t
	return cloneTask(t)
}

// Get returns a copy of the task, or (nil, false) if not found.
func (s *TaskStore) Get(agentID, taskID string) (*Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	agents := s.tasks[agentID]
	if agents == nil {
		return nil, false
	}
	t, ok := agents[taskID]
	if !ok {
		return nil, false
	}
	return cloneTask(t), true
}

// Update applies status/message changes from the dispatcher. Terminal
// states are allowed to transition only to themselves — late completions
// after a cancel stay canceled. Returns the resulting task copy.
func (s *TaskStore) Update(agentID, taskID string, status TaskStatus, appendMsg *Message) (*Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	agents := s.tasks[agentID]
	if agents == nil {
		return nil, false
	}
	t, ok := agents[taskID]
	if !ok {
		return nil, false
	}
	if isTerminal(t.Status.State) && t.Status.State != status.State {
		// Silently ignore: state is frozen. Return the existing task.
		return cloneTask(t), true
	}
	t.Status = status
	if appendMsg != nil {
		t.History = append(t.History, *appendMsg)
	}
	t.UpdatedAt = time.Now().UTC()
	return cloneTask(t), true
}

func isTerminal(s TaskState) bool {
	switch s {
	case TaskStateCompleted, TaskStateFailed, TaskStateCanceled:
		return true
	}
	return false
}

func cloneTask(t *Task) *Task {
	cp := *t
	if len(t.History) > 0 {
		cp.History = append([]Message(nil), t.History...)
	}
	return &cp
}

// ---- JSON-RPC handler ----

// jsonRPCRequest is the incoming envelope. id is kept raw because the
// spec allows string | number | null and we just echo it back.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      json.RawMessage `json:"id"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Canonical JSON-RPC error codes we emit. Keep in sync with A2A spec
// §3.2 — the service-specific codes (-32000+) are informally defined.
const (
	rpcErrParse         = -32700
	rpcErrInvalidReq    = -32600
	rpcErrMethodNotFnd  = -32601
	rpcErrInvalidParams = -32602
	rpcErrInternal      = -32603
	rpcErrTaskNotFound  = -32001
)

// TaskRPCHandler returns an http.Handler that speaks A2A v0.3 JSON-RPC
// for a single agent. Caller is responsible for routing by agent id (the
// server layer strips the /a2a/<id>/ prefix before delegating).
//
// agentID is bound at mount time so the dispatcher knows which agent to
// deliver to; idGen produces fresh task ids (defaults to a wall-clock
// monotonic so tests can observe ordering without an external uuid dep).
func TaskRPCHandler(agentID string, store *TaskStore, dispatcher Dispatcher, idGen func() string) http.Handler {
	if idGen == nil {
		idGen = defaultTaskID
	}
	if dispatcher == nil {
		dispatcher = NoopDispatcher{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeRPCError(w, nil, rpcErrParse, "parse error: "+err.Error(), http.StatusOK)
			return
		}
		if req.JSONRPC != "2.0" {
			writeRPCError(w, req.ID, rpcErrInvalidReq, "jsonrpc must be 2.0", http.StatusOK)
			return
		}
		switch req.Method {
		case "message/send":
			handleMessageSend(w, r, agentID, store, dispatcher, idGen, req)
		case "tasks/get":
			handleTasksGet(w, r, agentID, store, req)
		case "tasks/cancel":
			handleTasksCancel(w, r, agentID, store, req)
		default:
			writeRPCError(w, req.ID, rpcErrMethodNotFnd, "unknown method: "+req.Method, http.StatusOK)
		}
	})
}

type messageSendParams struct {
	Message Message `json:"message"`
}

type tasksIDParams struct {
	ID string `json:"id"`
}

func handleMessageSend(w http.ResponseWriter, r *http.Request, agentID string,
	store *TaskStore, dispatcher Dispatcher, idGen func() string, req jsonRPCRequest) {
	var p messageSendParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, rpcErrInvalidParams, "invalid params: "+err.Error(), http.StatusOK)
		return
	}
	if p.Message.MessageID == "" {
		writeRPCError(w, req.ID, rpcErrInvalidParams, "message.messageId required", http.StatusOK)
		return
	}
	if p.Message.Role == "" {
		p.Message.Role = "user"
	}
	taskID := idGen()
	task := store.Create(agentID, taskID, p.Message)

	// Dispatch on a detached context — the JSON-RPC caller gets its
	// submitted-state Task back immediately; the dispatcher runs async
	// and updates the store as work progresses.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := dispatcher.Dispatch(ctx, agentID, p.Message, taskID, store); err != nil {
			store.Update(agentID, taskID,
				TaskStatus{State: TaskStateFailed},
				&Message{
					MessageID: taskID + ".err",
					Role:      "agent",
					Parts:     json.RawMessage(fmt.Sprintf(`[{"kind":"text","text":%q}]`, err.Error())),
				})
		}
	}()

	writeRPCResult(w, req.ID, task)
}

func handleTasksGet(w http.ResponseWriter, r *http.Request, agentID string,
	store *TaskStore, req jsonRPCRequest) {
	var p tasksIDParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, rpcErrInvalidParams, "invalid params: "+err.Error(), http.StatusOK)
		return
	}
	if p.ID == "" {
		writeRPCError(w, req.ID, rpcErrInvalidParams, "id required", http.StatusOK)
		return
	}
	task, ok := store.Get(agentID, p.ID)
	if !ok {
		writeRPCError(w, req.ID, rpcErrTaskNotFound, "task not found", http.StatusOK)
		return
	}
	writeRPCResult(w, req.ID, task)
}

func handleTasksCancel(w http.ResponseWriter, r *http.Request, agentID string,
	store *TaskStore, req jsonRPCRequest) {
	var p tasksIDParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, rpcErrInvalidParams, "invalid params: "+err.Error(), http.StatusOK)
		return
	}
	if p.ID == "" {
		writeRPCError(w, req.ID, rpcErrInvalidParams, "id required", http.StatusOK)
		return
	}
	task, ok := store.Update(agentID, p.ID,
		TaskStatus{State: TaskStateCanceled}, nil)
	if !ok {
		writeRPCError(w, req.ID, rpcErrTaskNotFound, "task not found", http.StatusOK)
		return
	}
	writeRPCResult(w, req.ID, task)
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, http.StatusOK, jsonRPCResponse{JSONRPC: "2.0", Result: result, ID: id})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string, _ int) {
	writeJSON(w, http.StatusOK, jsonRPCResponse{
		JSONRPC: "2.0",
		Error:   &jsonRPCError{Code: code, Message: msg},
		ID:      id,
	})
}

// defaultTaskID returns task-<unix_ns>. Monotonic enough for tests and
// the MVP demo; a real uuid generator is a follow-up if collisions ever
// matter.
func defaultTaskID() string {
	return fmt.Sprintf("task-%d", time.Now().UnixNano())
}

// ErrDispatch is returned by dispatchers when the agent can't be reached
// (e.g. pane closed, driver not attached). Kept as a sentinel so callers
// can distinguish "not yet implemented" from genuine failures.
var ErrDispatch = errors.New("a2a: dispatch failed")
