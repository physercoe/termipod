package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordingDispatcher struct {
	mu    sync.Mutex
	calls []dispatchCall
	// onDispatch, if set, drives the store synchronously from inside the
	// Dispatch call — useful for testing the completion path without a
	// real goroutine race.
	onDispatch func(agentID, taskID string, msg Message, store *TaskStore)
	err        error
}

type dispatchCall struct {
	AgentID string
	TaskID  string
	Msg     Message
}

func (r *recordingDispatcher) Dispatch(ctx context.Context, agentID string, msg Message, taskID string, store *TaskStore) error {
	r.mu.Lock()
	r.calls = append(r.calls, dispatchCall{AgentID: agentID, TaskID: taskID, Msg: msg})
	cb := r.onDispatch
	r.mu.Unlock()
	if cb != nil {
		cb(agentID, taskID, msg, store)
	}
	return r.err
}

func doRPC(t *testing.T, h http.Handler, method string, params any) jsonRPCResponse {
	t.Helper()
	p, _ := json.Marshal(params)
	body, _ := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  p,
		ID:      json.RawMessage(`1`),
	})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	return resp
}

func TestMessageSend_ReturnsSubmittedTask(t *testing.T) {
	store := NewTaskStore()
	disp := &recordingDispatcher{}
	var idCount int32
	idGen := func() string {
		n := atomic.AddInt32(&idCount, 1)
		return fmt.Sprintf("task-%d", n)
	}
	h := TaskRPCHandler("agent-worker", store, disp, idGen)

	resp := doRPC(t, h, "message/send", messageSendParams{
		Message: Message{
			MessageID: "msg-1",
			Role:      "user",
			Parts:     json.RawMessage(`[{"kind":"text","text":"train me"}]`),
		},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// Re-marshal result so we can inspect it as a Task without a type
	// assertion through any.
	b, _ := json.Marshal(resp.Result)
	var task Task
	if err := json.Unmarshal(b, &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if task.ID != "task-1" {
		t.Errorf("id=%q, want task-1", task.ID)
	}
	if task.Status.State != TaskStateSubmitted {
		t.Errorf("state=%q, want submitted", task.Status.State)
	}

	// Dispatcher runs async; give it a beat to fire.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		disp.mu.Lock()
		n := len(disp.calls)
		disp.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	disp.mu.Lock()
	if len(disp.calls) != 1 || disp.calls[0].TaskID != "task-1" {
		t.Errorf("dispatcher calls = %+v, want one call for task-1", disp.calls)
	}
	disp.mu.Unlock()
}

func TestMessageSend_RejectsMissingMessageID(t *testing.T) {
	h := TaskRPCHandler("agent-worker", NewTaskStore(), nil, nil)
	resp := doRPC(t, h, "message/send", messageSendParams{
		Message: Message{Role: "user"},
	})
	if resp.Error == nil || resp.Error.Code != rpcErrInvalidParams {
		t.Errorf("error=%+v, want invalid-params", resp.Error)
	}
}

func TestTasksGet_ReturnsStoredTask(t *testing.T) {
	store := NewTaskStore()
	disp := &recordingDispatcher{
		onDispatch: func(agentID, taskID string, msg Message, s *TaskStore) {
			s.Update(agentID, taskID, TaskStatus{State: TaskStateCompleted}, &Message{
				MessageID: taskID + ".done",
				Role:      "agent",
				Parts:     json.RawMessage(`[{"kind":"text","text":"ok"}]`),
			})
		},
	}
	idGen := func() string { return "task-fixed" }
	h := TaskRPCHandler("agent-x", store, disp, idGen)

	doRPC(t, h, "message/send", messageSendParams{
		Message: Message{MessageID: "m", Role: "user", Parts: json.RawMessage(`[]`)},
	})
	// Let the goroutine land — onDispatch runs in the dispatcher's goroutine.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		task, _ := store.Get("agent-x", "task-fixed")
		if task != nil && task.Status.State == TaskStateCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	resp := doRPC(t, h, "tasks/get", tasksIDParams{ID: "task-fixed"})
	if resp.Error != nil {
		t.Fatalf("err=%+v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var task Task
	_ = json.Unmarshal(b, &task)
	if task.Status.State != TaskStateCompleted {
		t.Errorf("state=%q, want completed", task.Status.State)
	}
	if len(task.History) != 2 {
		t.Errorf("history len=%d, want 2 (user + agent)", len(task.History))
	}
}

func TestTasksGet_UnknownReturnsRPCError(t *testing.T) {
	h := TaskRPCHandler("agent-x", NewTaskStore(), nil, nil)
	resp := doRPC(t, h, "tasks/get", tasksIDParams{ID: "nope"})
	if resp.Error == nil || resp.Error.Code != rpcErrTaskNotFound {
		t.Errorf("err=%+v, want task-not-found", resp.Error)
	}
}

func TestTasksCancel_FreezesState(t *testing.T) {
	store := NewTaskStore()
	idGen := func() string { return "task-1" }
	h := TaskRPCHandler("agent-x", store, NoopDispatcher{}, idGen)

	doRPC(t, h, "message/send", messageSendParams{
		Message: Message{MessageID: "m", Role: "user", Parts: json.RawMessage(`[]`)},
	})
	resp := doRPC(t, h, "tasks/cancel", tasksIDParams{ID: "task-1"})
	if resp.Error != nil {
		t.Fatalf("err=%+v", resp.Error)
	}

	// A late completion after cancel must not flip the terminal state.
	store.Update("agent-x", "task-1", TaskStatus{State: TaskStateCompleted}, nil)
	task, _ := store.Get("agent-x", "task-1")
	if task.Status.State != TaskStateCanceled {
		t.Errorf("state=%q, want canceled (terminal-frozen)", task.Status.State)
	}
}

func TestJSONRPC_UnknownMethod(t *testing.T) {
	h := TaskRPCHandler("a", NewTaskStore(), nil, nil)
	resp := doRPC(t, h, "does/not/exist", nil)
	if resp.Error == nil || resp.Error.Code != rpcErrMethodNotFnd {
		t.Errorf("err=%+v, want method-not-found", resp.Error)
	}
}

func TestJSONRPC_BadBody(t *testing.T) {
	h := TaskRPCHandler("a", NewTaskStore(), nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("{not json")))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	var resp jsonRPCResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Error == nil || resp.Error.Code != rpcErrParse {
		t.Errorf("err=%+v, want parse error", resp.Error)
	}
}

func TestServer_RoutesJSONRPCToTasksHandler(t *testing.T) {
	// End-to-end smoke through Server.Handler() — the routing change from
	// this wedge: POST /a2a/<id> with a JSON-RPC body should reach the
	// task handler, not return 404.
	s := &Server{
		Source: func(ctx context.Context) ([]AgentInfo, error) {
			return []AgentInfo{{ID: "agent-x", Handle: "worker.ml"}}, nil
		},
		Dispatcher: NoopDispatcher{},
	}
	body, _ := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "tasks/get",
		Params:  json.RawMessage(`{"id":"nope"}`),
		ID:      json.RawMessage(`1`),
	})
	req := httptest.NewRequest(http.MethodPost, "/a2a/agent-x", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp jsonRPCResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Error == nil || resp.Error.Code != rpcErrTaskNotFound {
		t.Errorf("expected task-not-found, got %+v", resp.Error)
	}
}

func TestServer_UnknownAgent404s(t *testing.T) {
	s := &Server{
		Source: func(ctx context.Context) ([]AgentInfo, error) {
			return []AgentInfo{{ID: "agent-x", Handle: "x"}}, nil
		},
	}
	body, _ := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0", Method: "tasks/get", Params: json.RawMessage(`{"id":"n"}`),
		ID: json.RawMessage(`1`),
	})
	req := httptest.NewRequest(http.MethodPost, "/a2a/unknown-agent", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rr.Code)
	}
}
