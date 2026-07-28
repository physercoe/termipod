package server

import "testing"

// A crafted team URL parameter must not escape the templates/journals roots
// (path-injection barrier — the teamTemplatesDir/journalPath sinks CodeQL
// flagged). safePathSegment already gates handlers_project_start; these two
// sinks now share it.
func TestTeamTemplatesDir_RejectsTraversal(t *testing.T) {
	bad := []string{"../../etc", "..", "a/b", `a\b`, ".hidden", ""}
	for _, team := range bad {
		if got := teamTemplatesDir("/data", team); got != "" {
			t.Errorf("teamTemplatesDir(/data, %q) = %q; want \"\" (unsafe team)", team, got)
		}
	}
	if got := teamTemplatesDir("/data", "team-abc"); got == "" {
		t.Fatal("teamTemplatesDir rejected a valid team slug")
	}
}

func TestJournalPath_RejectsTraversalTeam(t *testing.T) {
	s := &Server{cfg: Config{DataRoot: "/data"}}
	if _, err := s.journalPath("../../etc", "worker"); err == nil {
		t.Fatal("journalPath accepted a traversing team")
	}
	if _, err := s.journalPath("team-abc", "worker"); err != nil {
		t.Fatalf("journalPath rejected a valid team/handle: %v", err)
	}
}
