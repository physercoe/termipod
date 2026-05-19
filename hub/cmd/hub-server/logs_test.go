package main

import (
	"strings"
	"testing"
)

func TestJournalctlArgs(t *testing.T) {
	got := strings.Join(journalctlArgs("termipod-hub.service", 200, false), " ")
	if got != "-u termipod-hub.service -n 200 --no-pager" {
		t.Errorf("snapshot args = %q", got)
	}
	got = strings.Join(journalctlArgs("custom.service", 50, true), " ")
	if got != "-u custom.service -n 50 --no-pager -f" {
		t.Errorf("follow args = %q", got)
	}
}
