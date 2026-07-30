package hostrunner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/termipod/hub/internal/hostjobs"
)

// lerobot_export.go — the `dataset_export_rrd` host job (ADR-058 §2, replay
// plan W4b).
//
// The plan's decision §11.3 is INTEGRATE, not build: LeRobot ships its own
// rerun path, and a bespoke `.rrd` writer would have to track both rerun's
// format and LeRobot's internals in lock-step. So this file spawns LeRobot's
// own exporter and does three things around it that a subprocess cannot do for
// itself — refuse before starting when the environment is not there, confine
// where it may write, and describe the artifact it produced.
//
// Facts verified against LeRobot main (v0.6.1) rather than assumed, because
// getting any of them wrong means the feature never works:
//
//   - The console entry point is `lerobot-dataset-viz`
//     (`pyproject.toml [project.scripts]` → `lerobot.scripts.lerobot_dataset_viz:main`).
//     The module was called `lerobot.scripts.visualize_dataset` before the
//     package moved under `src/`, which is why the invocation is *probed* from
//     a candidate list instead of pinned to one name.
//   - `--save 1` (it is an int flag, default 0) writes
//     `<output-dir>/{repo_id with / → _}_episode_{n}.rrd` via `rr.save()` and
//     asserts that `--output-dir` was given. No viewer is spawned.
//   - `--root` is the dataset directory **itself** — `lerobot_dataset_viz.py`
//     passes it straight to `LeRobotDataset(repo_id, root=root)`, which uses it
//     as-is and only falls back to `$HF_LEROBOT_HOME/{repo_id}` when it is
//     None. (LeRobot's own docs example reads as though `--root` were the
//     parent that repo_id is appended to. It is not; the source is
//     authoritative and this distinction is the difference between exporting
//     the requested dataset and exporting nothing.)
//   - LeRobot pins `rerun-sdk>=0.24.0,<0.34.0`, so the pair really is a pair:
//     the viewer W4a launches must match the SDK that wrote the file.

const (
	// ToolLeRobotExport is the capabilities key for the pinned
	// (lerobot, rerun-sdk) pair this job needs.
	ToolLeRobotExport = "lerobot-export"

	// lerobotVizBin is the console script pip installs. Preferred when present:
	// its shebang already names the interpreter of the environment LeRobot was
	// installed into, so we never have to guess which python that is — which
	// matters because the plan calls for a pinned venv/uvx.
	lerobotVizBin = "lerobot-dataset-viz"
)

// lerobotVizModules are the module paths that have carried the exporter, newest
// first. Probed as a list so a host on either side of LeRobot's rename works.
var lerobotVizModules = []struct{ module, relPath string }{
	{"lerobot.scripts.lerobot_dataset_viz", "scripts/lerobot_dataset_viz.py"},
	{"lerobot.scripts.visualize_dataset", "scripts/visualize_dataset.py"},
}

// lerobotPinnedDists are the two distributions whose versions make up the pin.
// Both must resolve or the tool is reported unavailable.
var lerobotPinnedDists = []string{"lerobot", "rerun-sdk"}

// ToolCap is a probe result for non-agent host tooling — the shape AgentCap has
// for engine families. Separate because a tool is not spawnable as an agent and
// carries an invocation rather than a driving mode.
type ToolCap struct {
	Installed bool `json:"installed"`
	// Invoke is the argv prefix that runs the tool. Published so an operator
	// can see which of several candidate invocations was chosen.
	Invoke []string `json:"invoke,omitempty"`
	// Versions is the recorded version pin, keyed by distribution name.
	Versions map[string]string `json:"versions,omitempty"`
	// Detail says why Installed is false, in words an operator can act on.
	Detail string `json:"detail,omitempty"`
}

// pyProbeScript reports the pinned distribution versions and which exporter
// module exists, without importing LeRobot.
//
// Not using `importlib.util.find_spec("lerobot.scripts.<x>")`: resolving a
// submodule imports its parent packages, and importing `lerobot` pulls in
// torch — seconds to tens of seconds on a cold cache, inside a probe that runs
// on a timer. Locating the `lerobot` package (which does not import it) and
// stat-ing the file is the same answer, immediately.
//
// The candidate list is interpolated from lerobotVizModules rather than written
// out here, so there is one place a new module name has to be added.
func pyProbeScript() string {
	var candidates strings.Builder
	for _, m := range lerobotVizModules {
		fmt.Fprintf(&candidates, "        (%q, %q),\n", m.module, m.relPath)
	}
	return `
import json, os, sys
try:
    from importlib.metadata import version
except Exception:
    from importlib_metadata import version
import importlib.util as u

out = {"versions": {}, "module": ""}
for dist in sys.argv[1:]:
    try:
        out["versions"][dist] = version(dist)
    except Exception:
        pass
try:
    spec = u.find_spec("lerobot")
    base = os.path.dirname(spec.origin) if spec and spec.origin else ""
except Exception:
    base = ""
if base:
    for mod, rel in (
` + candidates.String() + `    ):
        if os.path.exists(os.path.join(base, rel)):
            out["module"] = mod
            break
print(json.dumps(out))
`
}

// probeLeRobotExport resolves whether and how this host can export a LeRobot
// episode to a `.rrd`.
//
// override, when non-empty, is the operator's own invocation (a pinned venv or
// `uvx …`); it is trusted as the way to run the exporter, but the version pin is
// still read so what actually ran is on the record.
//
// Reporting Installed=false when the versions cannot be read is deliberate even
// though the exporter might still work: the failure this probe exists to
// prevent is a caller waiting out a long job that ends in a Python traceback,
// and a precise Detail plus an override flag is a better answer than a
// confident maybe.
func probeLeRobotExport(ctx context.Context, override []string) ToolCap {
	invoke, py, viaModule, detail := resolveLeRobotInvoke(override)
	if len(invoke) == 0 {
		return ToolCap{Detail: detail}
	}
	cap := ToolCap{Invoke: invoke}
	if py == "" {
		cap.Detail = "found " + strings.Join(invoke, " ") +
			" but no python interpreter to read the (lerobot, rerun-sdk) version pin from"
		return cap
	}
	versions, module, err := readPyProbe(ctx, py)
	cap.Versions = versions
	if err != nil {
		cap.Detail = "version probe via " + py + " failed: " + err.Error()
		return cap
	}
	if viaModule && module != "" {
		// resolveLeRobotInvoke could only guess the newest candidate; the probe
		// is what actually knows. Without this, a host carrying only the legacy
		// `visualize_dataset` module would be reported available and then fail
		// with "No module named …" the moment a caller submitted an export.
		cap.Invoke = []string{py, "-m", module}
	}
	var missing []string
	for _, d := range lerobotPinnedDists {
		if versions[d] == "" {
			missing = append(missing, d)
		}
	}
	if len(missing) > 0 {
		cap.Detail = "the exporter is present but " + strings.Join(missing, " and ") +
			" could not be resolved in " + py + "; install the pinned pair into that environment"
		return cap
	}
	// A resolved module confirms the two halves live in the same environment.
	// An override may legitimately point somewhere this python cannot see, so it
	// is not required there.
	if module == "" && len(override) == 0 {
		cap.Detail = "lerobot is installed in " + py + " but no known exporter module was found in it"
		return cap
	}
	cap.Installed = true
	return cap
}

// resolveLeRobotInvoke picks the argv prefix and the interpreter to read
// versions from. viaModule reports that the prefix is a `python -m <module>`
// guess the caller must correct once the probe names the real module. Returns an
// empty invoke plus a reason when nothing usable is present.
func resolveLeRobotInvoke(override []string) (invoke []string, python string, viaModule bool, detail string) {
	if len(override) > 0 {
		return override, pythonBeside(override[0]), false, ""
	}
	if p, err := exec.LookPath(lerobotVizBin); err == nil && p != "" {
		return []string{p}, pythonBeside(p), false, ""
	}
	py := lookPython()
	if py == "" {
		return nil, "", false, "neither " + lerobotVizBin +
			" nor a python3 interpreter is on the host-runner's PATH"
	}
	return []string{py, "-m", lerobotVizModules[0].module}, py, true, ""
}

// pythonBeside returns the interpreter installed alongside a console script —
// a venv's `bin/python3` next to its `bin/lerobot-dataset-viz`. That is the
// environment whose versions matter. Falls back to PATH.
func pythonBeside(bin string) string {
	if dir := filepath.Dir(bin); dir != "" && dir != "." {
		for _, name := range []string{"python3", "python"} {
			cand := filepath.Join(dir, name)
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				return cand
			}
		}
	}
	return lookPython()
}

func lookPython() string {
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil && p != "" {
			return p
		}
	}
	return ""
}

// readPyProbe runs pyProbeScript and parses its one line of JSON.
func readPyProbe(ctx context.Context, python string) (map[string]string, string, error) {
	args := append([]string{"-c", pyProbeScript()}, lerobotPinnedDists...)
	cmd := exec.CommandContext(ctx, python, args...)
	// A probe must not inherit a caller's HF cache redirection or pick up a
	// half-configured environment; it only needs to import the stdlib.
	cmd.Env = append(os.Environ(), "PYTHONWARNINGS=ignore")
	out, err := cmd.Output()
	if err != nil {
		return nil, "", err
	}
	var parsed struct {
		Versions map[string]string `json:"versions"`
		Module   string            `json:"module"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, "", fmt.Errorf("unparseable probe output: %w", err)
	}
	return parsed.Versions, parsed.Module, nil
}

// ---------------------------------------------------------------------------
// the job
// ---------------------------------------------------------------------------

// datasetExportRRDArgs is the typed arg schema for the kind. root_path matches
// the dataset verbs' spelling (host_dataset_verbs.go) so one name means one
// thing across the surface.
type datasetExportRRDArgs struct {
	RootPath string `json:"root_path"`
	// RepoID is LeRobot's dataset identity, `owner/name`. Optional: when empty
	// it is derived from the root's last two path segments, which is the layout
	// `$HF_LEROBOT_HOME/{owner}/{name}` produces. The value actually used is
	// reported back in the result so a caller never has to guess what ran.
	RepoID       string `json:"repo_id,omitempty"`
	EpisodeIndex int64  `json:"episode_index"`
}

// datasetExportRRDResult is what lands in result_json. ADR-058 §2 fixes the
// shape: {path, bytes, sha256}.
type datasetExportRRDResult struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	// RepoID and Versions record what produced the file. The viewer W4a
	// launches has to match the SDK that wrote it, so the pin travels with the
	// artifact rather than being re-derived later.
	RepoID   string            `json:"repo_id"`
	Versions map[string]string `json:"versions,omitempty"`
}

// exportStderrTail bounds how much of the exporter's stderr rides an error
// message. LeRobot logs through `logging` and rerun's `re_log` writes to
// stderr, so the tail is where a traceback's cause actually is.
const exportStderrTail = 4 << 10

// runDatasetExportRRD is the jobHandler for hostjobs.KindDatasetExportRRD.
func runDatasetExportRRD(ctx context.Context, a *Runner, cmd HostCommand, run *JobRun) (map[string]any, error) {
	var args datasetExportRRDArgs
	if err := json.Unmarshal(cmd.Args, &args); err != nil {
		return nil, fmt.Errorf("dataset_export_rrd: invalid args: %w", err)
	}
	root, err := cleanDatasetRoot(args.RootPath)
	if err != nil {
		return nil, fmt.Errorf("dataset_export_rrd: %w", err)
	}
	if args.EpisodeIndex < 0 {
		return nil, fmt.Errorf("dataset_export_rrd: episode_index must not be negative")
	}
	repoID := args.RepoID
	if repoID == "" {
		repoID = deriveRepoID(root)
	}
	if err := validateRepoID(repoID); err != nil {
		return nil, fmt.Errorf("dataset_export_rrd: %w", err)
	}

	// Fail before doing anything expensive, and fail with the reason. This is
	// the #394 soft-degrade shape: a missing environment is an actionable
	// answer, never a half-run.
	run.Report("probing", 0, 0)
	tool := probeLeRobotExport(ctx, a.LeRobotVizCmd)
	if !tool.Installed {
		detail := tool.Detail
		if detail == "" {
			detail = "the pinned (lerobot, rerun-sdk) pair is not available on this host"
		}
		return nil, fmt.Errorf("dataset_export_rrd: %s", detail)
	}

	// A dataset root that is not one gives a far better error here than the
	// same discovery made by Python thirty seconds in.
	if _, err := os.Stat(filepath.Join(root, "meta", "info.json")); err != nil {
		return nil, fmt.Errorf("dataset_export_rrd: %s does not look like a LeRobot dataset root (no meta/info.json)", root)
	}

	argv := buildLeRobotExportArgv(tool.Invoke, root, repoID, args.EpisodeIndex, run.Dir)

	run.Report("exporting", 0, 0)
	stderr := &tailBuffer{limit: exportStderrTail}
	proc := exec.CommandContext(ctx, argv[0], argv[1:]...)
	proc.Dir = run.Dir
	proc.Env = exportEnv(run.Dir)
	proc.Stdout = io.Discard
	proc.Stderr = stderr
	if err := proc.Run(); err != nil {
		if ctx.Err() != nil {
			// Cancelled or past the ceiling; the executor classifies which.
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("dataset_export_rrd: exporter failed: %w\n%s", err, stderr.String())
	}

	run.Report("hashing", 0, 0)
	path, err := findExportedRRD(run.Dir, repoID, args.EpisodeIndex)
	if err != nil {
		return nil, fmt.Errorf("dataset_export_rrd: %w\n%s", err, stderr.String())
	}
	size, sum, err := hashFile(path)
	if err != nil {
		return nil, fmt.Errorf("dataset_export_rrd: %w", err)
	}
	a.jobcache.touchJobDir(run.Dir)

	out := datasetExportRRDResult{
		Path: path, Bytes: size, SHA256: sum,
		RepoID: repoID, Versions: tool.Versions,
	}
	var m map[string]any
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// buildLeRobotExportArgv assembles the exporter command. Pure, so the argv is
// asserted in tests rather than discovered on a host that has LeRobot.
//
// Only long-standing flags are passed. `--display-mode` (rerun|foxglove) is
// deliberately omitted even though rerun is what we want: it does not exist on
// older LeRobot, argparse rejects unknown flags outright, and rerun is already
// the default — so naming it would trade a real compatibility break for
// protection against a hypothetical default flip.
func buildLeRobotExportArgv(invoke []string, root, repoID string, episode int64, outDir string) []string {
	argv := append([]string(nil), invoke...)
	return append(argv,
		"--repo-id", repoID,
		"--root", root,
		"--episode-index", strconv.FormatInt(episode, 10),
		"--mode", "local",
		"--save", "1",
		"--output-dir", outDir,
	)
}

// exportEnv is the subprocess environment.
//
// HF_HUB_OFFLINE=1 is a confinement, not an optimisation. `LeRobotDataset`
// falls back to `snapshot_download(repo_id)` when it cannot satisfy a read
// locally, so a derived-and-wrong repo_id could otherwise pull a *different*
// dataset off the Hub — silently, inside a job with a 30-minute ceiling, onto
// the host's disk. For a local dataset export, reaching the network is never
// the right answer, so make it impossible and let the local failure surface.
// MPLBACKEND=Agg is the other entry: matplotlib picks a backend at import time,
// and a daemon has no display. Modern matplotlib already falls back to Agg, so
// this is belt-and-braces rather than a fix for an observed failure.
//
// Deliberately NOT redirecting XDG_CACHE_HOME into the job directory, which an
// earlier draft did to keep stray caches inside what the jobcache can evict:
// HuggingFace derives HF_HOME (and therefore HF_LEROBOT_HOME) from
// XDG_CACHE_HOME, so that would silently relocate the host's whole HF cache for
// the duration of an export. Confining a cache we have not observed anyone
// writing is not worth moving one we know exists.
func exportEnv(jobDir string) []string {
	return append(os.Environ(),
		"HF_HUB_OFFLINE=1",
		"HF_DATASETS_OFFLINE=1",
		"MPLBACKEND=Agg",
	)
}

// findExportedRRD locates the produced file.
//
// The expected name is LeRobot's documented pattern, but a rename there must not
// present as "the export produced nothing", so a single `.rrd` under the job
// directory is accepted as the answer regardless of its name. Two would be
// ambiguous and is treated as a failure rather than a guess.
//
// The result is also re-checked against the job directory after symlink
// resolution: this path is handed to the hub and then opened by the desktop, so
// it must be one this job was allowed to write.
func findExportedRRD(dir, repoID string, episode int64) (string, error) {
	expected := filepath.Join(dir, fmt.Sprintf("%s_episode_%d.rrd",
		strings.ReplaceAll(repoID, "/", "_"), episode))
	candidates := []string{}
	if _, err := os.Stat(expected); err == nil {
		candidates = append(candidates, expected)
	} else {
		found, err := filepath.Glob(filepath.Join(dir, "*.rrd"))
		if err != nil {
			return "", fmt.Errorf("scanning for the exported .rrd: %w", err)
		}
		sort.Strings(found)
		candidates = found
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("the exporter reported success but wrote no .rrd into %s", dir)
	case 1:
	default:
		return "", fmt.Errorf("%d .rrd files in %s; cannot tell which one is this export", len(candidates), dir)
	}
	return confinedTo(dir, candidates[0])
}

// confinedTo resolves symlinks and asserts the path really is inside dir.
func confinedTo(dir, path string) (string, error) {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolving the job directory: %w", err)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolving the exported file: %w", err)
	}
	rel, err := filepath.Rel(realDir, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the exported file resolves outside the job directory")
	}
	return real, nil
}

func hashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", fmt.Errorf("open the exported file: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", fmt.Errorf("read the exported file: %w", err)
	}
	return n, fmt.Sprintf("%x", h.Sum(nil)), nil
}

// deriveRepoID reads `owner/name` off a root's last two segments — the shape
// `$HF_LEROBOT_HOME/{owner}/{name}` produces. One segment would be ambiguous
// (`alice/pusht` and `bob/pusht` share a last segment), so a root too shallow to
// carry two yields an empty string and the caller's validation rejects it with a
// message naming what to pass.
func deriveRepoID(root string) string {
	parent, name := filepath.Split(filepath.Clean(root))
	owner := filepath.Base(filepath.Clean(parent))
	if name == "" || owner == "" || owner == "." || owner == string(filepath.Separator) {
		return ""
	}
	return owner + "/" + name
}

// validateRepoID keeps the value inside what a HuggingFace repo id can be. It
// becomes both a process argument and part of a filename, so an unconstrained
// string is two problems.
func validateRepoID(id string) error {
	if id == "" {
		return fmt.Errorf("repo_id is required and could not be derived from root_path; pass owner/name")
	}
	owner, name, ok := strings.Cut(id, "/")
	if !ok || strings.Contains(name, "/") {
		return fmt.Errorf("repo_id %q must be exactly owner/name", id)
	}
	for _, seg := range []string{owner, name} {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("repo_id %q has an empty or traversing segment", id)
		}
		for _, r := range seg {
			ok := r == '-' || r == '_' || r == '.' ||
				(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
			if !ok {
				return fmt.Errorf("repo_id %q contains %q", id, r)
			}
		}
	}
	return nil
}

// tailBuffer keeps only the last `limit` bytes written to it. An exporter that
// decodes every frame of an episode can log a great deal; what a caller needs
// is the end, and the command row's error column is not a log sink.
type tailBuffer struct {
	limit int
	buf   []byte
	cut   bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
		t.cut = true
	}
	return n, nil
}

func (t *tailBuffer) String() string {
	s := string(t.buf)
	if t.cut {
		return "…(earlier output dropped)\n" + s
	}
	return s
}

// registered so the executor can dispatch the kind (ADR-058 §1: a job type
// exists only by being in this registry AND the hostjobs allowlist).
func init() {
	registerJobHandler(hostjobs.KindDatasetExportRRD, runDatasetExportRRD)
}
