package hostagent

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
)

// IdleDetector watches tmux panes for the "agent is stuck at a prompt"
// pattern: the tail of the pane looks like a waiting prompt AND the
// capture hash hasn't moved for longer than the idle threshold. When
// both conditions hold we POST an attention item to the hub, once per
// idle streak — the flag resets when the pane changes again.
//
// This is intentionally dumb: capture-pane + regex + hash. It catches
// the common failure mode (interactive prompt waiting for y/N) without
// needing marker injection on the agent side.
type IdleDetector struct {
	Threshold time.Duration
	Regex     *regexp.Regexp
}

var defaultIdleRegex = regexp.MustCompile(
	`(?m)(\[[yYnN/]+\]\s*$|^[?>$#%]\s*$|(?i:password:\s*$)|\(y/n\)\s*$|Continue\??\s*$)`,
)

func NewIdleDetector(threshold time.Duration) *IdleDetector {
	if threshold <= 0 {
		threshold = 90 * time.Second
	}
	return &IdleDetector{Threshold: threshold, Regex: defaultIdleRegex}
}

type paneState struct {
	hash         string
	unchangedAt  time.Time
	attentionRaisedForHash string
}

// Inspect takes the latest pane text and a stored state, returns the
// updated state plus whether a new attention item should be raised.
func (d *IdleDetector) Inspect(text string, prev paneState, now time.Time) (paneState, bool) {
	hash := hashText(text)
	cur := prev
	if hash != prev.hash {
		cur = paneState{hash: hash, unchangedAt: now}
		return cur, false
	}
	// Unchanged — check idle threshold + prompt tail.
	if cur.unchangedAt.IsZero() {
		cur.unchangedAt = now
	}
	if now.Sub(cur.unchangedAt) < d.Threshold {
		return cur, false
	}
	tail := tailLines(text, 5)
	if !d.Regex.MatchString(tail) {
		return cur, false
	}
	if cur.attentionRaisedForHash == hash {
		return cur, false
	}
	cur.attentionRaisedForHash = hash
	return cur, true
}

func hashText(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
