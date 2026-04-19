// hub-mcp-bridge is a stdio ⇆ HTTP shim for MCP clients (notably the
// Claude Desktop config schema) that can only speak newline-delimited
// JSON-RPC on stdin/stdout.
//
// Protocol: each line of stdin is a full JSON-RPC request. We POST it to
// HUB_URL + "/mcp/" + HUB_TOKEN and write the response back as a single
// line on stdout. Notifications (requests with no "id") still POST but do
// not produce a stdout line, matching the JSON-RPC 2.0 spec.
//
// Config: HUB_URL (e.g. https://hub.example.com:8443) and HUB_TOKEN (the
// per-agent MCP token issued by `hub-server init`). Both required; we
// fail fast on startup rather than returning opaque transport errors to
// the MCP client later.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	var (
		hubURL  = flag.String("hub-url", os.Getenv("HUB_URL"), "Hub base URL (or HUB_URL env)")
		token   = flag.String("token", os.Getenv("HUB_TOKEN"), "MCP token for this agent (or HUB_TOKEN env)")
		timeout = flag.Duration("timeout", 30*time.Second, "per-request HTTP timeout")
	)
	flag.Parse()

	if *hubURL == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "hub-mcp-bridge: HUB_URL and HUB_TOKEN are required")
		os.Exit(2)
	}
	endpoint := strings.TrimRight(*hubURL, "/") + "/mcp/" + *token

	// stderr is the only place we can log; MCP clients treat stdout as the
	// JSON-RPC channel and a stray log line there would corrupt the stream.
	logger := log.New(os.Stderr, "hub-mcp-bridge ", log.LstdFlags|log.Lmsgprefix)

	client := &http.Client{Timeout: *timeout}
	br := bufio.NewReader(os.Stdin)
	bw := bufio.NewWriter(os.Stdout)
	defer bw.Flush()

	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if out, perr := forward(client, endpoint, line); perr != nil {
				// Transport/HTTP errors become a synthetic JSON-RPC error so
				// the MCP client sees something structured rather than EOF.
				errFrame := makeTransportError(line, perr)
				_, _ = bw.Write(errFrame)
				_ = bw.Flush()
				logger.Printf("forward error: %v", perr)
			} else if out != nil {
				if _, werr := bw.Write(out); werr != nil {
					logger.Printf("stdout write: %v", werr)
					return
				}
				if err := bw.WriteByte('\n'); err != nil {
					return
				}
				_ = bw.Flush()
			}
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			logger.Printf("stdin read: %v", err)
			return
		}
	}
}

// forward sends one JSON-RPC line to the hub and returns the response body.
// A `nil` return with nil error means "no response expected" — true for
// JSON-RPC notifications (no id field). We inspect the id before sending
// so we can still do the POST for notifications but skip the stdout write.
func forward(client *http.Client, endpoint string, line []byte) ([]byte, error) {
	line = bytes.TrimRight(line, "\r\n")
	if len(line) == 0 {
		return nil, nil
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(line))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%d %s: %s", resp.StatusCode, resp.Status, bytes.TrimSpace(body))
	}
	// Notifications ("id" absent) get a 200/204 with empty body by MCP
	// convention; if the server returned no payload we mirror that as "no
	// stdout line" so the client's read loop doesn't stall.
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	return body, nil
}

// makeTransportError produces a JSON-RPC error response keyed to the
// request's id (if parseable). Using -32000 for transport failures is the
// convention reserved by the spec for server-defined errors.
func makeTransportError(reqLine []byte, cause error) []byte {
	var req struct {
		ID json.RawMessage `json:"id,omitempty"`
	}
	_ = json.Unmarshal(reqLine, &req)
	resp := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    -32000,
			"message": "hub-mcp-bridge transport error",
			"data":    cause.Error(),
		},
	}
	if len(req.ID) > 0 {
		resp["id"] = req.ID
	}
	b, _ := json.Marshal(resp)
	return append(b, '\n')
}
