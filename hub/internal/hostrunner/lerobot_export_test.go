package hostrunner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termipod/hub/internal/hostjobs"
)

// lerobot_export_test.go
//
// LeRobot and rerun are NOT installed on the development host and no real export
// has ever been run, so what is testable here is everything *around* the
// subprocess: the argv (asserted, not discovered), the refusals that must happen
// before it starts, where its output may live, and how the artifact is
// described. The end-to-end test below drives the real handler through the real
// executor with a stub exporter, which exercises every step except LeRobot's own
// decoding.

// stubBin writes an executable shell script and returns its path.
func stubBin(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// stubPython prints what the probe script would print for the given versions.
func stubPython(t *testing.T, dir string, lerobot, rerun, module string) {
	t.Helper()
	obj := map[string]any{
		"versions": map[string]string{},
		"module":   module,
	}
	if lerobot != "" {
		obj["versions"].(map[string]string)["lerobot"] = lerobot
	}
	if rerun != "" {
		obj["versions"].(map[string]string)["rerun-sdk"] = rerun
	}
	raw, _ := json.Marshal(obj)
	// `echo` is a shell builtin, so this stub still works when a test narrows
	// PATH to just its own bin dir. JSON carries no single quotes.
	stubBin(t, dir, "python3", "echo '"+string(raw)+"'\n")
}

// ---------------------------------------------------------------------------
// the argv
// ---------------------------------------------------------------------------

func TestBuildLeRobotExportArgv(t *testing.T) {
	got := buildLeRobotExportArgv(
		[]string{"/venv/bin/lerobot-dataset-viz"},
		"/data/lerobot/pusht", "lerobot/pusht", 7, "/cache/job-1")

	want := []string{
		"/venv/bin/lerobot-dataset-viz",
		"--repo-id", "lerobot/pusht",
		"--root", "/data/lerobot/pusht",
		"--episode-index", "7",
		"--mode", "local",
		"--save", "1",
		"--output-dir", "/cache/job-1",
	}
	if len(got) != len(want) {
		t.Fatalf("argv = %q\nwant %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q\nfull: %q", i, got[i], want[i], got)
		}
	}
}

// `--save` is an int flag whose default is 0. Passing it bare would leave the
// exporter in viewer mode and write no file at all.
func TestBuildLeRobotExportArgv_SaveCarriesItsValue(t *testing.T) {
	argv := buildLeRobotExportArgv([]string{"x"}, "/r", "o/n", 0, "/d")
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--save 1") {
		t.Fatalf("argv %q must pass `--save 1`; a bare --save leaves save=0", joined)
	}
}

// --display-mode is deliberately absent: argparse rejects unknown flags, the
// option does not exist on older LeRobot, and rerun is already the default. This
// asserts the omission so nobody re-adds it as an "obvious" improvement.
func TestBuildLeRobotExportArgv_OmitsDisplayMode(t *testing.T) {
	for _, a := range buildLeRobotExportArgv([]string{"x"}, "/r", "o/n", 0, "/d") {
		if a == "--display-mode" {
			t.Fatal("--display-mode is passed; it breaks older LeRobot, which rejects unknown flags")
		}
	}
}

// The invoke prefix may be several words (`uvx --from lerobot==… …`) and must
// survive intact, and building an argv must not mutate the caller's slice.
func TestBuildLeRobotExportArgv_MultiWordInvokeIsNotMutated(t *testing.T) {
	invoke := []string{"uvx", "--from", "lerobot==0.6.1", "lerobot-dataset-viz"}
	before := strings.Join(invoke, " ")
	argv := buildLeRobotExportArgv(invoke, "/r", "o/n", 1, "/d")
	if strings.Join(invoke, " ") != before {
		t.Fatalf("invoke was mutated: %q", invoke)
	}
	if strings.Join(argv[:4], " ") != before {
		t.Fatalf("argv lost the invoke prefix: %q", argv)
	}
}

// ---------------------------------------------------------------------------
// the confinement
// ---------------------------------------------------------------------------

// HF_HUB_OFFLINE is a confinement, not a speed-up: LeRobotDataset falls back to
// snapshot_download(repo_id), so a derived-and-wrong repo_id could otherwise
// pull a different dataset off the Hub inside a 30-minute job.
func TestExportEnv_ForbidsHubAccess(t *testing.T) {
	env := exportEnv("/cache/job-1")
	find := func(k string) string {
		var last string
		for _, kv := range env {
			if strings.HasPrefix(kv, k+"=") {
				last = strings.TrimPrefix(kv, k+"=")
			}
		}
		return last
	}
	if find("HF_HUB_OFFLINE") != "1" {
		t.Fatalf("HF_HUB_OFFLINE = %q, want 1: a wrong repo_id must not be able to download a dataset", find("HF_HUB_OFFLINE"))
	}
	// XDG_CACHE_HOME must be left alone: HuggingFace derives HF_HOME (and so
	// HF_LEROBOT_HOME) from it, so setting it would relocate the host's whole HF
	// cache for the duration of an export.
	if got := find("XDG_CACHE_HOME"); got != os.Getenv("XDG_CACHE_HOME") {
		t.Fatalf("XDG_CACHE_HOME was changed to %q; that moves HF_HOME with it", got)
	}
}

func TestFindExportedRRD_PrefersTheDocumentedName(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "lerobot_pusht_episode_3.rrd")
	if err := os.WriteFile(want, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := findExportedRRD(dir, "lerobot/pusht", 3)
	if err != nil {
		t.Fatalf("findExportedRRD: %v", err)
	}
	if filepath.Base(got) != filepath.Base(want) {
		t.Fatalf("found %q, want %q", got, want)
	}
}

// A rename upstream must not present as "the export produced nothing".
func TestFindExportedRRD_AcceptsASingleDifferentlyNamedFile(t *testing.T) {
	dir := t.TempDir()
	odd := filepath.Join(dir, "some-other-name.rrd")
	if err := os.WriteFile(odd, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := findExportedRRD(dir, "lerobot/pusht", 3)
	if err != nil {
		t.Fatalf("a single .rrd under a different name was rejected: %v", err)
	}
	if filepath.Base(got) != "some-other-name.rrd" {
		t.Fatalf("found %q", got)
	}
}

func TestFindExportedRRD_RefusesToGuessBetweenTwo(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.rrd", "b.rrd"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := findExportedRRD(dir, "lerobot/pusht", 3); err == nil {
		t.Fatal("two candidates were silently resolved to one")
	}
}

func TestFindExportedRRD_NoOutputIsAnError(t *testing.T) {
	if _, err := findExportedRRD(t.TempDir(), "lerobot/pusht", 3); err == nil {
		t.Fatal("an empty job dir reported success")
	}
}

// The returned path is handed to the hub and then opened by the desktop, so a
// symlink out of the job directory must not become a readable-anything handle.
func TestFindExportedRRD_RejectsASymlinkOutOfTheJobDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "job")
	outside := filepath.Join(base, "secret.rrd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.rrd")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := findExportedRRD(dir, "lerobot/pusht", 0); err == nil {
		t.Fatal("a symlink pointing outside the job directory was accepted")
	}
}

// ---------------------------------------------------------------------------
// repo id
// ---------------------------------------------------------------------------

func TestDeriveRepoID(t *testing.T) {
	cases := []struct{ root, want string }{
		{"/home/u/.cache/huggingface/lerobot/lerobot/pusht", "lerobot/pusht"},
		{"/data/alice/pusht/", "alice/pusht"},
		{"/data/bob/pusht", "bob/pusht"},
	}
	for _, c := range cases {
		if got := deriveRepoID(c.root); got != c.want {
			t.Fatalf("deriveRepoID(%q) = %q, want %q", c.root, got, c.want)
		}
	}
	// One segment cannot identify a dataset: alice/pusht and bob/pusht share a
	// last segment, so a too-shallow root must decline rather than invent.
	if got := deriveRepoID("/pusht"); got != "" {
		t.Fatalf("deriveRepoID(\"/pusht\") = %q, want empty", got)
	}
}

func TestValidateRepoID_RejectsWhatWouldBecomeAPathOrAFlag(t *testing.T) {
	bad := []string{
		"", "pusht", "a/b/c", "../etc/passwd", "lerobot/../../etc",
		"lerobot/", "/pusht", "lero bot/pusht", "lerobot/pu;sht", "./x",
	}
	for _, id := range bad {
		if err := validateRepoID(id); err == nil {
			t.Fatalf("validateRepoID(%q) accepted it", id)
		}
	}
	for _, id := range []string{"lerobot/pusht", "a-b_c.1/D-2_e.3"} {
		if err := validateRepoID(id); err != nil {
			t.Fatalf("validateRepoID(%q) = %v, want nil", id, err)
		}
	}
}

// ---------------------------------------------------------------------------
// the probe
// ---------------------------------------------------------------------------

func TestProbeLeRobotExport_ReportsThePinnedPair(t *testing.T) {
	dir := t.TempDir()
	viz := stubBin(t, dir, "lerobot-dataset-viz", "exit 0\n")
	stubPython(t, dir, "0.6.1", "0.25.0", "lerobot.scripts.lerobot_dataset_viz")

	cap := probeLeRobotExport(context.Background(), []string{viz})
	if !cap.Installed {
		t.Fatalf("not installed: %s", cap.Detail)
	}
	if cap.Versions["lerobot"] != "0.6.1" || cap.Versions["rerun-sdk"] != "0.25.0" {
		t.Fatalf("versions = %v, want the pinned pair recorded", cap.Versions)
	}
	if len(cap.Invoke) != 1 || cap.Invoke[0] != viz {
		t.Fatalf("invoke = %q, want the override", cap.Invoke)
	}
}

// Half the pair is not the pair. A host with lerobot but no rerun-sdk would fail
// deep inside Python; the probe has to catch it and say which half is missing.
func TestProbeLeRobotExport_MissingHalfOfThePairIsNotInstalled(t *testing.T) {
	dir := t.TempDir()
	viz := stubBin(t, dir, "lerobot-dataset-viz", "exit 0\n")
	stubPython(t, dir, "0.6.1", "", "lerobot.scripts.lerobot_dataset_viz")

	cap := probeLeRobotExport(context.Background(), []string{viz})
	if cap.Installed {
		t.Fatal("reported installed with rerun-sdk missing")
	}
	if !strings.Contains(cap.Detail, "rerun-sdk") {
		t.Fatalf("detail = %q, want it to name the missing half", cap.Detail)
	}
}

// On a host with no console script, the invocation falls back to
// `python -m <module>` — and the module has to be the one that actually exists.
// Guessing the newest name would report the host available and then fail with
// "No module named …" once someone submitted an export.
func TestProbeLeRobotExport_ModuleFallbackUsesTheModuleTheProbeFound(t *testing.T) {
	bin := t.TempDir()
	stubPython(t, bin, "0.5.0", "0.24.0", "lerobot.scripts.visualize_dataset")
	t.Setenv("PATH", bin) // no lerobot-dataset-viz anywhere

	cap := probeLeRobotExport(context.Background(), nil)
	if !cap.Installed {
		t.Fatalf("not installed: %s", cap.Detail)
	}
	joined := strings.Join(cap.Invoke, " ")
	if !strings.HasSuffix(joined, "-m lerobot.scripts.visualize_dataset") {
		t.Fatalf("invoke = %q, want the legacy module the probe actually found", joined)
	}
}

// The probe's module candidates come from one Go list; the python side is
// generated from it so a new name cannot be added to only one of the two.
func TestPyProbeScript_ListsEveryKnownModule(t *testing.T) {
	script := pyProbeScript()
	for _, m := range lerobotVizModules {
		if !strings.Contains(script, m.module) || !strings.Contains(script, m.relPath) {
			t.Fatalf("probe script does not mention %s / %s", m.module, m.relPath)
		}
	}
}

func TestProbeLeRobotExport_UnreadableProbeIsNotInstalled(t *testing.T) {
	dir := t.TempDir()
	viz := stubBin(t, dir, "lerobot-dataset-viz", "exit 0\n")
	stubBin(t, dir, "python3", "echo 'not json'\n")

	cap := probeLeRobotExport(context.Background(), []string{viz})
	if cap.Installed {
		t.Fatal("reported installed off unparseable probe output")
	}
	if cap.Detail == "" {
		t.Fatal("no detail explaining why")
	}
}

// The pinned pair is what a probe sweep can see change while the runner is up,
// so a change to it has to move the capabilities hash or the hub never learns.
func TestCapabilitiesHash_CoversTools(t *testing.T) {
	base := Capabilities{
		Agents: map[string]AgentCap{"claude-code": {Installed: true}},
		Tools:  map[string]ToolCap{ToolLeRobotExport: {Installed: true, Versions: map[string]string{"lerobot": "0.6.1"}}},
	}
	upgraded := Capabilities{
		Agents: map[string]AgentCap{"claude-code": {Installed: true}},
		Tools:  map[string]ToolCap{ToolLeRobotExport: {Installed: true, Versions: map[string]string{"lerobot": "0.7.0"}}},
	}
	if base.Hash() == upgraded.Hash() {
		t.Fatal("a version-pin change does not move the hash; the hub would never be told")
	}
	same := base
	if base.Hash() != same.Hash() {
		t.Fatal("hash is not stable for equal payloads")
	}
}

// ---------------------------------------------------------------------------
// registry discipline
// ---------------------------------------------------------------------------

// The allowlist and the handler table must agree, exactly. A handler for a kind
// missing from the allowlist would run INLINE — the one thing the executor
// exists to prevent — and an allowlisted kind with no handler accepts work it
// cannot do.
func TestJobRegistry_AgreesWithTheAllowlist(t *testing.T) {
	for _, k := range hostjobs.Kinds() {
		if _, ok := jobRegistry[k]; !ok {
			t.Errorf("kind %q is allowlisted as detached but has no handler", k)
		}
	}
	for k := range jobRegistry {
		if !hostjobs.Is(k) {
			t.Errorf("handler registered for %q, which is not in the hostjobs allowlist: it would run inline", k)
		}
	}
}

func TestRegisterJobHandler_PanicsOnAnUnlistedKind(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering a handler for an unlisted kind was allowed")
		}
	}()
	registerJobHandler("definitely_not_a_job_kind", func(context.Context, *Runner, HostCommand, *JobRun) (map[string]any, error) {
		return nil, nil
	})
}

func TestRegisterJobHandler_PanicsOnADuplicate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a duplicate handler registration was allowed")
		}
	}()
	registerJobHandler(hostjobs.KindDatasetExportRRD, runDatasetExportRRD)
}

// ---------------------------------------------------------------------------
// the whole job, with a stub exporter
// ---------------------------------------------------------------------------

// stubDatasetRoot creates the minimum a LeRobot root must show to get past the
// pre-flight check.
func stubDatasetRoot(t *testing.T, owner, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), owner, name)
	if err := os.MkdirAll(filepath.Join(root, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "meta", "info.json"),
		[]byte(`{"codebase_version":"v3.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// The real handler, through the real executor, against a stub that behaves like
// the exporter: reads --output-dir, writes the documented filename, exits 0.
func TestDatasetExportRRD_EndToEndWithAStubExporter(t *testing.T) {
	f := newJobHub(t)
	bin := t.TempDir()
	// Mimic LeRobot: parse --output-dir / --repo-id / --episode-index out of
	// argv and write `{repo_id with / -> _}_episode_{n}.rrd` there.
	viz := stubBin(t, bin, "lerobot-dataset-viz", `
out=""; repo=""; ep=""
while [ $# -gt 0 ]; do
  case "$1" in
    --output-dir) out="$2"; shift 2;;
    --repo-id) repo="$2"; shift 2;;
    --episode-index) ep="$2"; shift 2;;
    *) shift;;
  esac
done
[ -n "$out" ] || { echo "no --output-dir" >&2; exit 2; }
name=$(printf '%s' "$repo" | tr '/' '_')
printf 'RRD-BYTES' > "$out/${name}_episode_${ep}.rrd"
exit 0
`)
	stubPython(t, bin, "0.6.1", "0.25.0", "lerobot.scripts.lerobot_dataset_viz")

	root := stubDatasetRoot(t, "lerobot", "pusht")
	a := newJobRunner(t, f, nil)
	a.LeRobotVizCmd = []string{viz}

	args, _ := json.Marshal(datasetExportRRDArgs{RootPath: root, EpisodeIndex: 4})
	a.jobs.submit(context.Background(), HostCommand{
		ID: "exp-1", HostID: "host1",
		Kind: hostjobs.KindDatasetExportRRD, Args: args, Status: "delivered",
	})
	a.jobs.wait()

	p, ok := f.terminalFor("exp-1")
	if !ok {
		t.Fatal("the export reported nothing")
	}
	if p.Status != "done" {
		t.Fatalf("status = %q, error = %q", p.Status, p.Error)
	}

	var got datasetExportRRDResult
	if err := json.Unmarshal(p.Result, &got); err != nil {
		t.Fatalf("result_json = %s: %v", p.Result, err)
	}
	// ADR-058 §2 fixes the result shape.
	if got.Path == "" || got.Bytes == 0 || got.SHA256 == "" {
		t.Fatalf("result = %+v, want {path, bytes, sha256} all populated", got)
	}
	if got.Bytes != int64(len("RRD-BYTES")) {
		t.Fatalf("bytes = %d, want %d", got.Bytes, len("RRD-BYTES"))
	}
	// The digest must be of the artifact's own bytes — `printf 'RRD-BYTES' |
	// sha256sum`. Checking only its length would pass against a hash of the
	// path, the args, or nothing at all.
	const wantSHA = "f6605aed84aa7592ebd270baacf9eca092d1d13f71e123121fb3fbe7f94a04d2"
	if got.SHA256 != wantSHA {
		t.Fatalf("sha256 = %q, want %q (the digest of the file's contents)", got.SHA256, wantSHA)
	}
	// repo_id was derived from the root's last two segments, and is reported so
	// the caller can see what actually ran.
	if got.RepoID != "lerobot/pusht" {
		t.Fatalf("repo_id = %q, want the derived lerobot/pusht", got.RepoID)
	}
	if got.Versions["lerobot"] != "0.6.1" {
		t.Fatalf("versions = %v, want the pin that produced the file", got.Versions)
	}
	// The artifact is inside the jobcache, under this command's own directory.
	wantDir := filepath.Join(a.jobcache.Root, hostjobs.KindDatasetExportRRD, "exp-1")
	if filepath.Dir(got.Path) != wantDir {
		t.Fatalf("artifact at %q, want it under %q", got.Path, wantDir)
	}
	if _, err := os.Stat(got.Path); err != nil {
		t.Fatalf("reported path does not exist: %v", err)
	}
}

// A missing environment must fail before anything runs, with a reason — never a
// long poll ending in a Python traceback (#394 soft-degrade).
func TestDatasetExportRRD_RefusesWhenThePairIsMissing(t *testing.T) {
	f := newJobHub(t)
	bin := t.TempDir()
	viz := stubBin(t, bin, "lerobot-dataset-viz", "echo 'should not run' >&2; exit 1\n")
	stubPython(t, bin, "", "", "") // neither half resolvable

	root := stubDatasetRoot(t, "lerobot", "pusht")
	a := newJobRunner(t, f, nil)
	a.LeRobotVizCmd = []string{viz}

	args, _ := json.Marshal(datasetExportRRDArgs{RootPath: root, EpisodeIndex: 0})
	a.jobs.submit(context.Background(), HostCommand{
		ID: "exp-2", Kind: hostjobs.KindDatasetExportRRD, Args: args, Status: "delivered",
	})
	a.jobs.wait()

	p, _ := f.terminalFor("exp-2")
	if p.Status != "failed" {
		t.Fatalf("status = %q, want failed", p.Status)
	}
	if !strings.Contains(p.Error, "lerobot") {
		t.Fatalf("error = %q, want it to name what is missing", p.Error)
	}
	if strings.Contains(p.Error, "should not run") {
		t.Fatal("the exporter was started despite a missing environment")
	}
}

// A root that is not a LeRobot dataset gets a clear answer here rather than one
// discovered by Python thirty seconds in.
func TestDatasetExportRRD_RefusesANonDatasetRoot(t *testing.T) {
	f := newJobHub(t)
	bin := t.TempDir()
	viz := stubBin(t, bin, "lerobot-dataset-viz", "exit 0\n")
	stubPython(t, bin, "0.6.1", "0.25.0", "lerobot.scripts.lerobot_dataset_viz")

	a := newJobRunner(t, f, nil)
	a.LeRobotVizCmd = []string{viz}
	empty := filepath.Join(t.TempDir(), "owner", "name")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(datasetExportRRDArgs{RootPath: empty, EpisodeIndex: 0})
	a.jobs.submit(context.Background(), HostCommand{
		ID: "exp-3", Kind: hostjobs.KindDatasetExportRRD, Args: args, Status: "delivered",
	})
	a.jobs.wait()

	p, _ := f.terminalFor("exp-3")
	if p.Status != "failed" || !strings.Contains(p.Error, "meta/info.json") {
		t.Fatalf("patch = %+v, want a failure naming the missing meta/info.json", p)
	}
}

// A non-zero exit must carry the tail of stderr: that is where the cause is.
func TestDatasetExportRRD_FailureCarriesTheStderrTail(t *testing.T) {
	f := newJobHub(t)
	bin := t.TempDir()
	viz := stubBin(t, bin, "lerobot-dataset-viz",
		"echo 'Traceback (most recent call last):' >&2\necho 'ValueError: episode 99 out of range' >&2\nexit 1\n")
	stubPython(t, bin, "0.6.1", "0.25.0", "lerobot.scripts.lerobot_dataset_viz")

	root := stubDatasetRoot(t, "lerobot", "pusht")
	a := newJobRunner(t, f, nil)
	a.LeRobotVizCmd = []string{viz}

	args, _ := json.Marshal(datasetExportRRDArgs{RootPath: root, EpisodeIndex: 99})
	a.jobs.submit(context.Background(), HostCommand{
		ID: "exp-4", Kind: hostjobs.KindDatasetExportRRD, Args: args, Status: "delivered",
	})
	a.jobs.wait()

	p, _ := f.terminalFor("exp-4")
	if p.Status != "failed" {
		t.Fatalf("status = %q, want failed", p.Status)
	}
	if !strings.Contains(p.Error, "out of range") {
		t.Fatalf("error = %q, want the exporter's own stderr in it", p.Error)
	}
}

// An exporter that exits 0 having written nothing must not report success with an
// empty path: the desktop would open a file that is not there.
func TestDatasetExportRRD_SilentNoOutputIsAFailure(t *testing.T) {
	f := newJobHub(t)
	bin := t.TempDir()
	viz := stubBin(t, bin, "lerobot-dataset-viz", "exit 0\n")
	stubPython(t, bin, "0.6.1", "0.25.0", "lerobot.scripts.lerobot_dataset_viz")

	root := stubDatasetRoot(t, "lerobot", "pusht")
	a := newJobRunner(t, f, nil)
	a.LeRobotVizCmd = []string{viz}

	args, _ := json.Marshal(datasetExportRRDArgs{RootPath: root, EpisodeIndex: 0})
	a.jobs.submit(context.Background(), HostCommand{
		ID: "exp-5", Kind: hostjobs.KindDatasetExportRRD, Args: args, Status: "delivered",
	})
	a.jobs.wait()

	p, _ := f.terminalFor("exp-5")
	if p.Status != "failed" || !strings.Contains(p.Error, "wrote no .rrd") {
		t.Fatalf("patch = %+v, want a failure saying nothing was written", p)
	}
}

// validateRepoID is unit-tested above, but a unit test cannot see the *call*
// being removed — this drives a caller-supplied repo_id through the handler, so
// the guard is pinned where it is actually applied. repo_id becomes both a
// process argument and part of a filename inside the jobcache.
func TestDatasetExportRRD_RejectsAnUnsafeCallerSuppliedRepoID(t *testing.T) {
	f := newJobHub(t)
	bin := t.TempDir()
	viz := stubBin(t, bin, "lerobot-dataset-viz", "echo 'should not run' >&2; exit 1\n")
	stubPython(t, bin, "0.6.1", "0.25.0", "lerobot.scripts.lerobot_dataset_viz")

	root := stubDatasetRoot(t, "lerobot", "pusht")
	a := newJobRunner(t, f, nil)
	a.LeRobotVizCmd = []string{viz}

	args, _ := json.Marshal(datasetExportRRDArgs{
		RootPath: root, RepoID: "../../etc/passwd", EpisodeIndex: 0,
	})
	a.jobs.submit(context.Background(), HostCommand{
		ID: "exp-7", Kind: hostjobs.KindDatasetExportRRD, Args: args, Status: "delivered",
	})
	a.jobs.wait()

	p, _ := f.terminalFor("exp-7")
	if p.Status != "failed" {
		t.Fatalf("status = %q, want failed for repo_id %q", p.Status, "../../etc/passwd")
	}
	if !strings.Contains(p.Error, "repo_id") {
		t.Fatalf("error = %q, want it to name repo_id", p.Error)
	}
	if strings.Contains(p.Error, "should not run") {
		t.Fatal("the exporter was started with an unsafe repo_id")
	}
}

func TestDatasetExportRRD_RejectsARelativeRoot(t *testing.T) {
	f := newJobHub(t)
	a := newJobRunner(t, f, nil)
	args, _ := json.Marshal(datasetExportRRDArgs{RootPath: "relative/path", EpisodeIndex: 0})
	a.jobs.submit(context.Background(), HostCommand{
		ID: "exp-6", Kind: hostjobs.KindDatasetExportRRD, Args: args, Status: "delivered",
	})
	a.jobs.wait()
	p, _ := f.terminalFor("exp-6")
	if p.Status != "failed" || !strings.Contains(p.Error, "absolute") {
		t.Fatalf("patch = %+v, want a failure about an absolute root_path", p)
	}
}

func TestTailBuffer_KeepsTheEndAndSaysItDropped(t *testing.T) {
	b := &tailBuffer{limit: 10}
	if _, err := b.Write([]byte("0123456789ABCDE")); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.HasSuffix(got, "56789ABCDE") {
		t.Fatalf("tail = %q, want the last 10 bytes", got)
	}
	if !strings.Contains(got, "dropped") {
		t.Fatalf("tail = %q, want it to admit truncation", got)
	}
	small := &tailBuffer{limit: 100}
	_, _ = small.Write([]byte("short"))
	if small.String() != "short" {
		t.Fatalf("untruncated output was annotated: %q", small.String())
	}
}
