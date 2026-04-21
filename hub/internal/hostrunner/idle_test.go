package hostrunner

import (
	"testing"
	"time"
)

func TestIdleDetector_RaisesOncePerStreak(t *testing.T) {
	d := NewIdleDetector(10 * time.Millisecond)
	base := time.Now()

	text := "doing work...\ninstall package foo? [yN] "

	// First capture — establishes baseline, never raises.
	st, raise := d.Inspect(text, paneState{}, base)
	if raise {
		t.Fatalf("should not raise on first observation")
	}
	if st.hash == "" {
		t.Fatalf("expected hash to be set")
	}

	// Same text before threshold — still no raise.
	st, raise = d.Inspect(text, st, base.Add(5*time.Millisecond))
	if raise {
		t.Fatalf("should not raise before threshold")
	}

	// Same text past threshold with prompt tail — raises once.
	st, raise = d.Inspect(text, st, base.Add(50*time.Millisecond))
	if !raise {
		t.Fatalf("expected raise once threshold exceeded")
	}

	// Second inspection of the same hash must not raise again.
	_, raise = d.Inspect(text, st, base.Add(100*time.Millisecond))
	if raise {
		t.Fatalf("should not raise twice for the same stuck state")
	}
}

func TestIdleDetector_NoRaiseWithoutPromptTail(t *testing.T) {
	d := NewIdleDetector(1 * time.Millisecond)
	base := time.Now()
	text := "compile step 14 of 30: linking..." // no prompt marker
	st, _ := d.Inspect(text, paneState{}, base)
	_, raise := d.Inspect(text, st, base.Add(10*time.Millisecond))
	if raise {
		t.Fatalf("compile line should not match idle-prompt regex")
	}
}

func TestIdleDetector_ResetOnChange(t *testing.T) {
	d := NewIdleDetector(5 * time.Millisecond)
	base := time.Now()
	prompt := "install? [yN] "
	st, _ := d.Inspect(prompt, paneState{}, base)
	st, raised := d.Inspect(prompt, st, base.Add(10*time.Millisecond))
	if !raised {
		t.Fatalf("expected raise")
	}

	// Output changes — should reset, then a new stuck run raises again.
	st, _ = d.Inspect("y\ninstalling...\nremove foo? [yN] ", st, base.Add(20*time.Millisecond))
	st, raisedAgain := d.Inspect("y\ninstalling...\nremove foo? [yN] ", st, base.Add(40*time.Millisecond))
	if !raisedAgain {
		t.Fatalf("new stuck streak must raise after reset")
	}
	_ = st
}
