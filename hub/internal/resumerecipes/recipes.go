// Package resumerecipes holds the native-resume recipe table: how each
// engine CLI reattaches to a prior session, and which mechanism each of our
// agent families actually uses.
//
// The table is DATA (recipes.yaml), not Go literals, because the hub is not
// its only consumer — the desktop Companion's local agent service reads the
// same file from TypeScript (vision-parity L3/L4) and is pinned to the same
// fixture. See recipes.yaml's header for provenance and the per-row
// verification grades, and docs/reference/engine-resume-recipes.md for the
// rendered table.
//
// Plan: docs/plans/pane-state-manifests.md N1.
package resumerecipes

import (
	_ "embed"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unicode"

	"gopkg.in/yaml.v3"
)

//go:embed recipes.yaml
var recipesYAML []byte

// Validation envelope. Ported from herdr's agent_resume.rs (MAX_SESSION_ID_LEN
// / MAX_SESSION_PATH_LEN) — a session reference is data that arrives from the
// engine, so it gets bounded and screened before it is ever spliced into a
// command.
const (
	MaxSessionIDLen   = 512
	MaxSessionPathLen = 4096
)

// Argv styles. flag_pair and subcommand produce the same three-token argv;
// they stay distinct because one is a flag and the other is a verb, and a
// surface rendering the command for a human should be able to say which.
const (
	StyleFlagPair   = "flag_pair"
	StyleFlagEquals = "flag_equals"
	StyleSubcommand = "subcommand"
)

// Session-reference kinds. Only pi and omp accept a path.
const (
	RefID   = "id"
	RefPath = "path"
)

// Resume mechanisms a family can use. Only MechanismArgv implies a recipe.
const (
	MechanismArgv         = "argv"
	MechanismACPLoad      = "acp_session_load"
	MechanismAppServer    = "appserver_thread_resume"
	mechanismUnrecognized = ""
)

// Verification grades, weakest last. Recorded per row: a vendored recipe that
// is wrong fails silently (a resume that cold-starts), so "we have not run
// this binary" must survive into the data rather than being averaged away.
const (
	VerifiedProbe    = "probe"
	VerifiedInTree   = "in-tree"
	VerifiedVendored = "vendored"
)

// Engine is one engine CLI's resume recipe. `Engine` is the recipe key — it is
// neither our agent-family name nor `agents.kind`.
type Engine struct {
	Engine     string   `yaml:"engine"`
	Bin        string   `yaml:"bin"`
	WindowsBin string   `yaml:"windows_bin"`
	Style      string   `yaml:"style"`
	Token      string   `yaml:"token"`
	RefKinds   []string `yaml:"ref_kinds"`
	Source     string   `yaml:"source"`
	Verified   string   `yaml:"verified"`
	Note       string   `yaml:"note"`
}

// Family maps one of our registered agent families onto a resume mechanism.
// An empty Engine with MechanismArgv is a validation error; an empty Engine
// with any other mechanism means "this family does not resume by argv".
type Family struct {
	Family    string `yaml:"family"`
	Engine    string `yaml:"engine"`
	Mechanism string `yaml:"mechanism"`
	Note      string `yaml:"note"`
}

// Table is the parsed, validated recipe table.
type Table struct {
	Version  int      `yaml:"version"`
	Engines  []Engine `yaml:"engines"`
	Families []Family `yaml:"families"`

	byEngine map[string]Engine
	byFamily map[string]Family
}

var (
	loadOnce sync.Once
	loaded   *Table
	loadErr  error
)

// Load parses and validates the embedded table once per process.
func Load() (*Table, error) {
	loadOnce.Do(func() {
		loaded, loadErr = parse(recipesYAML)
	})
	return loaded, loadErr
}

// MustLoad is Load for callers with no error path. The table is embedded and
// validated by a test in this package, so a failure here is a build-time
// defect, not a runtime condition.
func MustLoad() *Table {
	t, err := Load()
	if err != nil {
		panic("resumerecipes: embedded table is invalid: " + err.Error())
	}
	return t
}

func parse(data []byte) (*Table, error) {
	var t Table
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("resumerecipes: parse: %w", err)
	}
	if t.Version != 1 {
		return nil, fmt.Errorf("resumerecipes: unsupported version %d (want 1)", t.Version)
	}
	t.byEngine = make(map[string]Engine, len(t.Engines))
	for _, e := range t.Engines {
		if err := validateEngine(e); err != nil {
			return nil, err
		}
		if _, dup := t.byEngine[e.Engine]; dup {
			return nil, fmt.Errorf("resumerecipes: duplicate engine %q", e.Engine)
		}
		t.byEngine[e.Engine] = e
	}
	t.byFamily = make(map[string]Family, len(t.Families))
	for _, f := range t.Families {
		if err := validateFamily(f, t.byEngine); err != nil {
			return nil, err
		}
		if _, dup := t.byFamily[f.Family]; dup {
			return nil, fmt.Errorf("resumerecipes: duplicate family %q", f.Family)
		}
		t.byFamily[f.Family] = f
	}
	return &t, nil
}

func validateEngine(e Engine) error {
	if e.Engine == "" {
		return errors.New("resumerecipes: engine row with empty `engine`")
	}
	if e.Bin == "" {
		return fmt.Errorf("resumerecipes: engine %q has empty `bin`", e.Engine)
	}
	switch e.Style {
	case StyleFlagPair, StyleFlagEquals, StyleSubcommand:
	default:
		return fmt.Errorf("resumerecipes: engine %q has unknown style %q", e.Engine, e.Style)
	}
	if e.Token == "" {
		return fmt.Errorf("resumerecipes: engine %q has empty `token`", e.Engine)
	}
	if len(e.RefKinds) == 0 {
		return fmt.Errorf("resumerecipes: engine %q declares no ref_kinds", e.Engine)
	}
	for _, k := range e.RefKinds {
		if k != RefID && k != RefPath {
			return fmt.Errorf("resumerecipes: engine %q has unknown ref kind %q", e.Engine, k)
		}
	}
	switch e.Verified {
	case VerifiedProbe, VerifiedInTree, VerifiedVendored:
	default:
		return fmt.Errorf("resumerecipes: engine %q has unknown verification grade %q", e.Engine, e.Verified)
	}
	if e.Source == "" {
		return fmt.Errorf("resumerecipes: engine %q has empty `source`", e.Engine)
	}
	return nil
}

func validateFamily(f Family, engines map[string]Engine) error {
	if f.Family == "" {
		return errors.New("resumerecipes: family row with empty `family`")
	}
	switch f.Mechanism {
	case MechanismArgv:
		if f.Engine == "" {
			return fmt.Errorf("resumerecipes: family %q claims argv resume but names no engine", f.Family)
		}
	case MechanismACPLoad, MechanismAppServer:
	default:
		return fmt.Errorf("resumerecipes: family %q has unknown mechanism %q", f.Family, f.Mechanism)
	}
	// An engine reference must resolve even when the mechanism is not argv —
	// codex names its CLI fallback rung, and a typo there would hand a future
	// consumer a recipe that does not exist.
	if f.Engine != "" {
		if _, ok := engines[f.Engine]; !ok {
			return fmt.Errorf("resumerecipes: family %q references unknown engine %q", f.Family, f.Engine)
		}
	}
	return nil
}

// EngineByID returns the recipe for an engine id.
func (t *Table) EngineByID(id string) (Engine, bool) {
	e, ok := t.byEngine[id]
	return e, ok
}

// FamilyByName returns the family row for an `agents.kind` value.
func (t *Table) FamilyByName(name string) (Family, bool) {
	f, ok := t.byFamily[name]
	return f, ok
}

// SessionRef is a validated reference to a prior engine session.
type SessionRef struct {
	Kind  string
	Value string
}

// Sentinel errors. Callers distinguish these: an unknown family and a family
// that resumes over a protocol are different situations, and neither is an
// invalid reference.
var (
	ErrUnknownFamily  = errors.New("resumerecipes: family has no recipe row")
	ErrNotArgvResume  = errors.New("resumerecipes: family does not resume by argv")
	ErrUnsupportedRef = errors.New("resumerecipes: engine does not accept this session-reference kind")
)

// NewID validates an opaque session id.
func NewID(value string) (SessionRef, error) {
	if value == "" {
		return SessionRef{}, errors.New("resumerecipes: empty session id")
	}
	if len(value) > MaxSessionIDLen {
		return SessionRef{}, fmt.Errorf("resumerecipes: session id exceeds %d bytes", MaxSessionIDLen)
	}
	if hasControl(value) {
		return SessionRef{}, errors.New("resumerecipes: session id contains a control character")
	}
	return SessionRef{Kind: RefID, Value: value}, nil
}

// NewPath validates an absolute path to a session file.
func NewPath(value string) (SessionRef, error) {
	if value == "" {
		return SessionRef{}, errors.New("resumerecipes: empty session path")
	}
	if len(value) > MaxSessionPathLen {
		return SessionRef{}, fmt.Errorf("resumerecipes: session path exceeds %d bytes", MaxSessionPathLen)
	}
	if hasControl(value) {
		return SessionRef{}, errors.New("resumerecipes: session path contains a control character")
	}
	// Absolute means POSIX-absolute here regardless of host: the value names a
	// path on the agent's host, which is not necessarily this one.
	if !strings.HasPrefix(value, "/") {
		return SessionRef{}, errors.New("resumerecipes: session path is not absolute")
	}
	return SessionRef{Kind: RefPath, Value: value}, nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// Plan is a resolved resume invocation.
type Plan struct {
	Family    string
	Engine    string
	Argv      []string
	DedupeKey string
}

// PlanForFamily resolves the resume argv for one of our agent families.
// goos selects the platform binary ("" means this process's runtime.GOOS).
func (t *Table) PlanForFamily(family string, ref SessionRef, goos string) (Plan, error) {
	f, ok := t.byFamily[family]
	if !ok {
		return Plan{}, fmt.Errorf("%w: %q", ErrUnknownFamily, family)
	}
	if f.Mechanism != MechanismArgv || f.Engine == "" {
		return Plan{}, fmt.Errorf("%w: %q resumes via %s", ErrNotArgvResume, family, f.Mechanism)
	}
	e, ok := t.byEngine[f.Engine]
	if !ok {
		// parse() rejects this, so reaching it means the table was built by
		// hand rather than loaded.
		return Plan{}, fmt.Errorf("resumerecipes: family %q references unknown engine %q", family, f.Engine)
	}
	argv, err := e.Argv(ref, goos)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Family:    family,
		Engine:    e.Engine,
		Argv:      argv,
		DedupeKey: DedupeKey(family, e.Engine, ref),
	}, nil
}

// Argv builds the engine's resume command. The value is a separate argv
// element in every style, so nothing here needs quoting — see ShellCommand
// for the case where the caller must flatten to a shell string.
func (e Engine) Argv(ref SessionRef, goos string) ([]string, error) {
	if !e.AcceptsRef(ref.Kind) {
		return nil, fmt.Errorf("%w: engine %q accepts %v, got %q",
			ErrUnsupportedRef, e.Engine, e.RefKinds, ref.Kind)
	}
	bin := e.BinFor(goos)
	switch e.Style {
	case StyleFlagPair, StyleSubcommand:
		return []string{bin, e.Token, ref.Value}, nil
	case StyleFlagEquals:
		return []string{bin, e.Token + "=" + ref.Value}, nil
	default:
		return nil, fmt.Errorf("resumerecipes: engine %q has unknown style %q", e.Engine, e.Style)
	}
}

// AcceptsRef reports whether the engine takes this session-reference kind.
func (e Engine) AcceptsRef(kind string) bool {
	for _, k := range e.RefKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// BinFor returns the binary name for a platform. Only cursor differs today,
// but a reader that ignores windows_bin builds a command that cannot run.
func (e Engine) BinFor(goos string) string {
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos == "windows" && e.WindowsBin != "" {
		return e.WindowsBin
	}
	return e.Bin
}

// DedupeKey identifies a session reference for restore-time deduplication:
// two panes claiming one session must not both resume it (the second would
// attach a second process to one engine conversation). NUL-separated because
// no component may contain NUL — ids and paths are control-char-screened.
func DedupeKey(family, engine string, ref SessionRef) string {
	return strings.Join([]string{family, engine, ref.Kind, ref.Value}, "\x00")
}

// ShellCommand flattens argv into a string safe to hand to `sh -c`.
//
// This exists because the hub does not exec the resume command — it splices it
// into a spawn spec's `backend.cmd`, which tmux runs through a shell. A session
// id reaches us from the engine's own `session.init` payload, so it is
// attacker-influenced data in a shell context: without quoting, an id of
// `x; rm -rf /` executes.
func (p Plan) ShellCommand() string {
	quoted := make([]string, 0, len(p.Argv))
	for _, a := range p.Argv {
		quoted = append(quoted, ShellQuote(a))
	}
	return strings.Join(quoted, " ")
}

// ShellQuote returns s safe for a POSIX shell word, quoting only when the
// value actually needs it. The conservative allow-list keeps ordinary session
// ids (UUIDs, hyphenated slugs) byte-identical to what the hub spliced before
// this table existed, so adopting it changes no rendered command that was
// already safe.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !needsQuoting(s) {
		return s
	}
	// Single-quote everything, ending and reopening the quote around each
	// embedded single quote.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func needsQuoting(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("_@%+=:,./-", r):
		default:
			return true
		}
	}
	return false
}
