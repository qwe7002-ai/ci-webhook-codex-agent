package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// TestCallTool_EndToEnd launches a fake MCP server (this same test binary
// re-executed via TestHelperMCPServer) and drives the initialize + tools/call
// flow through the real client, including a streamed notification.
func TestCallTool_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := Start(ctx, os.Args[0], []string{"-test.run=TestHelperMCPServer"},
		append(os.Environ(), "GO_WANT_MCP_HELPER=1"), t.TempDir(), log)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	res, err := client.CallTool(ctx, "codex", map[string]any{"prompt": "triage this"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError; text=%q", res.Text())
	}
	got := res.Text()
	if !strings.Contains(got, "ISSUE_URL: https://gitlab.internal/issues/7") {
		t.Fatalf("missing issue url in %q", got)
	}
}

// TestHelperMCPServer is not a real test: when GO_WANT_MCP_HELPER is set it acts
// as a minimal MCP server on stdio.
func TestHelperMCPServer(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		t.Skip("helper process")
	}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := dec.Decode(&msg); err != nil {
			return // EOF: client closed
		}
		switch msg.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": msg.ID,
				"result": map[string]any{
					"protocolVersion": ProtocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "fake-codex", "version": "0"},
				},
			})
		case "notifications/initialized":
			// no response to notifications
		case "tools/call":
			// stream a progress notification, then the result
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/message",
				"params": map[string]any{"level": "info", "data": "working"}})
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": msg.ID,
				"result": map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": "category=ci-failure severity=S2\nISSUE_URL: https://gitlab.internal/issues/7\nSUMMARY: CI failed on main, S2.",
					}},
					"isError": false,
				},
			})
		}
	}
}
