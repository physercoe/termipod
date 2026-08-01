// Package mcpbridge is the stdio ⇆ HTTP shim for MCP clients (notably
// the Claude Desktop config schema) that can only speak newline-
// delimited JSON-RPC on stdin/stdout. Each line of stdin is a full
// JSON-RPC request; we POST it to HUB_URL + "/mcp/" + HUB_TOKEN and
// write the response back as a single line on stdout. Notifications
// (no "id") still POST but produce no stdout line per JSON-RPC 2.0.
//
// This package is reused by two binaries:
//   - the standalone `hub-mcp-bridge` command (back-compat for
//     spawns whose .mcp.json names that command directly).
//   - the `host-runner mcp-bridge` subcommand and the multicall
//     basename `hub-mcp-bridge` symlink, both shipped by host-runner
//     so a single install covers both roles.
package mcpbridge

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

	"github.com/termipod/hub/internal/mcpwire"
)

// Run executes the bridge with `args` (typically os.Args[1:] from the
// caller's main). Returns a process exit code. Reads HUB_URL / HUB_TOKEN
// from flags or env; fails fast if either is missing so MCP clients see
// a clean error rather than opaque transport silence later.
func Run(args []string) int {
	fs := flag.NewFlagSet("mcp-bridge", flag.ExitOnError)
	hubURL := fs.String("hub-url", os.Getenv("HUB_URL"), "Hub base URL (or HUB_URL env)")
	token := fs.String("token", os.Getenv("HUB_TOKEN"), "MCP token for this agent (or HUB_TOKEN env)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-request HTTP timeout")
	_ = fs.Parse(args)

	if *hubURL == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "mcp-bridge: HUB_URL and HUB_TOKEN are required")
		return 2
	}
	endpoint := strings.TrimRight(*hubURL, "/") + "/mcp/" + *token

	// stderr is the only place we can log; MCP clients treat stdout as the
	// JSON-RPC channel and a stray log line there would corrupt the stream.
	logger := log.New(os.Stderr, "mcp-bridge ", log.LstdFlags|log.Lmsgprefix)

	client := &http.Client{Timeout: *timeout}
	fwd := func(line []byte) ([]byte, error) {
		return forward(client, endpoint, line)
	}
	return runLoop(os.Stdin, os.Stdout, fwd, logger)
}

// runLoop pumps newline-delimited JSON-RPC from stdin → fwd → stdout.
// Extracted from Run so the framing is testable without spinning up
// real stdin/stdout. See TestRunLoop_NormalizesTrailingNewline for the
// codex/rmcp regression that motivated normalizing the output frame.
func runLoop(stdin io.Reader, stdout io.Writer, fwd func([]byte) ([]byte, error), logger *log.Logger) int {
	br := bufio.NewReader(stdin)
	bw := bufio.NewWriter(stdout)
	defer bw.Flush()

	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if out, perr := fwd(line); perr != nil {
				errFrame := makeTransportError(line, perr)
				_, _ = bw.Write(errFrame)
				_ = bw.Flush()
				logger.Printf("forward error: %v", perr)
			} else if out != nil {
				// Strict MCP clients (codex's rmcp) parse every stdout
				// line with serde_json and treat an empty line as a
				// fatal "EOF while parsing a value at line 1 column 0",
				// which closes the transport. The hub already terminates
				// its body with "\n", so writing body+"\n" produced an
				// empty trailing line. Normalize to exactly one trailing
				// newline.
				out = bytes.TrimRight(out, "\r\n")
				if _, werr := bw.Write(out); werr != nil {
					logger.Printf("stdout write: %v", werr)
					return 0
				}
				if err := bw.WriteByte('\n'); err != nil {
					return 0
				}
				_ = bw.Flush()
			}
		}
		if err == io.EOF {
			return 0
		}
		if err != nil {
			logger.Printf("stdin read: %v", err)
			return 1
		}
	}
}

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
	stampMCPHeaders(req, line)
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
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	return body, nil
}

// stampMCPHeaders adds the standard MCP request headers (2026-07-28
// SEP-2243) to this relay's HTTP leg: Mcp-Method for every frame, and
// Mcp-Name for a tools/call.
//
// This relay is otherwise a byte pump — it never needed to know what it
// was carrying. The headers change that on purpose: they let the hub
// classify read-vs-action, meter, and ring-audit a call WITHOUT parsing
// the body, which is the point of the spec's header routing. Parsing
// here is cheap because the frame is a single JSON-RPC object we are
// already holding.
//
// Failure is silent by design. A frame we cannot parse is still
// forwarded verbatim, unstamped: the hub is the authority on whether it
// is valid JSON-RPC, and a relay that started rejecting frames on its
// own reading of them would be a second, weaker parser in the path
// (plan §4 — the relays stay protocol-blind about everything except
// this).
func stampMCPHeaders(req *http.Request, line []byte) {
	var frame struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if err := json.Unmarshal(line, &frame); err != nil || frame.Method == "" {
		return
	}
	req.Header.Set(mcpwire.HeaderMethod, frame.Method)
	if frame.Method == "tools/call" && frame.Params.Name != "" {
		req.Header.Set(mcpwire.HeaderName, frame.Params.Name)
	}
}

func makeTransportError(reqLine []byte, cause error) []byte {
	var req struct {
		ID json.RawMessage `json:"id,omitempty"`
	}
	_ = json.Unmarshal(reqLine, &req)
	resp := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    -32000,
			"message": "mcp-bridge transport error",
			"data":    cause.Error(),
		},
	}
	if len(req.ID) > 0 {
		resp["id"] = req.ID
	}
	b, _ := json.Marshal(resp)
	return append(b, '\n')
}
