package hostrunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/termipod/hub/internal/buildinfo"
	"github.com/termipod/hub/internal/hostrunner/a2a"
	"github.com/termipod/hub/internal/selfupdate"
)

// TestHandleHostPing_ReportsBuildInfo confirms the read-side host.ping
// verb reflects this binary's build identity and never schedules an
// exit (no verbExit override needed — it would fail the test if hit).
func TestHandleHostPing_ReportsBuildInfo(t *testing.T) {
	r := &Runner{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r-ping", Kind: "host.ping",
	})
	if resp == nil || resp.Status != http.StatusOK {
		t.Fatalf("resp = %+v, want 200", resp)
	}
	body, _ := base64.StdEncoding.DecodeString(resp.BodyB64)
	var parsed struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
		TS      string `json:"ts"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse body %q: %v", string(body), err)
	}
	if !parsed.OK {
		t.Error("ok = false")
	}
	if parsed.Version != buildinfo.Version {
		t.Errorf("version = %q, want %q", parsed.Version, buildinfo.Version)
	}
	if parsed.TS == "" {
		t.Error("ts is empty")
	}
}

// TestHandleHostVerb_UnknownVerb_ReturnsNil pins the contract that
// unrecognised host.* verbs short-circuit with nil so the tunnel loop
// emits the canonical unknown_verb envelope. The hub-side error shape
// is asserted in a2a/tunnel_test.go.
func TestHandleHostVerb_UnknownVerb_ReturnsNil(t *testing.T) {
	r := &Runner{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	got := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r1",
		Kind:  "host.does_not_exist",
	})
	if got != nil {
		t.Fatalf("unknown verb should return nil; got %+v", got)
	}
}

// TestHandleHostTokenRotate_PersistsAndSwaps drives the happy path:
// the verb persists the new bearer to the state dir AND swaps it into
// the live Client, and a fresh ResolveBearerToken picks it up.
func TestHandleHostTokenRotate_PersistsAndSwaps(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		StateDir: dir,
		HostName: "host-a",
		Client:   NewClient("http://hub.example", "old-token", "team-1"),
	}
	payload, _ := json.Marshal(map[string]any{"token": "new-token", "reason": "test"})
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r-rot", Kind: "host.token_rotate", Payload: payload,
	})
	if resp == nil || resp.Status != http.StatusOK {
		t.Fatalf("resp = %+v, want 200", resp)
	}
	if got := r.Client.Bearer(); got != "new-token" {
		t.Errorf("live bearer = %q, want new-token", got)
	}
	tok, rotated := ResolveBearerToken(dir, "http://hub.example", "team-1", "host-a", "old-token")
	if !rotated || tok != "new-token" {
		t.Errorf("ResolveBearerToken = (%q, %v), want (new-token, true)", tok, rotated)
	}
}

// TestHandleHostTokenRotate_NoStateDirRefuses pins brick-safety: with
// no state dir the new token cannot survive a restart, so the verb
// refuses (500) and does NOT swap the live token.
func TestHandleHostTokenRotate_NoStateDirRefuses(t *testing.T) {
	r := &Runner{
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		HostName: "host-a",
		Client:   NewClient("http://hub", "old-token", "team-1"),
	}
	payload, _ := json.Marshal(map[string]any{"token": "new-token"})
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r", Kind: "host.token_rotate", Payload: payload,
	})
	if resp == nil || resp.Status != http.StatusInternalServerError {
		t.Fatalf("resp = %+v, want 500", resp)
	}
	if r.Client.Bearer() != "old-token" {
		t.Error("token must not swap when it cannot be persisted")
	}
}

// TestHandleHostTokenRotate_EmptyTokenRefuses rejects an empty token.
func TestHandleHostTokenRotate_EmptyTokenRefuses(t *testing.T) {
	r := &Runner{
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		StateDir: t.TempDir(),
		HostName: "host-a",
		Client:   NewClient("http://hub", "old-token", "team-1"),
	}
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r", Kind: "host.token_rotate",
	})
	if resp == nil || resp.Status != http.StatusBadRequest {
		t.Fatalf("resp = %+v, want 400", resp)
	}
}

// TestHandleHostShutdown_AcksAndExits drives the full host.shutdown
// path with stubbed exit so the test doesn't terminate the process.
// Asserts: response is 200 with acked body, exit fires with code 0,
// and the exit happens AFTER the response is constructed (the
// goroutine sleep is squashed to ~0 here).
func TestHandleHostShutdown_AcksAndExits(t *testing.T) {
	prevExit, prevDelay := verbExit, verbExitDelay
	t.Cleanup(func() {
		verbExit = prevExit
		verbExitDelay = prevDelay
	})
	var exited atomic.Int32
	var gotCode atomic.Int32
	gotCode.Store(-1)
	exitCh := make(chan struct{}, 1)
	verbExit = func(code int) {
		gotCode.Store(int32(code))
		exited.Add(1)
		exitCh <- struct{}{}
	}
	verbExitDelay = 1 * time.Millisecond

	r := &Runner{
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		drivers: map[string]Driver{},
	}
	payload, _ := json.Marshal(map[string]any{
		"reason":     "test-update",
		"force_kill": false,
	})
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID:   "r-shutdown",
		Kind:    "host.shutdown",
		Payload: payload,
	})
	if resp == nil {
		t.Fatal("expected response envelope, got nil")
	}
	if resp.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.Status)
	}
	body, _ := base64.StdEncoding.DecodeString(resp.BodyB64)
	var parsed struct {
		Acked             bool   `json:"acked"`
		StragglersStopped int    `json:"stragglers_stopped"`
		Reason            string `json:"reason"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse body %q: %v", string(body), err)
	}
	if !parsed.Acked {
		t.Errorf("acked = false")
	}
	if parsed.Reason != "test-update" {
		t.Errorf("reason = %q, want test-update", parsed.Reason)
	}
	// The handler scheduled exit on a goroutine; wait for it.
	select {
	case <-exitCh:
	case <-time.After(time.Second):
		t.Fatal("verbExit was not called")
	}
	if gotCode.Load() != 0 {
		t.Errorf("exit code = %d, want 0", gotCode.Load())
	}
}

// TestHandleHostRestart_AcksAndExits75 confirms host.restart shares the
// host.shutdown body but exits 75 (bounce) instead of 0 (true down).
func TestHandleHostRestart_AcksAndExits75(t *testing.T) {
	prevExit, prevDelay := verbExit, verbExitDelay
	t.Cleanup(func() { verbExit, verbExitDelay = prevExit, prevDelay })
	exitCh := make(chan int, 1)
	verbExit = func(code int) { exitCh <- code }
	verbExitDelay = 1 * time.Millisecond

	r := &Runner{
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		drivers: map[string]Driver{},
	}
	payload, _ := json.Marshal(map[string]any{"reason": "bounce"})
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r-restart", Kind: "host.restart", Payload: payload,
	})
	if resp == nil || resp.Status != http.StatusOK {
		t.Fatalf("resp = %+v, want 200", resp)
	}
	select {
	case code := <-exitCh:
		if code != 75 {
			t.Errorf("exit code = %d, want 75", code)
		}
	case <-time.After(time.Second):
		t.Fatal("verbExit was not called")
	}
}

// TestHandleHostUpdate_SuccessExits75 stubs the self-update routine to
// succeed and asserts the verb acks ok and schedules exit 75 so the
// supervisor respawns with the new binary.
func TestHandleHostUpdate_SuccessExits75(t *testing.T) {
	prevExit, prevDelay, prevSU := verbExit, verbExitDelay, runSelfUpdate
	t.Cleanup(func() {
		verbExit, verbExitDelay, runSelfUpdate = prevExit, prevDelay, prevSU
	})
	exitCh := make(chan int, 1)
	verbExit = func(code int) { exitCh <- code }
	verbExitDelay = 1 * time.Millisecond
	runSelfUpdate = func(_ context.Context, opt selfupdate.Options) (*selfupdate.Result, error) {
		if opt.Binary != "host-runner" {
			t.Errorf("Binary = %q, want host-runner", opt.Binary)
		}
		return &selfupdate.Result{
			Binary: "host-runner", FromVersion: "v1.0.0", ToVersion: "v1.0.1",
			Asset: "termipod-host-runner-v1.0.1-linux-amd64.tar.gz",
		}, nil
	}

	r := &Runner{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	payload, _ := json.Marshal(map[string]any{"version": "v1.0.1", "reason": "update-all"})
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r-update", Kind: "host.update", Payload: payload,
	})
	if resp == nil || resp.Status != http.StatusOK {
		t.Fatalf("resp = %+v, want 200", resp)
	}
	body, _ := base64.StdEncoding.DecodeString(resp.BodyB64)
	var parsed struct {
		OK        bool   `json:"ok"`
		ToVersion string `json:"to_version"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse body %q: %v", string(body), err)
	}
	if !parsed.OK || parsed.ToVersion != "v1.0.1" {
		t.Errorf("body = %+v, want ok + to_version v1.0.1", parsed)
	}
	select {
	case code := <-exitCh:
		if code != 75 {
			t.Errorf("exit code = %d, want 75", code)
		}
	case <-time.After(time.Second):
		t.Fatal("verbExit was not called")
	}
}

// TestHandleHostUpdate_PostsProgress asserts the two-stage shape of
// host.update: the synchronous resolve is a dry-run, the ack carries
// started:true, and the background stage streams throttled progress
// samples to the hub — ending with a terminal "done" before exit 75.
func TestHandleHostUpdate_PostsProgress(t *testing.T) {
	prevExit, prevDelay, prevSU := verbExit, verbExitDelay, runSelfUpdate
	t.Cleanup(func() {
		verbExit, verbExitDelay, runSelfUpdate = prevExit, prevDelay, prevSU
	})
	exitCh := make(chan int, 1)
	verbExit = func(code int) { exitCh <- code }
	verbExitDelay = 1 * time.Millisecond
	var dryRuns []bool
	runSelfUpdate = func(_ context.Context, opt selfupdate.Options) (*selfupdate.Result, error) {
		dryRuns = append(dryRuns, opt.DryRun)
		if !opt.DryRun && opt.OnProgress != nil {
			opt.OnProgress(selfupdate.Progress{Phase: selfupdate.PhaseDownloading, Done: 5, Total: 10})
			opt.OnProgress(selfupdate.Progress{Phase: selfupdate.PhaseInstalling, Done: 10, Total: 10})
		}
		return &selfupdate.Result{
			Binary: "host-runner", FromVersion: "v1.0.0", ToVersion: "v1.0.1",
		}, nil
	}

	progressCh := make(chan UpdateProgressIn, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost ||
			req.URL.Path != "/v1/teams/t/hosts/h1/update-progress" {
			t.Errorf("unexpected call: %s %s", req.Method, req.URL.Path)
		}
		var in UpdateProgressIn
		_ = json.NewDecoder(req.Body).Decode(&in)
		progressCh <- in
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := &Runner{
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Client: NewClient(srv.URL, "tok", "t"),
		HostID: "h1",
	}
	payload, _ := json.Marshal(map[string]any{"version": "v1.0.1"})
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r-update-prog", Kind: "host.update", Payload: payload,
	})
	if resp == nil || resp.Status != http.StatusOK {
		t.Fatalf("resp = %+v, want 200", resp)
	}
	body, _ := base64.StdEncoding.DecodeString(resp.BodyB64)
	var ack struct {
		OK      bool   `json:"ok"`
		Started bool   `json:"started"`
		ToVer   string `json:"to_version"`
	}
	if err := json.Unmarshal(body, &ack); err != nil {
		t.Fatalf("parse ack %q: %v", string(body), err)
	}
	if !ack.OK || !ack.Started || ack.ToVer != "v1.0.1" {
		t.Errorf("ack = %+v, want ok+started with to_version v1.0.1", ack)
	}

	select {
	case code := <-exitCh:
		if code != 75 {
			t.Errorf("exit code = %d, want 75", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("verbExit was not called")
	}

	// The resolve ran as a dry-run; the install ran for real.
	if len(dryRuns) != 2 || !dryRuns[0] || dryRuns[1] {
		t.Errorf("self-update calls DryRun = %v, want [true false]", dryRuns)
	}

	// Progress stream: downloading + installing samples, terminal done.
	var got []UpdateProgressIn
	for done := false; !done; {
		select {
		case p := <-progressCh:
			got = append(got, p)
			if p.Phase == "done" {
				done = true
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("progress stream stalled at %+v, want a done sample", got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("progress samples = %+v, want [downloading installing done]", got)
	}
	if got[0].Phase != "downloading" || got[0].Done != 5 || got[0].Total != 10 {
		t.Errorf("first sample = %+v, want downloading 5/10", got[0])
	}
	if got[1].Phase != "installing" {
		t.Errorf("second sample = %+v, want installing", got[1])
	}
	for i, p := range got {
		if p.ToVersion != "v1.0.1" {
			t.Errorf("sample %d to_version = %q, want v1.0.1", i, p.ToVersion)
		}
	}
}

// TestHandleHostUpdate_BackgroundFailurePostsError stubs a download-stage
// failure (after a clean resolve) and asserts the host reports the error
// sample and stays up — no exit, old binary untouched.
func TestHandleHostUpdate_BackgroundFailurePostsError(t *testing.T) {
	prevExit, prevDelay, prevSU := verbExit, verbExitDelay, runSelfUpdate
	t.Cleanup(func() {
		verbExit, verbExitDelay, runSelfUpdate = prevExit, prevDelay, prevSU
	})
	verbExit = func(code int) { t.Errorf("verbExit(%d) on the failure path", code) }
	verbExitDelay = 1 * time.Millisecond
	runSelfUpdate = func(_ context.Context, opt selfupdate.Options) (*selfupdate.Result, error) {
		if opt.DryRun {
			return &selfupdate.Result{Binary: "host-runner", FromVersion: "v1.0.0", ToVersion: "v1.0.1"}, nil
		}
		return nil, errors.New("sha256 mismatch")
	}

	progressCh := make(chan UpdateProgressIn, 4)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/teams/t/hosts/h1/update-progress", func(w http.ResponseWriter, req *http.Request) {
		var in UpdateProgressIn
		_ = json.NewDecoder(req.Body).Decode(&in)
		progressCh <- in
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := &Runner{
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Client: NewClient(srv.URL, "tok", "t"),
		HostID: "h1",
	}
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r-update-dlfail", Kind: "host.update",
	})
	if resp == nil || resp.Status != http.StatusOK {
		t.Fatalf("resp = %+v, want 200 (resolve succeeded; failure is post-ack)", resp)
	}
	select {
	case p := <-progressCh:
		if p.Phase != "error" || !strings.Contains(p.Error, "sha256 mismatch") {
			t.Errorf("sample = %+v, want error mentioning the mismatch", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no error progress sample posted")
	}
}

// asserts the verb returns 500 and does NOT exit — the host keeps
// running on the old binary.
func TestHandleHostUpdate_FailureStaysUp(t *testing.T) {
	prevExit, prevSU := verbExit, runSelfUpdate
	t.Cleanup(func() { verbExit, runSelfUpdate = prevExit, prevSU })
	verbExit = func(code int) { t.Fatalf("verbExit(%d) called on the failure path", code) }
	runSelfUpdate = func(_ context.Context, _ selfupdate.Options) (*selfupdate.Result, error) {
		return nil, errors.New("sha256 mismatch")
	}

	r := &Runner{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r-update-fail", Kind: "host.update",
	})
	if resp == nil || resp.Status != http.StatusInternalServerError {
		t.Fatalf("resp = %+v, want 500", resp)
	}
	body, _ := base64.StdEncoding.DecodeString(resp.BodyB64)
	var parsed struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)
	if parsed.OK || parsed.Error == "" {
		t.Errorf("body = %+v, want ok=false with an error", parsed)
	}
}
