// Package selfupdate fetches a tagged release of hub-server or
// host-runner from GitHub, verifies it against the release's
// SHA256SUMS, and atomically replaces the running binary on disk.
//
// It performs no process control: Run returns once the new bytes are
// in place, and the caller exits 75 (EX_TEMPFAIL) so the systemd
// supervisor respawns with the new binary (ADR-028 D-2). On any
// failure the binary on disk is left untouched and the caller exits 1
// — a generic failure that still triggers a same-binary respawn, so
// the host never goes dark.
//
// The release layout this package expects is the per-binary split
// from ADR-028 W5.5, refined when the release lanes split per component:
// each lane's release (Hub → `hub-v*`, Host → `host-v*`) ships four
// tarballs (`<binary>-<version>-<os>-<arch>.tar.gz`, one per platform),
// each expanding to a single bare binary, plus one SHA256SUMS. resolveRelease
// filters the channel listing to the binary's lane prefix so a host never
// resolves a foreign lane's release. Releases predating the split fail with a
// clear message.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/termipod/hub/internal/buildinfo"
)

// DefaultRepo is the GitHub "owner/name" self-update fetches from
// unless an --upstream-repo override is supplied (ADR-028 D-4).
const DefaultRepo = "physercoe/termipod"

// Options configures a self-update run. Binary is the only required
// field.
type Options struct {
	Binary      string // "hub-server" | "host-runner"
	Repo        string // GitHub "owner/name"; empty = DefaultRepo
	Channel     string // "stable" | "alpha"; ignored when Version is set
	Version     string // explicit release tag, with or without leading "v"
	InstallPath string // file to replace; empty = this binary's resolved path
	DryRun      bool   // resolve + report only; touch nothing on disk
	Log         *slog.Logger
}

// Result reports what a (successful or dry-run) Run resolved.
type Result struct {
	Binary      string
	FromVersion string // the running binary's version (buildinfo.Version)
	ToVersion   string // the resolved release tag, e.g. "v1.0.634-alpha"
	InstallPath string
	Asset       string // the per-binary tarball name
}

// Run resolves the target release, downloads and SHA256-verifies the
// per-binary tarball, and atomically replaces the binary on disk.
func Run(ctx context.Context, opt Options) (*Result, error) {
	return run(ctx, opt, newGHClient(opt.Repo))
}

// run is Run with the GitHub client injected, so tests can point it at
// an httptest server.
func run(ctx context.Context, opt Options, gh *ghClient) (*Result, error) {
	if opt.Binary != "hub-server" && opt.Binary != "host-runner" {
		return nil, fmt.Errorf("selfupdate: unknown binary %q (want hub-server|host-runner)", opt.Binary)
	}
	if opt.Log == nil {
		opt.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	installPath, err := resolveInstallPath(opt.InstallPath)
	if err != nil {
		return nil, err
	}

	rel, err := gh.resolveRelease(ctx, opt.Version, opt.Channel, lanePrefix(opt.Binary))
	if err != nil {
		return nil, err
	}

	assetName := artifactName(opt.Binary, rel.TagName)
	res := &Result{
		Binary:      opt.Binary,
		FromVersion: buildinfo.Version,
		ToVersion:   rel.TagName,
		InstallPath: installPath,
		Asset:       assetName,
	}

	tarAsset, ok := rel.asset(assetName)
	if !ok {
		return nil, fmt.Errorf("release %s has no asset %q — it predates the "+
			"per-binary release split (ADR-028 W5.5); upgrade this host manually "+
			"or target a newer release", rel.TagName, assetName)
	}
	sumsAsset, ok := rel.asset("SHA256SUMS")
	if !ok {
		return nil, fmt.Errorf("release %s has no SHA256SUMS asset", rel.TagName)
	}

	if opt.DryRun {
		opt.Log.Info("self-update dry run",
			"binary", opt.Binary, "from", res.FromVersion, "to", res.ToVersion,
			"asset", assetName, "install_path", installPath)
		return res, nil
	}

	// Stage downloads in the install directory so the final rename is
	// same-filesystem and therefore atomic.
	stageDir := filepath.Dir(installPath)

	// Fetch the checksum first: a missing or garbled SHA256SUMS should
	// fail before we spend bandwidth on the tarball.
	wantSum, err := fetchExpectedSum(ctx, gh, sumsAsset.DownloadURL, assetName)
	if err != nil {
		return nil, err
	}

	tarPath := filepath.Join(stageDir, "."+opt.Binary+".selfupdate.tar.gz")
	gotSum, err := downloadToFile(ctx, gh, tarAsset.DownloadURL, tarPath)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tarPath)

	if gotSum != wantSum {
		return nil, fmt.Errorf("sha256 mismatch for %s: got %s, want %s — "+
			"binary NOT replaced", assetName, gotSum, wantSum)
	}
	opt.Log.Info("self-update checksum verified", "asset", assetName, "sha256", gotSum)

	// Extract the single binary next to the install path, then rename
	// over it. Verify-before-extract means the tar bytes are trusted.
	stagedBin := filepath.Join(stageDir, "."+opt.Binary+".selfupdate.bin")
	if err := extractBinary(tarPath, opt.Binary, stagedBin); err != nil {
		return nil, err
	}
	defer os.Remove(stagedBin) // no-op once the rename below succeeds

	if err := os.Rename(stagedBin, installPath); err != nil {
		return nil, fmt.Errorf("install %s: %w (staged binary left at %s)",
			installPath, err, stagedBin)
	}
	opt.Log.Info("self-update installed",
		"binary", opt.Binary, "from", res.FromVersion, "to", res.ToVersion,
		"path", installPath)
	return res, nil
}

// resolveInstallPath returns the explicit path, or the running
// binary's path with symlinks resolved (so we replace the real file,
// not a /usr/local/bin symlink).
func resolveInstallPath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("selfupdate: locate running binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("selfupdate: resolve %s: %w", exe, err)
	}
	return resolved, nil
}

// artifactName builds the per-binary tarball name for this host's
// OS/arch, matching the W5.5 release layout.
// lanePrefix is the release-tag namespace a binary ships under, since the
// release lanes were split per component (Hub / Host / Mobile / Desktop):
// hub-server → `hub-v*`, host-runner → `host-v*`. resolveRelease filters the
// channel listing to this prefix so a host never resolves a mobile/desktop
// release that has no server tarball, and artifactName strips it so the file
// name carries the bare version.
func lanePrefix(binary string) string {
	if binary == "host-runner" {
		return "host-v"
	}
	return "hub-v" // hub-server
}

// artifactName is the per-binary tarball name in a release. The `termipod-`
// prefix was dropped when the lanes split (the release itself is now named
// Hub/Host); the tag's lane prefix is stripped so the file reads
// `hub-server-<version>-<os>-<arch>.tar.gz`. Must stay in lockstep with the
// name the release workflow (release-server.yml) generates + lists in
// SHA256SUMS.
func artifactName(binary, tag string) string {
	ver := strings.TrimPrefix(tag, lanePrefix(binary))
	return fmt.Sprintf("%s-%s-%s-%s.tar.gz",
		binary, ver, runtime.GOOS, runtime.GOARCH)
}

// fetchExpectedSum downloads SHA256SUMS and returns the digest line
// for assetName.
func fetchExpectedSum(ctx context.Context, gh *ghClient, url, assetName string) (string, error) {
	body, err := gh.download(ctx, url)
	if err != nil {
		return "", fmt.Errorf("fetch SHA256SUMS: %w", err)
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read SHA256SUMS: %w", err)
	}
	sum, ok := parseSHA256SUMS(string(data), assetName)
	if !ok {
		return "", fmt.Errorf("SHA256SUMS has no entry for %s", assetName)
	}
	return sum, nil
}

// parseSHA256SUMS finds the hex digest for name in `sha256sum`-format
// output — "<64-hex>  <filename>" per line, filenames as basenames. A
// leading "*" binary-mode marker on the filename is tolerated.
func parseSHA256SUMS(data, name string) (string, bool) {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// downloadToFile streams an asset to dest while hashing it, returning
// the hex SHA256 of the bytes written.
func downloadToFile(ctx context.Context, gh *ghClient, url, dest string) (string, error) {
	body, err := gh.download(ctx, url)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", filepath.Base(dest), err)
	}
	defer body.Close()

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), body); err != nil {
		f.Close()
		os.Remove(dest)
		return "", fmt.Errorf("write %s: %w", dest, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(dest)
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinary pulls the single file named want out of the .tar.gz at
// tarPath and writes it to dest with mode 0755. It matches by basename
// and ignores the archive's path entirely, so there is no traversal
// risk; the tar bytes are already SHA256-trusted by the caller.
func extractBinary(tarPath, want, dest string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gunzip %s: %w", filepath.Base(tarPath), err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("archive %s has no %q entry", filepath.Base(tarPath), want)
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != want {
			continue
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil { //nolint:gosec // SHA256-verified upstream
			out.Close()
			os.Remove(dest)
			return fmt.Errorf("extract %s: %w", want, err)
		}
		if err := out.Close(); err != nil {
			os.Remove(dest)
			return err
		}
		// O_CREATE honours umask; force 0755 so the exec bit survives a
		// restrictive umask on the service account.
		if err := os.Chmod(dest, 0o755); err != nil {
			os.Remove(dest)
			return err
		}
		return nil
	}
}
