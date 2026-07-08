// Package mcp is a minimal Model Context Protocol client over a stdio subprocess
// transport (newline-delimited JSON-RPC 2.0). It implements just enough to
// launch a server, complete the initialize handshake, and make synchronous
// tools/call requests — which is all this agent needs to drive `codex mcp`.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
)

// ProtocolVersion is the MCP revision we advertise in initialize.
const ProtocolVersion = "2025-06-18"

// Client wraps a running MCP server subprocess.
type Client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser // server stdin, closed on shutdown
	enc   *json.Encoder  // to server stdin
	dec   *json.Decoder  // from server stdout
	log   *slog.Logger

	mu     sync.Mutex // serializes requests (one in flight at a time)
	nextID int
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// envelope covers every inbound frame: response (has result/error), server
// notification (method, no id), or server->client request (method + id).
type envelope struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Method  string           `json:"method"`
	Result  json.RawMessage  `json:"result"`
	Error   *rpcError        `json:"error"`
}

// Start launches the server, wires up stdio, and performs the initialize
// handshake. The provided ctx should carry the overall deadline; if it is
// cancelled the subprocess is killed, which unblocks any pending read.
func Start(ctx context.Context, bin string, args, env []string, workdir string, log *slog.Logger) (*Client, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Dir = workdir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}

	// Drain stderr to the log so server diagnostics aren't lost or blocking.
	go forwardStderr(stderr, log)

	c := &Client{
		cmd:   cmd,
		stdin: stdin,
		enc:   json.NewEncoder(stdin),
		dec:   json.NewDecoder(stdout),
		log:   log,
	}

	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "ci-webhook-codex-agent", "version": "0.1.0"},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return fmt.Errorf("mcp initialize: %w", err)
	}
	// Per spec, follow up with the initialized notification.
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("mcp initialized notify: %w", err)
	}
	return nil
}

// CallToolResult is the subset of the MCP tools/call result we consume.
type CallToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// Text concatenates all text content blocks.
func (r *CallToolResult) Text() string {
	var s string
	for _, b := range r.Content {
		if b.Type == "text" {
			s += b.Text
		}
	}
	return s
}

// CallTool invokes a tool and returns its result. Notifications streamed by the
// server during the call (e.g. Codex progress events) are logged and skipped
// until the matching response arrives.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return nil, err
	}
	var res CallToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("decode tool result: %w", err)
	}
	return &res, nil
}

// call sends a request and reads until its response, handling interleaved
// notifications and server->client requests along the way.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	id := c.nextID
	if err := c.enc.Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return nil, fmt.Errorf("write %s: %w", method, err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var msg envelope
		if err := c.dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("server closed during %s", method)
			}
			return nil, fmt.Errorf("read during %s: %w", method, err)
		}

		switch {
		case msg.Method != "" && msg.ID != nil:
			// Server->client request. We don't implement any (e.g. sampling,
			// elicitation); decline so the server doesn't block. With
			// approval-policy=never this path should not be hit.
			c.declineRequest(msg.ID, msg.Method)
		case msg.Method != "":
			// Notification (progress/log). Best-effort visibility.
			c.log.Debug("mcp notification", "method", msg.Method)
		default:
			// Response. One request in flight, so this is ours.
			if msg.Error != nil {
				return nil, msg.Error
			}
			return msg.Result, nil
		}
	}
}

func (c *Client) declineRequest(id *json.RawMessage, method string) {
	c.log.Warn("declining unsupported server request", "method", method)
	_ = c.enc.Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": -32601, "message": "method not supported by client"},
	})
}

func (c *Client) notify(method string, params any) error {
	return c.enc.Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

// Close shuts down the server: close stdin to signal EOF, then kill and reap the
// process. We've already collected the tool result by the time we get here, so a
// hard kill is fine and avoids waiting on a server that lingers after stdin EOF.
func (c *Client) Close() error {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return nil
}

func forwardStderr(r io.Reader, log *slog.Logger) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		log.Debug("codex mcp stderr", "line", sc.Text())
	}
}
