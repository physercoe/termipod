package hostrunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/termipod/hub/internal/hostrunner/a2a"
)

func datasetTestRunner() *Runner {
	return &Runner{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// fixtureRoot resolves one of datasetmeta's pinned LeRobot trees to an absolute
// path — the verbs refuse relative roots, and rightly so.
func fixtureRoot(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("datasetmeta", "testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(abs, "meta")); err != nil {
		t.Fatalf("fixture %s missing (run datasetmeta/testdata/fetch-fixtures.sh): %v", rel, err)
	}
	return abs
}

func callDatasetVerb(t *testing.T, kind string, payload map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	resp := datasetTestRunner().handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r-1", Kind: kind, Payload: raw,
	})
	if resp == nil {
		t.Fatalf("%s returned nil — the dispatcher does not route it", kind)
	}
	body, err := base64.StdEncoding.DecodeString(resp.BodyB64)
	if err != nil {
		t.Fatalf("body was not base64: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("body was not JSON: %v (%q)", err, body)
	}
	return resp.Status, out
}

// A verb with no dispatcher case returns nil, which the tunnel loop turns into
// unknown_verb — invisible to the hub in exactly the way a missing MCP
// dispatcher case is invisible to an agent. Pinned so a handler cannot ship
// without its route.
func TestDatasetVerbsAreRouted(t *testing.T) {
	r := datasetTestRunner()
	for _, kind := range []string{"host.dataset_digest", "host.dataset_episodes"} {
		resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
			ReqID: "r", Kind: kind, Payload: json.RawMessage(`{}`),
		})
		if resp == nil {
			t.Errorf("%s is not routed by handleHostVerb", kind)
		}
	}
	// The negative control: an unrouted verb must still return nil, otherwise
	// the assertion above would pass for any string at all.
	if resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r", Kind: "host.dataset_nonexistent", Payload: json.RawMessage(`{}`),
	}); resp != nil {
		t.Errorf("an unknown verb returned %+v, want nil", resp)
	}
}

func TestHostDatasetDigestVerb(t *testing.T) {
	root := fixtureRoot(t, "lerobot/nyu_rot_dataset/v3.0")
	status, body := callDatasetVerb(t, "host.dataset_digest", map[string]any{"root_path": root})
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %v", status, body)
	}
	digest, ok := body["digest"].(map[string]any)
	if !ok {
		t.Fatalf("no digest in %v", body)
	}
	if digest["format"] != "lerobot_v3.0" {
		t.Errorf("format = %v", digest["format"])
	}
	if digest["total_episodes"].(float64) != 14 || digest["total_frames"].(float64) != 440 {
		t.Errorf("counts = %v / %v", digest["total_episodes"], digest["total_frames"])
	}
	// The fingerprint rides along so the hub can judge staleness later without
	// a second round-trip.
	fp, ok := body["fingerprint"].(map[string]any)
	if !ok || fp["files"].(float64) != 4 {
		t.Errorf("fingerprint = %v", body["fingerprint"])
	}
}

func TestHostDatasetEpisodesVerb(t *testing.T) {
	root := fixtureRoot(t, "lerobot/nyu_rot_dataset/v2.1")
	status, body := callDatasetVerb(t, "host.dataset_episodes", map[string]any{
		"root_path": root, "offset": 2, "limit": 3,
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %v", status, body)
	}
	eps, ok := body["episodes"].([]any)
	if !ok || len(eps) != 3 {
		t.Fatalf("episodes = %v", body["episodes"])
	}
	first := eps[0].(map[string]any)
	if first["index"].(float64) != 2 {
		t.Errorf("window did not start at the requested offset: %v", first)
	}
	if body["total"].(float64) != 14 {
		t.Errorf("total = %v, want the dataset count not the page size", body["total"])
	}
}

// An unknown generation must come back as a typed answer carrying the version,
// not as a generic read failure: the version is the only thing that tells a
// user what to do next.
func TestHostDatasetVerbUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "info.json"),
		[]byte(`{"codebase_version":"v4.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"host.dataset_digest", "host.dataset_episodes"} {
		status, body := callDatasetVerb(t, kind, map[string]any{"root_path": dir})
		if status != http.StatusUnprocessableEntity {
			t.Errorf("%s status = %d, want 422 (body=%v)", kind, status, body)
		}
		if body["error"] != "unsupported_format" || body["codebase_version"] != "v4.0" {
			t.Errorf("%s body = %v", kind, body)
		}
	}
}

func TestHostDatasetVerbRootPathValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		root any
	}{
		// Relative paths would resolve against the host-runner's working
		// directory — an implementation detail that differs between a systemd
		// unit and a shell, so resolving one lands somewhere the caller cannot
		// predict. Refusing is the honest answer.
		{"relative", "relative/path"},
		{"dot relative", "./data"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := callDatasetVerb(t, "host.dataset_digest",
				map[string]any{"root_path": tc.root})
			if status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", status)
			}
			if body["error"] != "bad_root_path" {
				t.Errorf("error = %v", body["error"])
			}
		})
	}
}

func TestHostDatasetVerbMissingRootReadsAsFailure(t *testing.T) {
	status, body := callDatasetVerb(t, "host.dataset_digest",
		map[string]any{"root_path": filepath.Join(t.TempDir(), "nope")})
	if status != http.StatusBadRequest || body["error"] != "read_failed" {
		t.Errorf("status = %d body = %v", status, body)
	}
}

// The traversal guard lives in datasetmeta, but the verb is where an attacker-
// supplied root would arrive, so the refusal is pinned at this boundary too.
func TestHostDatasetVerbCannotEscapeTheRoot(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"codebase_version":"v2.1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A root whose meta/ would have to resolve through .. to reach anything.
	root := filepath.Join(dir, "sub")
	if err := os.MkdirAll(filepath.Join(root, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "meta", "info.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Reading through the symlink is allowed (it resolves inside the root's
	// own meta/), but the response must never contain file bytes — only a
	// fold. This asserts the shape of what escapes: keys, not contents.
	status, body := callDatasetVerb(t, "host.dataset_digest", map[string]any{"root_path": root})
	if status == http.StatusOK {
		if _, ok := body["digest"]; !ok {
			t.Errorf("200 without a digest: %v", body)
		}
		for _, forbidden := range []string{"content", "bytes", "raw"} {
			if _, ok := body[forbidden]; ok {
				t.Errorf("verb response carries %q — it must return a fold, not file bytes", forbidden)
			}
		}
	}
}
