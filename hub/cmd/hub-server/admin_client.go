package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// adminCall performs an owner-authenticated request against the hub's
// /v1/admin/* surface and decodes a JSON 200 response into out (pass
// nil to ignore the body). It is the shared REST client behind the
// read-side Phase 4 CLIs (`hosts ls/ping`, `version --remote`),
// mirroring runFleetStop's --hub-url / --token / HUB_TOKEN posture.
func adminCall(method, hubURL, path, token string, body, out any) error {
	if token == "" {
		return fmt.Errorf("an owner-scope bearer is required (pass --token or set HUB_TOKEN)")
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, hubURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("hub returned %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// shortCommit trims a git revision to its 7-char prefix for table
// display; shorter / empty values pass through unchanged.
func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}
