package server

import "testing"

// Regression lock for the v1.0.649 W11 fix: agy 1.0.1's MCP client
// sends `protocolVersion: 2025-11-25` and treats a downgrade in the
// initialize response as a fatal protocol error ("client is closing:
// invalid request"). The hub must echo back whatever known revision
// the client requested.
func TestNegotiateMCPProtocolVersion_EchoesKnown(t *testing.T) {
	cases := []struct {
		requested, want string
	}{
		// Every revision we declare support for must round-trip.
		{"2024-11-05", "2024-11-05"},
		{"2025-03-26", "2025-03-26"},
		{"2025-06-18", "2025-06-18"},
		{"2025-11-25", "2025-11-25"},
	}
	for _, c := range cases {
		if got := negotiateMCPProtocolVersion(c.requested); got != c.want {
			t.Errorf("negotiate(%q) = %q; want %q", c.requested, got, c.want)
		}
	}
}

func TestNegotiateMCPProtocolVersion_UnknownFallsBack(t *testing.T) {
	if got := negotiateMCPProtocolVersion("9999-99-99"); got != mcpProtocolVersion {
		t.Errorf("unknown version should fall back to %q; got %q", mcpProtocolVersion, got)
	}
}

func TestNegotiateMCPProtocolVersion_EmptyFallsBack(t *testing.T) {
	if got := negotiateMCPProtocolVersion(""); got != mcpProtocolVersion {
		t.Errorf("empty requested should fall back to default; got %q", got)
	}
}
