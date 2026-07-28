package hostrunner

import "testing"

func TestHomeRelativePath(t *testing.T) {
	cases := []struct {
		abs, home, want string
	}{
		{"/home/alice/hub-work/t/p/wt", "/home/alice", "~/hub-work/t/p/wt"},
		{"/home/alice", "/home/alice", "~"},
		{"/home/alice/", "/home/alice", "~"},
		{"/data/agents/wt", "/home/alice", "/data/agents/wt"}, // not under home → verbatim
		{"relative/path", "/home/alice", "relative/path"},     // not absolute → verbatim
		{"/home/alice/x", "", "/home/alice/x"},                // no home → verbatim
	}
	for _, c := range cases {
		if got := homeRelativePath(c.abs, c.home); got != c.want {
			t.Errorf("homeRelativePath(%q, %q) = %q, want %q", c.abs, c.home, got, c.want)
		}
	}
}

func TestExpandHomeWith(t *testing.T) {
	cases := []struct {
		p, home, want string
		wantErr       bool
	}{
		{"~/hub-work/t/p/wt", "/data/agents", "/data/agents/hub-work/t/p/wt", false},
		{"~", "/data/agents", "/data/agents", false},
		{"/abs/path", "/data/agents", "/abs/path", false}, // non-tilde → verbatim
		{"~user/x", "/data/agents", "~user/x", false},     // ~user unsupported → verbatim
		{"~/x", "", "", true},                             // tilde but no home → error
	}
	for _, c := range cases {
		got, err := expandHomeWith(c.p, c.home)
		if c.wantErr {
			if err == nil {
				t.Errorf("expandHomeWith(%q, %q): expected error", c.p, c.home)
			}
			continue
		}
		if err != nil {
			t.Errorf("expandHomeWith(%q, %q): unexpected error %v", c.p, c.home, err)
			continue
		}
		if got != c.want {
			t.Errorf("expandHomeWith(%q, %q) = %q, want %q", c.p, c.home, got, c.want)
		}
	}
}

// The round trip: an absolute path under home, made portable, re-anchors under a
// DIFFERENT home to the same relative location.
func TestPathPortabilityRoundTrip(t *testing.T) {
	src := "/home/alice/hub-work/team/pid/worker-1"
	portable := homeRelativePath(src, "/home/alice")
	got, err := expandHomeWith(portable, "/data/agents")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/data/agents/hub-work/team/pid/worker-1" {
		t.Fatalf("re-anchored to %q", got)
	}
}
