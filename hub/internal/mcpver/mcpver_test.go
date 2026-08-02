package mcpver

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// Regression lock for the v1.0.649 W11 fix, carried here from
// server/mcp_protocol_negotiation_test.go when the three copies
// collapsed onto this package: agy 1.0.1's MCP client sends
// `protocolVersion: 2025-11-25` and treats a downgrade in the
// initialize response as a fatal protocol error ("client is closing:
// invalid request"). We must echo back whatever known revision the
// client requested — every revision in the set, not just the newest.
func TestNegotiate_EchoesEveryKnownRevision(t *testing.T) {
	for _, v := range Supported() {
		if got := Negotiate(v); got != v {
			t.Errorf("Negotiate(%q) = %q; want the same revision echoed back", v, got)
		}
	}
}

func TestNegotiate_UnknownFallsBackToFloor(t *testing.T) {
	// Both directions of unknown: a future revision we have not
	// implemented, and a string that is not a revision at all.
	for _, ask := range []string{"2027-01-01", "9999-99-99", "banana"} {
		if got := Negotiate(ask); got != Floor {
			t.Errorf("Negotiate(%q) = %q; want the floor %q", ask, got, Floor)
		}
	}
}

func TestNegotiate_EmptyFallsBackToFloor(t *testing.T) {
	if got := Negotiate(""); got != Floor {
		t.Errorf("Negotiate(\"\") = %q; want the floor %q", got, Floor)
	}
}

// The floor must itself be a revision we speak — otherwise the answer we
// give an unknown client is a version we would reject if they asked for
// it directly.
func TestFloorIsSupported(t *testing.T) {
	if !IsSupported(Floor) {
		t.Fatalf("floor %q is not in the supported set", Floor)
	}
	if Supported()[0] != Floor {
		t.Errorf("floor should be the oldest revision; set starts at %q", Supported()[0])
	}
}

// Supported() hands its slice to callers that serialize it straight into
// a server/discover response. A caller mutating that slice must not be
// able to edit the package's own set.
func TestSupportedReturnsACopy(t *testing.T) {
	got := Supported()
	got[0] = "tampered"
	if Supported()[0] != Floor {
		t.Fatal("Supported() handed out the package's own backing array")
	}
}

// versionsFixture is the cross-language parity corpus (ADR-063 D1). The
// desktop bridge cannot import this package, so both sides are pinned to
// this file instead; browserbridge.test.ts reads the same bytes.
type versionsFixture struct {
	Floor     string   `json:"floor"`
	Supported []string `json:"supported"`
}

func TestSupportedMatchesFixture(t *testing.T) {
	raw, err := os.ReadFile("versions.json")
	if err != nil {
		t.Fatalf("read parity fixture: %v", err)
	}
	var fx versionsFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("parse parity fixture: %v", err)
	}
	if fx.Floor != Floor {
		t.Errorf("fixture floor %q != package floor %q", fx.Floor, Floor)
	}
	got := Supported()
	if len(got) != len(fx.Supported) {
		t.Fatalf("fixture lists %d revisions, package has %d — update both (and browserbridge.ts)",
			len(fx.Supported), len(got))
	}
	for i := range got {
		if got[i] != fx.Supported[i] {
			t.Errorf("revision %d: package %q, fixture %q", i, got[i], fx.Supported[i])
		}
	}
}

// The 2026-07-28 revision is the reason this package exists. Pin it
// explicitly so a careless edit to the set cannot silently drop the
// revision lane U was built to answer.
func TestSupports2026Revision(t *testing.T) {
	if !IsSupported("2026-07-28") {
		t.Fatal("2026-07-28 must stay in the set — lane U implements its response side")
	}
}

func TestAnySupported(t *testing.T) {
	if !AnySupported("nonsense", "2025-11-25") {
		t.Error("one known ask among unknowns should count")
	}
	if AnySupported("2027-01-01", "", "banana") {
		t.Error("no known ask should be false")
	}
	if AnySupported() {
		t.Error("no asks at all should be false")
	}
}

// The observability half of the D2 amendment: declared-but-unsupported
// logs exactly once per (server, declared set) — one line is the signal,
// a line per request is the noise that buries it. Nothing is logged for
// silence (no declaration) or for a set containing a known revision.
func TestWarnIfUnsupported_OncePerServerAndSet(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	WarnIfUnsupported("warntest-a", "2027-01-01")
	WarnIfUnsupported("warntest-a", "2027-01-01") // dup — suppressed
	WarnIfUnsupported("warntest-a")               // silence — no warning
	WarnIfUnsupported("warntest-a", "2025-11-25") // known — no warning
	WarnIfUnsupported("warntest-b", "2027-01-01") // other server — its own line

	if got := strings.Count(buf.String(), "unsupported protocol version"); got != 2 {
		t.Fatalf("expected exactly 2 warnings (one per server), got %d:\n%s", got, buf.String())
	}
}
