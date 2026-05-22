package antigravity

import (
	"context"
	"fmt"
)

// Permission handling (ADR-035 D6) has two modes:
//
//   - Auto-approve (the MVP default, what steward.antigravity.v1.yaml
//     ships): `agy --dangerously-skip-permissions` resolves every
//     engine-side tool gate itself, and the steward gates higher-order
//     decisions via the vendor-neutral `request_approval` attention path.
//     No pane scraping is involved.
//
//   - Interactive (non-default): an operator who runs `agy` WITHOUT
//     --dangerously-skip-permissions gets agy's arrow-nav permission
//     menu in the TUI. agy's transcript records only the *resolved*
//     outcome, never the pending prompt, so the only way to observe a
//     pending decision is to scrape the pane. CapturePane is that
//     mechanism; the answer is sent back via HandleInput("pick_option")
//     (arrow-nav, sendkeys.go).
//
// The interactive *detector* — matching the captured pane text to decide
// "a permission menu is up, here are its options" — is deliberately NOT
// implemented yet: it would require pinning agy's exact menu rendering,
// which can only be verified against a live interactive prompt (W11 on
// host). Shipping a guessed marker set would violate verify-don't-guess.
// CapturePane gives that detector its input when W11 captures the real
// layout.

// CapturePane returns the visible text of a tmux pane via
// `tmux capture-pane -p -t <pane>`. runner is the CmdRunner seam (nil →
// realRunner) so tests don't need a live tmux.
func CapturePane(ctx context.Context, paneID string, runner CmdRunner) (string, error) {
	if paneID == "" {
		return "", fmt.Errorf("antigravity capturepane: empty pane id")
	}
	if runner == nil {
		runner = realRunner{}
	}
	out, err := runner.Run(ctx, "tmux", "capture-pane", "-p", "-t", paneID)
	if err != nil {
		return "", fmt.Errorf("capture-pane %s: %w", paneID, err)
	}
	return string(out), nil
}
