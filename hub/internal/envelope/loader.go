package envelope

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	hub "github.com/termipod/hub"
	"gopkg.in/yaml.v3"
)

// envOverridePath is the environment variable an operator sets to
// point the loader at an arbitrary YAML path. When unset, the loader
// looks at `<HUB_DATA>/team/templates/envelope/active.yaml` — the same
// path the REST `/templates` surface seeds + edits, so mobile edits
// land here automatically. Tests inject paths via this var rather
// than monkey-patching the default.
const envOverridePath = "TERMIPOD_ENVELOPE_TEMPLATE"

// envHubData mirrors hub/cmd/hub-server/main.go's `HUB_DATA` env.
// Kept in sync intentionally — both reside in different packages but
// describe the same on-disk root.
const envHubData = "HUB_DATA"

// embeddedPath is the location of the embedded fallback YAML inside
// `hub.TemplatesFS`. The same file is seeded to the operator override
// path by `writeBuiltinTemplates` (server/init.go); using the same
// bytes here as the loader's last-resort guarantees no drift.
const embeddedPath = "templates/envelope/active.yaml"

// Warner is the audit hook the loader uses to surface operator-
// visible problems (parse error in the override file, validation
// failure). The server package injects a closure that writes an
// audit_events row; tests pass a recorder. A nil Warner is valid —
// diagnostics are then dropped silently.
type Warner func(action, summary string, meta map[string]any)

// Loader caches the parsed Templates. Concurrency-safe (RWMutex).
// One instance per process is the intended deployment; the server
// owns it and reuses across requests. Test code can construct its
// own Loader with overridden paths.
type Loader struct {
	// overridePath, when non-empty, replaces the env+default
	// resolution of the on-disk operator override. Tests set this;
	// production leaves it empty.
	overridePath string

	// hubData replaces the env+default resolution of $HUB_DATA.
	// Tests set this to a temp dir; production leaves it empty.
	hubData string

	// warner is the audit-hook for parse errors. Nil is legal.
	warner Warner

	mu          sync.RWMutex
	cached      *Templates
	cachedPath  string
	cachedMtime time.Time
}

// NewLoader constructs a Loader with the given audit hook. A nil
// Warner is acceptable; problems are then dropped silently.
func NewLoader(warner Warner) *Loader {
	return &Loader{warner: warner}
}

// WithOverridePath returns the loader after overriding the on-disk
// override path resolution. Test seam — production code does NOT
// call this; it lets env+default resolution win.
func (l *Loader) WithOverridePath(path string) *Loader {
	l.overridePath = path
	return l
}

// WithHubData returns the loader after overriding $HUB_DATA
// resolution for tests that want to isolate filesystem state from
// the operator's real ~/hub directory.
func (l *Loader) WithHubData(dir string) *Loader {
	l.hubData = dir
	return l
}

// resolvedPath computes the operator override path the loader will
// consult: env var first, then `<hubData>/team/templates/envelope/active.yaml`,
// then `<homeDir>/hub/team/templates/envelope/active.yaml` as the
// last resort. Returns "" only if every fallback fails to give a
// usable string.
func (l *Loader) resolvedPath() string {
	if l.overridePath != "" {
		return l.overridePath
	}
	if v := os.Getenv(envOverridePath); v != "" {
		return v
	}
	root := l.hubData
	if root == "" {
		root = os.Getenv(envHubData)
	}
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, "hub")
		}
	}
	if root == "" {
		return ""
	}
	return filepath.Join(root, "team", "templates", "envelope", "active.yaml")
}

// Resolve returns the current Templates. Hot-reload: if the file at
// `resolvedPath()` exists and its mtime has changed since the last
// load, the file is re-parsed; on parse-failure we fall through to
// the embedded default and emit one warning audit row per distinct
// error.
//
// Returns a non-nil *Templates in every code path — the embedded
// default is guaranteed parseable by a CI test in this package, so
// a successful build implies Resolve() can never return nil.
func (l *Loader) Resolve() *Templates {
	path := l.resolvedPath()
	useEmbedded := false

	var mtime time.Time
	var statErr error
	if path != "" {
		st, err := os.Stat(path)
		statErr = err
		if err == nil {
			mtime = st.ModTime()
		}
	}
	if path == "" || (statErr != nil && errors.Is(statErr, fs.ErrNotExist)) {
		useEmbedded = true
	} else if statErr != nil {
		l.warn("envelope.config_error", fmt.Sprintf("stat %q: %v", path, statErr), map[string]any{
			"path": path, "kind": "stat_failed",
		})
		useEmbedded = true
	}

	// Fast path: cached templates whose origin + path + mtime match.
	l.mu.RLock()
	if l.cached != nil {
		switch {
		case useEmbedded && l.cached.Origin == OriginEmbedded:
			t := l.cached
			l.mu.RUnlock()
			return t
		case !useEmbedded && l.cached.Origin == OriginOperator &&
			l.cachedPath == path && l.cachedMtime.Equal(mtime):
			t := l.cached
			l.mu.RUnlock()
			return t
		}
	}
	l.mu.RUnlock()

	if useEmbedded {
		return l.loadEmbedded()
	}
	return l.loadOverrideOrFallback(path, mtime)
}

// loadOverrideOrFallback reads the operator file, validates, caches,
// and returns. On any failure it warns and recurses to loadEmbedded.
// The recursion is single-step (loadEmbedded does not call back).
func (l *Loader) loadOverrideOrFallback(path string, mtime time.Time) *Templates {
	data, err := os.ReadFile(path)
	if err != nil {
		l.warn("envelope.config_error", fmt.Sprintf("read %q: %v", path, err), map[string]any{
			"path": path, "kind": "read_failed",
		})
		return l.loadEmbedded()
	}
	t, err := parseAndValidate(data)
	if err != nil {
		l.warn("envelope.config_error", fmt.Sprintf("parse %q: %v", path, err), map[string]any{
			"path": path, "kind": "parse_failed",
		})
		return l.loadEmbedded()
	}
	t.Origin = OriginOperator
	l.mu.Lock()
	l.cached = t
	l.cachedPath = path
	l.cachedMtime = mtime
	l.mu.Unlock()
	return t
}

// loadEmbedded parses the embedded fallback from hub.TemplatesFS.
// Cached separately from the operator path so a successful operator
// override can later replace it without re-parsing the embedded YAML
// on every call.
//
// A parse error here is a build-time bug (the file ships in the
// binary); we panic with a descriptive message so CI catches it
// rather than letting an unparseable embedded template reach prod.
func (l *Loader) loadEmbedded() *Templates {
	l.mu.RLock()
	if l.cached != nil && l.cached.Origin == OriginEmbedded {
		t := l.cached
		l.mu.RUnlock()
		return t
	}
	l.mu.RUnlock()

	data, err := hub.TemplatesFS.ReadFile(embeddedPath)
	if err != nil {
		panic(fmt.Sprintf("envelope: embedded %s unreadable: %v", embeddedPath, err))
	}
	t, err := parseAndValidate(data)
	if err != nil {
		panic(fmt.Sprintf("envelope: embedded %s invalid: %v", embeddedPath, err))
	}
	t.Origin = OriginEmbedded
	l.mu.Lock()
	l.cached = t
	l.cachedPath = ""
	l.cachedMtime = time.Time{}
	l.mu.Unlock()
	return t
}

// parseAndValidate YAML-decodes and runs Templates.Validate. Returns
// a parse error or validation error; never returns nil templates on
// success.
func parseAndValidate(data []byte) (*Templates, error) {
	var t Templates
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("yaml decode: %w", err)
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return &t, nil
}

// warn dispatches a non-nil Warner with the given audit action and
// summary. The action namespace is `envelope.*` to keep config
// errors distinct from per-render errors in the audit feed.
func (l *Loader) warn(action, summary string, meta map[string]any) {
	if l.warner == nil {
		return
	}
	l.warner(action, summary, meta)
}
