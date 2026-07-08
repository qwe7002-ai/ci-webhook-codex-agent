package codex

import (
	"strings"
	"testing"

	codexskill "github.com/qwe7002-ai/ci-webhook-codex-agent/codex"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
)

func runner(systemKey string) *Runner {
	return &Runner{cfg: &config.Config{Codex: config.CodexConfig{
		MCP: config.MCPConfig{PromptKey: "prompt", SystemKey: systemKey},
	}}}
}

func TestToolArgs_PassesAgentsMDAsSystem(t *testing.T) {
	if !strings.Contains(codexskill.AgentsMD, "triage_issue") {
		t.Fatal("embedded AGENTS.md looks empty/wrong")
	}
	args := runner("developer-instructions").toolArgs("do it")
	if args["prompt"] != "do it" {
		t.Fatalf("prompt not set: %v", args["prompt"])
	}
	if args["developer-instructions"] != codexskill.AgentsMD {
		t.Fatalf("system instructions not set to embedded AGENTS.md")
	}
}

func TestToolArgs_EmptySystemKeyOmitsIt(t *testing.T) {
	args := runner("").toolArgs("do it")
	if _, ok := args["developer-instructions"]; ok {
		t.Fatalf("no system key should be injected when system_key is empty")
	}
}
