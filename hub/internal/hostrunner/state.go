package hostrunner

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// stateFile is the on-disk record that lets a host-runner skip the
// register round-trip on restart and pick up a rotated bearer token.
// It's keyed by (hub, team, name) so one state dir can hold records for
// multiple hubs without collision.
//
// The server-side host-register is also idempotent via UPSERT, so
// losing the host_id isn't fatal — the runner just pays one extra HTTP
// call on its next boot. Losing the rotated token IS load-bearing: a
// host.token_rotate verb persists here so the new token survives a
// restart (ADR-028 W20).
type stateFile struct {
	Entries []stateEntry `json:"entries"`
}

type stateEntry struct {
	Hub    string `json:"hub"`
	Team   string `json:"team"`
	Name   string `json:"name"`
	HostID string `json:"host_id,omitempty"`
	// Token is the bearer a host.token_rotate verb installed. Empty
	// until the first rotation — the runner then falls back to the
	// --token flag. Stored 0600; never logged.
	Token string `json:"token,omitempty"`
}

func statePath(dir string) string {
	return filepath.Join(dir, "host-runner.json")
}

func loadStateEntry(dir, hub, team, name string) (string, bool) {
	e, ok := findStateEntry(dir, hub, team, name)
	if !ok || e.HostID == "" {
		return "", false
	}
	return e.HostID, true
}

// loadStateToken returns the rotated bearer for this (hub, team, name),
// or "" if none has been installed — the caller then uses the --token
// flag.
func loadStateToken(dir, hub, team, name string) string {
	e, ok := findStateEntry(dir, hub, team, name)
	if !ok {
		return ""
	}
	return e.Token
}

// ResolveBearerToken picks the bearer host-runner should boot with: a
// token persisted by a prior host.token_rotate verb takes precedence
// over the --token flag, so a rotation survives a restart (ADR-028
// W20). The bool reports whether the persisted token was chosen.
func ResolveBearerToken(stateDir, hub, team, name, flagToken string) (string, bool) {
	if rotated := loadStateToken(stateDir, hub, team, name); rotated != "" {
		return rotated, true
	}
	return flagToken, false
}

func findStateEntry(dir, hub, team, name string) (stateEntry, bool) {
	if dir == "" {
		return stateEntry{}, false
	}
	data, err := os.ReadFile(statePath(dir))
	if err != nil {
		return stateEntry{}, false
	}
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return stateEntry{}, false
	}
	for _, e := range sf.Entries {
		if e.Hub == hub && e.Team == team && e.Name == name {
			return e, true
		}
	}
	return stateEntry{}, false
}

func saveStateEntry(dir, hub, team, name, hostID string) error {
	if dir == "" || hostID == "" {
		return nil
	}
	return updateStateEntry(dir, hub, team, name, func(e *stateEntry) {
		e.HostID = hostID
	})
}

// saveStateToken persists a rotated bearer for this (hub, team, name).
func saveStateToken(dir, hub, team, name, token string) error {
	if dir == "" || token == "" {
		return nil
	}
	return updateStateEntry(dir, hub, team, name, func(e *stateEntry) {
		e.Token = token
	})
}

// updateStateEntry loads the state file, finds-or-creates the entry for
// (hub, team, name), applies mutate, and writes the file back via a
// rename so a crash mid-write can't truncate it.
func updateStateEntry(dir, hub, team, name string, mutate func(*stateEntry)) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var sf stateFile
	if data, err := os.ReadFile(statePath(dir)); err == nil {
		_ = json.Unmarshal(data, &sf)
	}
	idx := -1
	for i := range sf.Entries {
		if sf.Entries[i].Hub == hub && sf.Entries[i].Team == team && sf.Entries[i].Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		sf.Entries = append(sf.Entries,
			stateEntry{Hub: hub, Team: team, Name: name})
		idx = len(sf.Entries) - 1
	}
	mutate(&sf.Entries[idx])
	buf, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(dir) + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(dir))
}
