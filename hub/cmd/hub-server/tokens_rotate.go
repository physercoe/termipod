package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
)

// runTokensRotate implements `hub-server tokens rotate` (ADR-028 plan
// W20): issue a fresh host bearer, push it to every live host via the
// host.token_rotate verb, and — once every host has confirmed — revoke
// the prior host tokens. A thin client over POST /v1/admin/tokens/rotate
// where the brick-safe orchestration lives.
func runTokensRotate(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("tokens rotate", flag.ExitOnError)
	hubURL := fs.String("hub-url", defaultHubURL(), "hub base URL (HUB_URL env)")
	token := fs.String("token", os.Getenv("HUB_TOKEN"), "owner-scope bearer token (HUB_TOKEN env)")
	forceRevoke := fs.Bool("force-revoke", false,
		"revoke the old host tokens even if a host did not ack — recovery "+
			"mode; a host that missed the rotation will need a fresh token to return")
	reason := fs.String("reason", "tokens-rotate", "audit-log reason")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	_ = fs.Parse(args)
	_ = log

	body := map[string]any{"force_revoke": *forceRevoke, "reason": *reason}
	var out struct {
		NewTokenID   string `json:"new_token_id"`
		NewToken     string `json:"new_token"`
		OldRevoked   bool   `json:"old_tokens_revoked"`
		RevokedCount int    `json:"revoked_count"`
		Note         string `json:"note"`
		Hosts        []struct {
			HostID   string `json:"host_id"`
			HostName string `json:"host_name"`
			Acked    bool   `json:"acked"`
			Error    string `json:"error"`
		} `json:"hosts"`
	}
	if err := adminCall(http.MethodPost, *hubURL, "/v1/admin/tokens/rotate",
		*token, body, &out); err != nil {
		fmt.Fprintf(os.Stderr, "tokens rotate: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("tokens rotate: issued new host token %s\n", out.NewTokenID)
		for _, h := range out.Hosts {
			if h.Acked {
				fmt.Printf("  %s (%s) — adopted the new token\n", h.HostID, h.HostName)
			} else {
				fmt.Printf("  %s (%s) — FAILED: %s\n", h.HostID, h.HostName, h.Error)
			}
		}
		if out.OldRevoked {
			fmt.Printf("old host tokens revoked: %d\n", out.RevokedCount)
		} else {
			fmt.Printf("old host tokens NOT revoked — %s\n", out.Note)
		}
		fmt.Printf("\nNew host token (store it — new hosts register with this):\n\n  %s\n\n",
			out.NewToken)
	}

	failures := 0
	for _, h := range out.Hosts {
		if !h.Acked {
			failures++
		}
	}
	if failures > 0 {
		fmt.Fprintf(os.Stderr,
			"tokens rotate: %d host(s) did not adopt the new token\n", failures)
		os.Exit(1)
	}
}
