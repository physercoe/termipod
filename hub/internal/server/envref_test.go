package server

import "testing"

// The handle parser is the whole of E2's resolution contract, so its edges are
// the contract's edges: what counts as a handle, and what is refused rather
// than normalised into a different one.
func TestParseEnvRef(t *testing.T) {
	for _, tc := range []struct {
		in                     string
		ok                     bool
		family, envID, version string
	}{
		// The shape E0 actually writes: derived from LeRobot's robot_type, no
		// version, because nothing in info.json knows one.
		{in: "lerobot:so100_follower", ok: true, family: "lerobot", envID: "so100_follower"},
		// The shape a benchmark writes.
		{in: "maniskill:PickCube-v1@0.6", ok: true, family: "maniskill", envID: "PickCube-v1", version: "0.6"},
		{in: "real-site:bench-3@2026-07", ok: true, family: "real-site", envID: "bench-3", version: "2026-07"},
		// An env_id may carry colons — module- and path-shaped ids do — because
		// the family ends at the FIRST separator.
		{in: "isaac-lab:lab/east:table@v2", ok: true, family: "isaac-lab", envID: "lab/east:table", version: "v2"},
		// A version may carry a colon too; the version starts at the last '@'.
		{in: "mujoco:reach@2026-07-30T10:00Z", ok: true, family: "mujoco", envID: "reach", version: "2026-07-30T10:00Z"},
		// Surrounding whitespace is noise from a config file, not identity.
		{in: "  mujoco:reach  ", ok: true, family: "mujoco", envID: "reach"},

		// Refused, each for its own reason.
		{in: "", ok: false},
		{in: "no-separator", ok: false},
		{in: ":orphan", ok: false},
		{in: "family:", ok: false},
		// A trailing '@' names no version. Reading it as versionless would turn
		// one malformed handle into a different, valid-looking one.
		{in: "maniskill:PickCube-v1@", ok: false},
		// Ambiguous: env_id "a@b" version "1", or env_id "a" version "b@1"?
		{in: "maniskill:a@b@1", ok: false},
		// The parts written in the wrong order.
		{in: "maniskill@0.6:PickCube", ok: false},
	} {
		got, ok := parseEnvRef(tc.in)
		if ok != tc.ok {
			t.Errorf("parseEnvRef(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !tc.ok {
			continue
		}
		if got.Family != tc.family || got.EnvID != tc.envID || got.Version != tc.version {
			t.Errorf("parseEnvRef(%q) = %+v, want {%q %q %q}",
				tc.in, got, tc.family, tc.envID, tc.version)
		}
	}
}

// The stated invariant the charset validation exists to hold: every set of
// parts the write path accepts formats into a handle that parses back to the
// same parts. A row nobody can name is a row nobody can resolve, and that
// failure would look like "the registry is empty" rather than like a bug.
func TestEnvHandleRoundTripsForEveryAcceptedPart(t *testing.T) {
	families := []string{"lerobot", "maniskill", "isaac-lab", "real-site", "mujoco.v3"}
	envIDs := []string{"PickCube-v1", "so100_follower", "lab/east:table", "a b", "Isaac-Lift-Cube-Franka-v0"}
	versions := []string{"", "0.6", "v2", "2026-07-30T10:00Z", "a:b"}

	for _, f := range families {
		for _, e := range envIDs {
			for _, v := range versions {
				if !validEnvHandlePart(f, false) || !validEnvHandlePart(e, true) {
					t.Fatalf("fixture rejected by validation: family=%q env_id=%q", f, e)
				}
				if v != "" && !validEnvHandlePart(v, true) {
					t.Fatalf("fixture version rejected by validation: %q", v)
				}
				want := envRefParts{Family: f, EnvID: e, Version: v}
				ref := formatEnvRef(want)
				got, ok := parseEnvRef(ref)
				if !ok {
					t.Errorf("formatEnvRef(%+v) = %q, which does not parse", want, ref)
					continue
				}
				if got != want {
					t.Errorf("round trip of %+v via %q gave %+v", want, ref, got)
				}
			}
		}
	}
}

// The two separators are the only content rules, and they are rules because
// breaking either one breaks the round trip above.
func TestValidEnvHandlePart(t *testing.T) {
	for _, tc := range []struct {
		s          string
		allowColon bool
		want       bool
	}{
		{s: "PickCube-v1", allowColon: true, want: true},
		{s: "lab/east:table", allowColon: true, want: true},
		{s: "lab:east", allowColon: false, want: false},
		{s: "has@at", allowColon: true, want: false},
		{s: "", allowColon: true, want: false},
		{s: " padded", allowColon: true, want: false},
		{s: "padded ", allowColon: true, want: false},
		{s: "line\nbreak", allowColon: true, want: false},
		{s: string(make([]byte, 129)), allowColon: true, want: false},
	} {
		if got := validEnvHandlePart(tc.s, tc.allowColon); got != tc.want {
			t.Errorf("validEnvHandlePart(%q, %v) = %v, want %v", tc.s, tc.allowColon, got, tc.want)
		}
	}
}
