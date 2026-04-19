package hostagent

import (
	"strings"
	"testing"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		in        string
		wantKind  string
		wantBody  string
		wantMatch bool
	}{
		{`<<mcp:post_message {"text":"hi"}>>`, "post_message", `{"text":"hi"}`, true},
		{`<<mcp:request_approval {"tier":"critical"}>>`, "request_approval", `{"tier":"critical"}`, true},
		{`<<mcp:ping>>`, "ping", "", true},
		{"plain shell output", "", "", false},
		{`<<mcp:end>>`, "end", "", true},
		// Trailing CR should be tolerated (tmux pipe-pane often emits CRLF).
		{`<<mcp:ping>>` + "\r", "ping", "", true},
	}
	for _, c := range cases {
		kind, body, ok := ParseLine(c.in)
		if ok != c.wantMatch {
			t.Errorf("ParseLine(%q) match = %v, want %v", c.in, ok, c.wantMatch)
			continue
		}
		if kind != c.wantKind {
			t.Errorf("ParseLine(%q) kind = %q, want %q", c.in, kind, c.wantKind)
		}
		if string(body) != c.wantBody {
			t.Errorf("ParseLine(%q) body = %q, want %q", c.in, string(body), c.wantBody)
		}
	}
}

func TestScan(t *testing.T) {
	in := `build step 1
<<mcp:post_message {"text":"done"}>>
more output
<<mcp:ping>>
`
	var seen []string
	if err := Scan(strings.NewReader(in), func(kind string, body []byte) {
		seen = append(seen, kind+":"+string(body))
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{`post_message:{"text":"done"}`, "ping:"}
	if len(seen) != len(want) {
		t.Fatalf("got %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("idx %d: got %q want %q", i, seen[i], want[i])
		}
	}
}
