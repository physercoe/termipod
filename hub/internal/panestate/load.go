package panestate

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed manifests/vendor/*.toml
var vendorFS embed.FS

//go:embed manifests/overlay
var overlayFS embed.FS

const (
	vendorDir  = "manifests/vendor"
	overlayDir = "manifests/overlay"

	// SourceVendor / SourceOverlay label where a manifest came from.
	SourceVendor  = "vendor"
	SourceOverlay = "overlay"
)

// Registry is the loaded, validated, compiled rule set plus the mapping from
// our agent families onto manifest ids.
type Registry struct {
	byID    map[string]compiledManifest
	mapping map[string]string // agent family (agents.kind) -> manifest id
	ids     []string
}

// overlayConfig is manifests/overlay/config.yaml — everything
// termipod-specific lives here so the vendored TOMLs stay byte-exact and
// diffable against upstream (plan D-1).
type overlayConfig struct {
	// Engines maps an agent family to a manifest id. Plan D-3: this mapping
	// lives in the overlay, NEVER in a vendored file, because upstream's ids
	// (`claude`, `agy`) are not our family names (`claude-code`,
	// `antigravity`).
	Engines map[string]string `yaml:"engines"`
}

var (
	loadOnce sync.Once
	registry *Registry
	loadErr  error
)

// Load parses, validates and compiles the embedded manifests once per
// process.
func Load() (*Registry, error) {
	loadOnce.Do(func() { registry, loadErr = build() })
	return registry, loadErr
}

// MustLoad is Load for callers with no error path. A failure is a build-time
// defect — the embedded set is validated by this package's tests.
func MustLoad() *Registry {
	r, err := Load()
	if err != nil {
		panic("panestate: embedded manifests are invalid: " + err.Error())
	}
	return r
}

func build() (*Registry, error) {
	reg := &Registry{byID: map[string]compiledManifest{}}

	if err := loadDir(reg, vendorFS, vendorDir, SourceVendor); err != nil {
		return nil, err
	}
	// Overlay wins by precedence: a same-id file replaces the vendored
	// manifest ENTIRELY rather than merging into it. Upstream's own
	// local-override rule, repurposed as our extension point — a partial
	// merge would make "what is actually in force?" unanswerable from either
	// file alone.
	if err := loadDir(reg, overlayFS, overlayDir, SourceOverlay); err != nil {
		return nil, err
	}

	cfg, err := loadOverlayConfig()
	if err != nil {
		return nil, err
	}
	reg.mapping = map[string]string{}
	for family, id := range cfg.Engines {
		if family == "" {
			return nil, fmt.Errorf("panestate: overlay config has an empty family key")
		}
		if _, ok := reg.byID[id]; !ok {
			// Plan D-3: an unknown manifest id in the mapping fails
			// validation loudly. Silently dropping it would leave the family
			// unevaluated, which looks exactly like "this engine has no
			// rules yet".
			return nil, fmt.Errorf("panestate: overlay maps family %q to unknown manifest %q",
				family, id)
		}
		reg.mapping[family] = id
	}

	for id := range reg.byID {
		reg.ids = append(reg.ids, id)
	}
	sort.Strings(reg.ids)
	return reg, nil
}

func loadDir(reg *Registry, fsys fs.FS, dir, source string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("panestate: read %s: %w", dir, err)
	}
	// Sorted so a duplicate-id collision reports deterministically.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		data, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return fmt.Errorf("panestate: read %s/%s: %w", dir, name, err)
		}
		m, err := ParseManifest(data, source)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", dir, name, err)
		}
		if source == SourceVendor {
			if _, dup := reg.byID[m.ID]; dup {
				return fmt.Errorf("panestate: duplicate manifest id %q in %s", m.ID, dir)
			}
		}
		cm, err := compileManifest(m)
		if err != nil {
			return err
		}
		reg.byID[m.ID] = cm
	}
	return nil
}

func loadOverlayConfig() (overlayConfig, error) {
	data, err := overlayFS.ReadFile(path.Join(overlayDir, "config.yaml"))
	if err != nil {
		return overlayConfig{}, fmt.Errorf("panestate: read overlay config: %w", err)
	}
	var cfg overlayConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return overlayConfig{}, fmt.Errorf("panestate: parse overlay config: %w", err)
	}
	return cfg, nil
}

// ManifestIDs returns every loaded manifest id, sorted.
func (r *Registry) ManifestIDs() []string {
	out := make([]string, len(r.ids))
	copy(out, r.ids)
	return out
}

// ManifestForFamily returns the manifest id bound to an agent family.
//
// An UNMAPPED family gets no manifest and no evaluation — never a guess
// (plan D-3). Classifying an engine with another engine's rules would produce
// confident, wrong attention, which is worse than the silence we have today.
func (r *Registry) ManifestForFamily(family string) (string, bool) {
	id, ok := r.mapping[family]
	return id, ok
}

// Families returns every mapped agent family, sorted.
func (r *Registry) Families() []string {
	out := make([]string, 0, len(r.mapping))
	for f := range r.mapping {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// Manifest returns the parsed manifest for an id, for surfaces that want to
// render the rules rather than evaluate them.
func (r *Registry) Manifest(id string) (Manifest, bool) {
	cm, ok := r.byID[id]
	if !ok {
		return Manifest{}, false
	}
	return cm.manifest, true
}

// EvaluateManifest classifies a screen against a manifest id.
func (r *Registry) EvaluateManifest(id string, in Input) (Explain, error) {
	cm, ok := r.byID[id]
	if !ok {
		return Explain{}, fmt.Errorf("panestate: no manifest %q", id)
	}
	return cm.Evaluate(in), nil
}

// EvaluateFamily classifies a screen for one of our agent families.
// Returns ErrNoManifest when the family is unmapped.
func (r *Registry) EvaluateFamily(family string, in Input) (Explain, error) {
	id, ok := r.mapping[family]
	if !ok {
		return Explain{}, fmt.Errorf("%w: %q", ErrNoManifest, family)
	}
	return r.EvaluateManifest(id, in)
}

// ErrNoManifest is returned for an agent family with no manifest mapping.
var ErrNoManifest = errNoManifest{}

type errNoManifest struct{}

func (errNoManifest) Error() string { return "panestate: family has no manifest mapping" }
