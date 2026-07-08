// Package codex drives Codex over MCP: it launches `codex mcp` (stdio JSON-RPC),
// calls the Codex tool with the triage prompt, and parses the final message.
// Codex, in that session, does the evaluation and creates the GitLab issue via
// the glab CLI.
package codex

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	codexskill "github.com/qwe7002-ai/ci-webhook-codex-agent/codex"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/github"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/mcp"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/prompt"
)

// Runner runs Codex for incidents. It is safe for concurrent use: each Run
// launches its own isolated `codex mcp` process.
type Runner struct {
	cfg *config.Config
	log *slog.Logger
}

func New(cfg *config.Config, log *slog.Logger) *Runner { return &Runner{cfg: cfg, log: log} }

// Result is the parsed outcome of a Codex run.
type Result struct {
	IssueURL string // parsed from an "ISSUE_URL:" line (triage playbook)
	MRURL    string // parsed from an "MR_URL:" line (PR playbooks)
	Summary  string // parsed from a "SUMMARY:" line, if present
	Output   string // full final message, for logging/debugging
}

// Link returns whichever URL the playbook produced, for logging.
func (r *Result) Link() string {
	if r.IssueURL != "" {
		return r.IssueURL
	}
	return r.MRURL
}

// Run renders the prompt, opens an MCP session to Codex, calls the tool, and
// parses the result. The GitLab host/token are injected into the `codex mcp`
// child environment so glab (called by Codex) authenticates without a login.
func (r *Runner) Run(ctx context.Context, inc github.Incident) (*Result, error) {
	promptText, err := prompt.Render(inc, r.cfg)
	if err != nil {
		return nil, fmt.Errorf("render prompt: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, r.cfg.Codex.Timeout)
	defer cancel()

	mc := r.cfg.Codex.MCP
	client, err := mcp.Start(ctx, r.cfg.Codex.Bin, mc.Args, r.childEnv(), r.cfg.Codex.Workdir, r.log)
	if err != nil {
		return nil, fmt.Errorf("start codex mcp: %w", err)
	}
	defer client.Close()

	args := r.toolArgs(promptText)
	res, err := client.CallTool(ctx, mc.ToolName, args)
	if err != nil {
		return nil, fmt.Errorf("codex tool call: %w", err)
	}

	out := res.Text()
	result := &Result{Output: out}
	result.IssueURL = extractField(out, "ISSUE_URL:")
	result.MRURL = extractField(out, "MR_URL:")
	result.Summary = extractField(out, "SUMMARY:")

	if res.IsError {
		return result, fmt.Errorf("codex tool returned error: %s", truncate(out, 2000))
	}
	if errLine := extractField(out, "ERROR:"); errLine != "" && result.Link() == "" {
		return result, fmt.Errorf("codex reported error: %s", errLine)
	}
	return result, nil
}

// toolArgs builds the tool-call arguments: the configured static arguments,
// plus the prompt (under the configured key) and optional model override.
func (r *Runner) toolArgs(promptText string) map[string]any {
	args := make(map[string]any, len(r.cfg.Codex.MCP.Arguments)+3)
	for k, v := range r.cfg.Codex.MCP.Arguments {
		args[k] = v
	}
	args[r.cfg.Codex.MCP.PromptKey] = promptText
	// Pass the operating guide (AGENTS.md) as the call's developer instructions
	// instead of relying on Codex reading it from the working directory. If a
	// workdir AGENTS.md exists, Codex still reads it on top of this.
	if key := r.cfg.Codex.MCP.SystemKey; key != "" {
		args[key] = codexskill.AgentsMD
	}
	if r.cfg.Codex.Model != "" {
		args["model"] = r.cfg.Codex.Model
	}
	return args
}

// childEnv builds the environment for the Codex process: inherit the parent's,
// then overlay GitLab connection vars consumed by glab.
func (r *Runner) childEnv() []string {
	env := os.Environ()
	if r.cfg.GitLab.Host != "" {
		env = append(env, "GITLAB_HOST="+r.cfg.GitLab.Host)
	}
	if r.cfg.GitLab.Token != "" {
		env = append(env, "GITLAB_TOKEN="+r.cfg.GitLab.Token)
	}
	// gh (PR review/fetch) reads GH_TOKEN or GITHUB_TOKEN; set both.
	if r.cfg.GitHub.Token != "" {
		env = append(env, "GH_TOKEN="+r.cfg.GitHub.Token, "GITHUB_TOKEN="+r.cfg.GitHub.Token)
	}
	return env
}

// extractField returns the trimmed remainder of the last line beginning with
// prefix, or "" if none. Last-wins so a concluding line overrides earlier noise.
func extractField(out, prefix string) string {
	var val string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			val = strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return val
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
