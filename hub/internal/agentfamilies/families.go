// Package agentfamilies is the single source of truth for the agent
// CLI families the hub knows about.
//
// Both the host-runner capability probe and the server's spawn-mode
// resolver depend on the same data — what binary to look for, which
// modes a family supports, which mode/billing combinations are
// blocked. Keeping that data in agent_families.yaml (and embedding it
// here) means a new family is a single YAML row, not a coordinated
// edit across hostrunner + modes + server.
package agentfamilies

import (
	"embed"
	"fmt"
	"io/fs"
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
	Family            string     `yaml:"family"`
	Bin               string     `yaml:"bin"`
	VersionFlag       string     `yaml:"version_flag"`
	Supports          []string   `yaml:"supports"`
	Incompatibilities []Incompat `yaml:"incompatibilities"`
}

var (
	loaded     []Family
	loadedErr  error
	loadedOnce sync.Once
)

// All returns the full family list parsed from the embedded YAML. The
// parse runs once per process; subsequent calls are cheap. A parse
// error here is a build-time bug — the YAML is committed alongside
// the loader — but we surface it rather than panic so tests can fail
// loudly.
func All() ([]Family, error) {
	loadedOnce.Do(func() {
		b, err := fs.ReadFile(familiesFS, "agent_families.yaml")
		if err != nil {
			loadedErr = fmt.Errorf("read agent_families.yaml: %w", err)
			return
		}
		var doc struct {
			Families []Family `yaml:"families"`
		}
		if err := yaml.Unmarshal(b, &doc); err != nil {
			loadedErr = fmt.Errorf("parse agent_families.yaml: %w", err)
			return
		}
		loaded = doc.Families
	})
	return loaded, loadedErr
}

// ByName returns the family entry whose .Family matches name. The
// boolean is false when name is unknown — callers treat that as
// "family not in the closed set" and surface a clean error rather
// than fabricating defaults.
func ByName(name string) (Family, bool) {
	fams, err := All()
	if err != nil {
		return Family{}, false
	}
	for _, f := range fams {
		if f.Family == name {
			return f, true
		}
	}
	return Family{}, false
}
