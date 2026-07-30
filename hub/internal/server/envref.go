package server

import "strings"

// env_ref — the opaque environment handle (plan
// docs/plans/environments-and-embodiments.md, wedges E0 + E2).
//
// E0 put the string on runs and datasets deliberately unvalidated, so
// provenance could accumulate before this registry existed. E2 is the other
// half: turning a handle into a row. That means parsing it — and parsing it
// only this far. `family:env_id@version` splits into three identity parts and
// stops; nothing here reads meaning out of the parts (no "lerobot means a real
// robot", no version ordering), because the model says the handle names an
// identity, not a semantics.
//
// Resolution is EXACT. A versionless ref does not match a versioned row: which
// version it meant is exactly the question, and answering it by picking one
// would put a guess where the plan promised an honest "unresolved". Matching is
// case-sensitive for the same reason.

// envRefParts is one parsed handle. Version is empty for a handle that names
// none — the common case, since E0's derived handles are versionless.
type envRefParts struct {
	Family  string
	EnvID   string
	Version string
}

// parseEnvRef splits a handle into its three parts. ok is false for anything
// that cannot unambiguously name a row.
//
// The family ends at the FIRST ':' so an env_id may contain colons (module and
// path-shaped ids do). The version begins after the LAST '@' — and an env_id
// carrying its own '@' is refused rather than split at a guess, which is also
// why writes refuse '@' in an env_id.
func parseEnvRef(ref string) (envRefParts, bool) {
	s := strings.TrimSpace(ref)
	colon := strings.Index(s, ":")
	if colon <= 0 {
		return envRefParts{}, false
	}
	family, rest := s[:colon], s[colon+1:]
	envID, version := rest, ""
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		envID, version = rest[:at], rest[at+1:]
		// A trailing '@' names no version. Reading it as versionless would
		// normalise a malformed handle into a different one.
		if version == "" {
			return envRefParts{}, false
		}
	}
	if family == "" || envID == "" {
		return envRefParts{}, false
	}
	// '@' left in either part means the handle is ambiguous: "f:a@b@1" could
	// be env_id "a@b" version "1" or env_id "a" version "b@1".
	if strings.Contains(family, "@") || strings.Contains(envID, "@") {
		return envRefParts{}, false
	}
	return envRefParts{Family: family, EnvID: envID, Version: version}, true
}

// formatEnvRef rebuilds the handle a row answers to. It is the inverse of
// parseEnvRef for every row the write path accepts — the invariant the charset
// validation below exists to hold, because a row nobody can name is a row
// nobody can resolve.
func formatEnvRef(p envRefParts) string {
	if p.Version == "" {
		return p.Family + ":" + p.EnvID
	}
	return p.Family + ":" + p.EnvID + "@" + p.Version
}

// validEnvHandlePart accepts an identity part that survives that round trip.
// Deliberately permissive about content — env ids in the wild are
// `PickCube-v1`, `Isaac-Lift-Cube-Franka-v0`, `bench-3`, `lab/east:table` — and
// strict only about the two separators, about whitespace at the edges, and
// about control characters, which would make a handle unreadable in a log line
// or a chip.
func validEnvHandlePart(s string, allowColon bool) bool {
	if s == "" || len(s) > 128 || strings.TrimSpace(s) != s {
		return false
	}
	if strings.ContainsAny(s, "@\n\r\t") {
		return false
	}
	if !allowColon && strings.Contains(s, ":") {
		return false
	}
	return true
}
