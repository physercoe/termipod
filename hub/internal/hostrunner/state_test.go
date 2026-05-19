package hostrunner

import "testing"

// TestResolveBearerToken_FallsBackToFlag confirms that with no
// persisted token the --token flag value is used.
func TestResolveBearerToken_FallsBackToFlag(t *testing.T) {
	tok, rotated := ResolveBearerToken(t.TempDir(), "hub", "team", "name", "flag-token")
	if rotated || tok != "flag-token" {
		t.Errorf("got (%q, %v), want (flag-token, false)", tok, rotated)
	}
}

// TestSaveLoadStateToken round-trips a persisted token and confirms it
// is keyed by (hub, team, name).
func TestSaveLoadStateToken(t *testing.T) {
	dir := t.TempDir()
	if err := saveStateToken(dir, "hub", "team", "name", "tok-1"); err != nil {
		t.Fatalf("saveStateToken: %v", err)
	}
	if got := loadStateToken(dir, "hub", "team", "name"); got != "tok-1" {
		t.Errorf("loadStateToken = %q, want tok-1", got)
	}
	if got := loadStateToken(dir, "hub", "team", "other"); got != "" {
		t.Errorf("loadStateToken for a different name = %q, want empty", got)
	}
}

// TestSaveStateToken_PreservesHostID confirms persisting a token does
// not clobber a host_id saved into the same entry.
func TestSaveStateToken_PreservesHostID(t *testing.T) {
	dir := t.TempDir()
	if err := saveStateEntry(dir, "hub", "team", "name", "host-99"); err != nil {
		t.Fatalf("saveStateEntry: %v", err)
	}
	if err := saveStateToken(dir, "hub", "team", "name", "tok-9"); err != nil {
		t.Fatalf("saveStateToken: %v", err)
	}
	if id, ok := loadStateEntry(dir, "hub", "team", "name"); !ok || id != "host-99" {
		t.Errorf("host_id = (%q, %v), want (host-99, true)", id, ok)
	}
	if got := loadStateToken(dir, "hub", "team", "name"); got != "tok-9" {
		t.Errorf("token = %q, want tok-9", got)
	}
}
