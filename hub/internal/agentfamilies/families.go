// Package agentfamilies is the single source of truth for the agent
// CLI families the hub knows about.
//
// Both the host-runner capability probe and the server's spawn-mode
// resolver depend on the same data — what binary to look for, which
// modes a family supports, which mode/billing combinations are
// blocked. Keeping that data in agent_families.yaml (and embedding it
// here) means a new family is a single YAML row, not a coordinated
// edit across hostrunner + modes + server.
//
// At runtime the embedded YAML is the default set; per-family override
// files under <DataRoot>/agent_families/<family>.yaml replace embedded
// entries by name and add new ones. The hub API hands operators a
// CRUD surface over the override directory and calls Invalidate after
// every mutation so edits land instantly without a restart.
package agentfamilies

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed agent_families.yaml
var familiesFS embed.FS

// Incompat is a single mode×billing rejection rule. Resolver consults
// the rules attached to the agent's family at spawn time; an empty
// list means "no known billing conflicts".
type Incompat struct {
	Mode    string `yaml:"mode" json:"mode"`
	Billing string `yaml:"billing" json:"billing"`
	Reason  string `yaml:"reason" json:"reason"`
}

// Family is one entry from agent_families.yaml. Field tags match the
// YAML schema 1:1; we don't promote internal aliases to the YAML so
// editing the file feels like editing data, not configuring a struct.
type Family struct {
	Family            string        `yaml:"family" json:"family"`
	Bin               string        `yaml:"bin" json:"bin"`
	VersionFlag       string        `yaml:"version_flag" json:"version_flag"`
	Supports          []string      `yaml:"supports" json:"supports"`
	Incompatibilities []Incompat    `yaml:"incompatibilities,omitempty" json:"incompatibilities,omitempty"`
	FrameProfile      *FrameProfile `yaml:"frame_profile,omitempty" json:"frame_profile,omitempty"`
}

// FrameProfile is the per-engine declarative translator for stream-json
// frames (ADR-010). Each rule is a (matcher → emit) pair; rules are
// dispatched by most-specific match (largest match-keyset wins; ties
// fire in declaration order). Anything unmatched falls through to
// today's `kind=raw, payload=verbatim` behavior.
//
// Description is the agent-facing prose header — three or four lines
// stating dispatch semantics + scope conventions inline so a fresh AI
// agent maintainer reading rule 17 sees the model without having to
// grep the implementation. Optional but strongly recommended for
// canonical profiles; see `reference/frame-profiles.md` for the
// recommended template.
//
// ProfileVersion is incremented when the schema breaks. Loaders accept
// lower versions when the change is forward-compatible; an unknown
// higher version is rejected so old hubs don't silently misbehave on a
// new overlay.
type FrameProfile struct {
	Description    string `yaml:"description,omitempty" json:"description,omitempty"`
	ProfileVersion int    `yaml:"profile_version" json:"profile_version"`
	Rules          []Rule `yaml:"rules" json:"rules"`
}

// Rule is one (match → emit) translation. The vocabulary is fixed:
//
//   - Match is a literal-equality predicate over flat top-level fields
//     of the input frame. `{type: assistant}` matches when
//     frame["type"] == "assistant". Multiple keys are AND-ed.
//   - ForEach is an expression yielding an array; the rule fires once
//     per element with that element as the inner scope and the parent
//     frame as the outer scope (`$$.…` in expressions).
//   - WhenPresent gates the emit on a non-nil expression evaluation —
//     used for "also emit usage when message.usage exists" alongside
//     a for_each over content blocks.
//   - Emit declares the resulting agent_event row.
//
// Conditional dispatch inside a for_each (claude's
// `assistant.message.content[].type ∈ {text, tool_use, …}`) is
// expressed via per-element rules with their own `match` block on the
// inner scope. See the claude-code profile in agent_families.yaml for
// the canonical example.
type Rule struct {
	Match       map[string]any `yaml:"match,omitempty" json:"match,omitempty"`
	ForEach     string         `yaml:"for_each,omitempty" json:"for_each,omitempty"`
	WhenPresent string         `yaml:"when_present,omitempty" json:"when_present,omitempty"`
	Emit        Emit           `yaml:"emit" json:"emit"`
	// SubRules fire once each on the inner scope during a for_each.
	// Used to dispatch on per-element shape (e.g. content[].type =
	// text vs tool_use). Only meaningful when ForEach is set.
	SubRules []Rule `yaml:"sub_rules,omitempty" json:"sub_rules,omitempty"`
}

// Emit declares the agent_event row a rule produces. Kind and
// Producer are literal strings.
//
// Payload shape (mutually exclusive):
//
//   - Payload map[string]string — per-field expression map. Each value
//     is evaluated against the rule's scope and the result becomes
//     the named field on the agent_event row's payload_json.
//   - PayloadExpr string — single expression yielding the *entire*
//     payload. Used when the legacy translator passes the raw frame
//     as payload (system fallback, error, the deprecated completion
//     alias). The expression must resolve to a map; non-map values
//     produce an empty payload (defensive — drives a parity-test
//     finding rather than a panic).
//
// PayloadExpr wins when both are set. Empty payload yields a
// `payload_json` of `{}`.
type Emit struct {
	Kind        string            `yaml:"kind" json:"kind"`
	Producer    string            `yaml:"producer,omitempty" json:"producer,omitempty"`
	Payload     map[string]string `yaml:"payload,omitempty" json:"payload,omitempty"`
	PayloadExpr string            `yaml:"payload_expr,omitempty" json:"payload_expr,omitempty"`
}

// Source tags whether a returned Family came from the embedded default,
// an override of an embedded family, or a custom file with no embedded
// counterpart. Drives the badge the mobile UI shows next to each row.
type Source string

const (
	SourceEmbedded Source = "embedded"
	SourceOverride Source = "override"
	SourceCustom   Source = "custom"
)

// View pairs a family record with its origin tag. List endpoints return
// []View so the UI can render "default vs custom" without re-reading the
// disk twice.
type View struct {
	Family Family `json:"family"`
	Source Source `json:"source"`
}

// Registry holds the merged embedded + overlay family list, with a
// lazy cache that Invalidate clears on every mutation. The zero value
// is not usable — call New.
type Registry struct {
	overlayDir string

	mu       sync.RWMutex
	cached   []View
	cachedErr error
	loaded   bool
}

// New constructs a registry that reads embedded defaults first, then
// overlays any *.yaml file found in overlayDir. An empty overlayDir is
// allowed — the registry behaves as embedded-only.
func New(overlayDir string) *Registry {
	return &Registry{overlayDir: overlayDir}
}

// All returns the full merged list as []View. View pairs the family
// record with its source tag (embedded / override / custom).
func (r *Registry) All() ([]View, error) {
	r.mu.RLock()
	if r.loaded {
		out := append([]View(nil), r.cached...)
		err := r.cachedErr
		r.mu.RUnlock()
		return out, err
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return append([]View(nil), r.cached...), r.cachedErr
	}
	views, err := r.loadLocked()
	r.cached = views
	r.cachedErr = err
	r.loaded = true
	return append([]View(nil), views...), err
}

// Families returns only the Family records, dropping the source tag.
// Equivalent to All() projected to .Family for callers that don't care
// about provenance (host-runner probe, resolver lookup).
func (r *Registry) Families() ([]Family, error) {
	views, err := r.All()
	if err != nil {
		return nil, err
	}
	out := make([]Family, len(views))
	for i, v := range views {
		out[i] = v.Family
	}
	return out, nil
}

// ByName returns the merged family entry by name. Boolean is false when
// the name isn't known — callers treat that as "not in the closed set".
func (r *Registry) ByName(name string) (Family, bool) {
	views, err := r.All()
	if err != nil {
		return Family{}, false
	}
	for _, v := range views {
		if v.Family.Family == name {
			return v.Family, true
		}
	}
	return Family{}, false
}

// Invalidate drops the cache. The next All() call re-reads the embedded
// YAML and rescans the overlay directory. Cheap — call it after every
// successful PUT/DELETE on the overlay.
func (r *Registry) Invalidate() {
	r.mu.Lock()
	r.cached = nil
	r.cachedErr = nil
	r.loaded = false
	r.mu.Unlock()
}

// OverlayDir is the absolute path the registry writes override files
// to. Empty when the registry runs without an overlay (tests, embedded-
// only mode). Handlers use this to resolve PUT/DELETE targets.
func (r *Registry) OverlayDir() string { return r.overlayDir }

// loadLocked merges embedded YAML with overlay files. Caller holds r.mu.
//
// Resolution order:
//  1. Parse embedded YAML — these become source=embedded.
//  2. Scan overlayDir for *.yaml; each parsed file either replaces an
//     embedded entry by .Family (source flips to override) or adds a
//     new entry (source=custom).
//
// Malformed overlay files are logged-and-skipped, never fatal — a bad
// hand-edit shouldn't take down the hub. A malformed embedded file is
// fatal because that's a build-time bug.
func (r *Registry) loadLocked() ([]View, error) {
	embedded, err := readEmbedded()
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(embedded))
	indexByName := make(map[string]int, len(embedded))
	for _, f := range embedded {
		indexByName[f.Family] = len(views)
		views = append(views, View{Family: f, Source: SourceEmbedded})
	}

	if r.overlayDir == "" {
		return views, nil
	}
	entries, err := os.ReadDir(r.overlayDir)
	if err != nil {
		if os.IsNotExist(err) {
			return views, nil
		}
		return views, fmt.Errorf("read overlay dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(r.overlayDir, e.Name())
		fam, ferr := readOverlayFile(path)
		if ferr != nil {
			fmt.Fprintf(os.Stderr, "agentfamilies: skip %s: %v\n", path, ferr)
			continue
		}
		if i, ok := indexByName[fam.Family]; ok {
			views[i] = View{Family: fam, Source: SourceOverride}
			continue
		}
		indexByName[fam.Family] = len(views)
		views = append(views, View{Family: fam, Source: SourceCustom})
	}
	return views, nil
}

func readEmbedded() ([]Family, error) {
	b, err := fs.ReadFile(familiesFS, "agent_families.yaml")
	if err != nil {
		return nil, fmt.Errorf("read agent_families.yaml: %w", err)
	}
	var doc struct {
		Families []Family `yaml:"families"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse agent_families.yaml: %w", err)
	}
	return doc.Families, nil
}

func readOverlayFile(path string) (Family, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Family{}, err
	}
	var f Family
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return Family{}, fmt.Errorf("parse: %w", err)
	}
	if f.Family == "" {
		return Family{}, fmt.Errorf("missing family name")
	}
	return f, nil
}

// Default is the package-level registry used when callers don't carry
// their own. It runs embedded-only — server.New replaces it via
// SetDefault once the DataRoot is known so the spawn path picks up the
// overlay too.
var defaultMu sync.RWMutex
var defaultRegistry = New("")

// SetDefault swaps the package-level registry. Server.New calls it
// once at startup with a registry rooted at <DataRoot>/agent_families.
// Tests that need a clean default can call SetDefault(New("")) in a
// t.Cleanup.
func SetDefault(r *Registry) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultRegistry = r
}

func currentDefault() *Registry {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultRegistry
}

// All is the package-level shim retained for callers that don't hold a
// Registry handle (host-runner embedded fallback, modes/spawn helpers).
// Returns the merged Family list of the current default registry.
func All() ([]Family, error) { return currentDefault().Families() }

// ByName is the package-level shim. Equivalent to
// currentDefault().ByName(name).
func ByName(name string) (Family, bool) { return currentDefault().ByName(name) }
