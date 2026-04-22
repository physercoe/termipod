package hostrunner

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// stateFile is the on-disk record that lets a host-runner skip the
// register round-trip on restart. It's keyed by (hub, team, name) so one
// state dir can hold records for multiple hubs without collision.
//
// The server-side host-register is also idempotent via UPSERT, so losing
// this file isn't fatal — the runner just pays one extra HTTP call on
// its next boot.
type stateFile struct {
	Entries []stateEntry `json:"entries"`
}

type stateEntry struct {
	Hub    string `json:"hub"`
	Team   string `json:"team"`
	Name   string `json:"name"`
	HostID string `json:"host_id"`
}

func statePath(dir string) string {
	return filepath.Join(dir, "host-runner.json")
}

func loadStateEntry(dir, hub, team, name string) (string, bool) {
	if dir == "" {
		return "", false
	}
	data, err := os.ReadFile(statePath(dir))
	if err != nil {
		return "", false
	}
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return "", false
	}
	for _, e := range sf.Entries {
		if e.Hub == hub && e.Team == team && e.Name == name && e.HostID != "" {
			return e.HostID, true
		}
	}
	return "", false
}

func saveStateEntry(dir, hub, team, name, hostID string) error {
	if dir == "" || hostID == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var sf stateFile
	if data, err := os.ReadFile(statePath(dir)); err == nil {
		_ = json.Unmarshal(data, &sf)
	}
	replaced := false
	for i := range sf.Entries {
		if sf.Entries[i].Hub == hub && sf.Entries[i].Team == team && sf.Entries[i].Name == name {
			sf.Entries[i].HostID = hostID
			replaced = true
			break
		}
	}
	if !replaced {
		sf.Entries = append(sf.Entries,
			stateEntry{Hub: hub, Team: team, Name: name, HostID: hostID})
	}
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
