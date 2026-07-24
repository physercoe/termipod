package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSHA256SUMS(t *testing.T) {
	const digest = "abc123abc123abc123abc123abc123abc123abc123abc123abc123abc123abcd"
	data := digest + "  host-runner-1.0.0-linux-amd64.tar.gz\n" +
		strings.Repeat("f", 64) + " *hub-server-1.0.0-linux-amd64.tar.gz\n" +
		"# a comment line that must be ignored\n"

	tests := []struct {
		name    string
		wantHit bool
		want    string
	}{
		{"host-runner-1.0.0-linux-amd64.tar.gz", true, digest},
		{"hub-server-1.0.0-linux-amd64.tar.gz", true, strings.Repeat("f", 64)},
		{"host-runner-9.9.9-linux-amd64.tar.gz", false, ""},
	}
	for _, tc := range tests {
		got, ok := parseSHA256SUMS(data, tc.name)
		if ok != tc.wantHit {
			t.Errorf("%s: hit = %v, want %v", tc.name, ok, tc.wantHit)
		}
		if got != tc.want {
			t.Errorf("%s: digest = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRun_HappyPath(t *testing.T) {
	tag := "host-v1.0.999-alpha"
	asset := artifactName("host-runner", tag)
	newBytes := []byte("NEW host-runner " + tag + "\n")
	tarball := makeTarGz(t, "host-runner", newBytes)

	sums := sha256hex(tarball) + "  " + asset + "\n" +
		strings.Repeat("0", 64) + "  hub-server-" + tag + "-other.tar.gz\n"
	srv := newReleaseServer(t, releaseFixture{tag: tag, tarAsset: asset, tarball: tarball, sums: sums})
	defer srv.Close()

	dir := t.TempDir()
	installPath := filepath.Join(dir, "host-runner")
	if err := os.WriteFile(installPath, []byte("OLD binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	gh := &ghClient{repo: "x/y", apiBase: srv.URL, http: srv.Client()}
	res, err := run(context.Background(), Options{
		Binary: "host-runner", Version: tag, InstallPath: installPath,
	}, gh)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ToVersion != tag || res.Asset != asset {
		t.Errorf("result = %+v, want ToVersion=%s Asset=%s", res, tag, asset)
	}
	if got, _ := os.ReadFile(installPath); string(got) != string(newBytes) {
		t.Errorf("binary not replaced: got %q", got)
	}
	if fi, _ := os.Stat(installPath); fi.Mode().Perm() != 0o755 {
		t.Errorf("install mode = %v, want 0755", fi.Mode().Perm())
	}
	// Only the install path should remain — staging files cleaned up.
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("staging files left behind: %v", entries)
	}
}

func TestRun_SHAMismatch(t *testing.T) {
	tag := "host-v1.0.999-alpha"
	asset := artifactName("host-runner", tag)
	tarball := makeTarGz(t, "host-runner", []byte("tampered payload"))
	// SHA256SUMS advertises a digest that does not match the tarball.
	sums := strings.Repeat("a", 64) + "  " + asset + "\n"
	srv := newReleaseServer(t, releaseFixture{tag: tag, tarAsset: asset, tarball: tarball, sums: sums})
	defer srv.Close()

	dir := t.TempDir()
	installPath := filepath.Join(dir, "host-runner")
	if err := os.WriteFile(installPath, []byte("OLD binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	gh := &ghClient{repo: "x/y", apiBase: srv.URL, http: srv.Client()}
	_, err := run(context.Background(), Options{
		Binary: "host-runner", Version: tag, InstallPath: installPath,
	}, gh)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err = %v, want sha256 mismatch", err)
	}
	if got, _ := os.ReadFile(installPath); string(got) != "OLD binary" {
		t.Errorf("binary replaced despite mismatch: got %q", got)
	}
}

func TestRun_ReleasePredatesSplit(t *testing.T) {
	tag := "host-v1.0.500-alpha"
	srv := newReleaseServer(t, releaseFixture{tag: tag, omitTar: true, sums: "x\n"})
	defer srv.Close()

	gh := &ghClient{repo: "x/y", apiBase: srv.URL, http: srv.Client()}
	_, err := run(context.Background(), Options{
		Binary: "host-runner", Version: tag, InstallPath: filepath.Join(t.TempDir(), "host-runner"),
	}, gh)
	if err == nil || !strings.Contains(err.Error(), "per-binary release split") {
		t.Fatalf("err = %v, want per-binary-release-split message", err)
	}
}

func TestRun_DryRunTouchesNothing(t *testing.T) {
	tag := "hub-v1.0.999-alpha"
	asset := artifactName("hub-server", tag)
	tarball := makeTarGz(t, "hub-server", []byte("new"))
	sums := sha256hex(tarball) + "  " + asset + "\n"
	srv := newReleaseServer(t, releaseFixture{tag: tag, tarAsset: asset, tarball: tarball, sums: sums})
	defer srv.Close()

	dir := t.TempDir()
	installPath := filepath.Join(dir, "hub-server")
	if err := os.WriteFile(installPath, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	gh := &ghClient{repo: "x/y", apiBase: srv.URL, http: srv.Client()}
	res, err := run(context.Background(), Options{
		Binary: "hub-server", Version: tag, InstallPath: installPath, DryRun: true,
	}, gh)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ToVersion != tag {
		t.Errorf("ToVersion = %q, want %q", res.ToVersion, tag)
	}
	if got, _ := os.ReadFile(installPath); string(got) != "OLD" {
		t.Errorf("dry run replaced the binary: got %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("dry run left staging files: %v", entries)
	}
}

func TestRun_UnknownBinary(t *testing.T) {
	_, err := run(context.Background(), Options{Binary: "bogus"}, newGHClient(""))
	if err == nil || !strings.Contains(err.Error(), "unknown binary") {
		t.Fatalf("err = %v, want unknown binary", err)
	}
}

// resolveRelease must confine the channel search to the binary's lane: a
// host-runner alpha resolve skips newer mobile-v / electron-v / hub-v releases
// and lands on the newest host-v. (Regression guard — before the split it took
// the newest non-draft release of ANY lane, which for a server binary is often
// a desktop prerelease that carries no tarball.)
func TestResolveRelease_LaneFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/x/y/releases", func(w http.ResponseWriter, _ *http.Request) {
		// Newest-first, as GitHub returns them. Only host-v* should win.
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"tag_name": "electron-v2026.724.305-alpha", "draft": false, "prerelease": true},
			{"tag_name": "mobile-v2026.724.301-alpha", "draft": false, "prerelease": false},
			{"tag_name": "host-v2026.724.300-alpha", "draft": false, "prerelease": true},
			{"tag_name": "hub-v2026.724.300-alpha", "draft": false, "prerelease": true},
			{"tag_name": "host-v2026.723.100-alpha", "draft": false, "prerelease": true},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	gh := &ghClient{repo: "x/y", apiBase: srv.URL, http: srv.Client()}

	rel, err := gh.resolveRelease(context.Background(), "", "alpha", lanePrefix("host-runner"))
	if err != nil {
		t.Fatalf("resolveRelease: %v", err)
	}
	if rel.TagName != "host-v2026.724.300-alpha" {
		t.Errorf("resolved %q, want host-v2026.724.300-alpha (newest host-v lane)", rel.TagName)
	}
}

// --- helpers ---

type releaseFixture struct {
	tag      string
	tarAsset string
	tarball  []byte
	sums     string
	omitTar  bool
}

func newReleaseServer(t *testing.T, f releaseFixture) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/dl/tarball", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(f.tarball)
	})
	mux.HandleFunc("/dl/sums", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, f.sums)
	})
	mux.HandleFunc("/repos/x/y/releases/tags/"+f.tag, func(w http.ResponseWriter, _ *http.Request) {
		assets := []map[string]any{
			{"name": "SHA256SUMS", "browser_download_url": srv.URL + "/dl/sums"},
		}
		if !f.omitTar {
			assets = append(assets, map[string]any{
				"name": f.tarAsset, "browser_download_url": srv.URL + "/dl/tarball",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": f.tag, "draft": false, "prerelease": true, "assets": assets,
		})
	})
	srv = httptest.NewServer(mux)
	return srv
}

func makeTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
