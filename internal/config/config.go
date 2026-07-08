// Package config loads runtime configuration from a YAML file. Secrets are kept
// out of the file: any ${VAR} in the YAML is expanded from the process
// environment at load time, so tokens live in env vars, not on disk.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level runtime configuration.
type Config struct {
	Server ServerConfig `yaml:"server"`
	GitHub GitHubConfig `yaml:"github"`
	Codex  CodexConfig  `yaml:"codex"`
	GitLab GitLabConfig `yaml:"gitlab"`
	PR     PRConfig     `yaml:"pr"`
	Worker WorkerConfig `yaml:"worker"`
}

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	Addr string `yaml:"addr"` // e.g. ":8080"
	Path string `yaml:"path"` // webhook path, e.g. "/webhook"
}

// GitHubConfig controls webhook verification and which events are processed.
type GitHubConfig struct {
	// WebhookSecret is the shared secret configured on the GitHub webhook.
	// Used to verify the X-Hub-Signature-256 header. Provide via ${GITHUB_WEBHOOK_SECRET}.
	WebhookSecret string `yaml:"webhook_secret"`

	// Token is a GitHub token (repo scope) exported to the Codex process as
	// GH_TOKEN/GITHUB_TOKEN so `gh` can post PR reviews and fetch PR diffs.
	// Required when the pull_request event is enabled. Provide via ${GH_TOKEN}.
	Token string `yaml:"token"`

	// Events maps a GitHub event name (the X-GitHub-Event header value, e.g.
	// "issues", "pull_request", "push", "workflow_run", "check_run") to a filter.
	// An event with no entry here, or with Enabled=false, is ignored.
	Events map[string]EventFilter `yaml:"events"`
}

// EventFilter decides whether a given delivery of an event type is processed.
type EventFilter struct {
	Enabled bool `yaml:"enabled"`
	// Actions, when non-empty, restricts processing to these payload "action"
	// values (e.g. issues: ["opened", "reopened"]). Empty means "any action".
	Actions []string `yaml:"actions"`
	// Conclusions, when non-empty, restricts processing to these CI conclusions
	// for workflow_run / check_run (e.g. ["failure", "timed_out"]). This is what
	// turns a generic CI webhook into a "only react to failures" trigger.
	Conclusions []string `yaml:"conclusions"`
}

// CodexConfig controls how Codex is invoked. Codex runs as an MCP server
// (`codex mcp`, stdio JSON-RPC); this agent is the MCP client and drives one
// triage per incident by calling Codex's tool.
type CodexConfig struct {
	Bin     string        `yaml:"bin"`     // path to the codex binary
	Model   string        `yaml:"model"`   // optional; injected as the "model" tool argument
	Workdir string        `yaml:"workdir"` // working dir for the codex mcp process
	Timeout time.Duration `yaml:"timeout"` // per-invocation timeout
	MCP     MCPConfig     `yaml:"mcp"`     // MCP transport / tool-call shape
}

// MCPConfig describes how to launch and call the Codex MCP server. The tool
// name and argument keys are configurable so the agent can track Codex CLI
// versions whose tool schema differs, without code changes.
type MCPConfig struct {
	// Args launches Codex as a stdio MCP server, e.g. ["mcp"].
	Args []string `yaml:"args"`
	// ToolName is the MCP tool to call (Codex exposes "codex").
	ToolName string `yaml:"tool_name"`
	// PromptKey is the tool argument the rendered prompt is placed into.
	PromptKey string `yaml:"prompt_key"`
	// SystemKey is the tool argument the embedded operating guide (AGENTS.md) is
	// placed into. Default "developer-instructions": the Codex tool injects it as
	// a developer-role message layered on top of Codex's own base instructions
	// (unlike "base-instructions", which would replace them). This passes the
	// guide via the call instead of relying on Codex reading AGENTS.md from the
	// working directory. Empty disables it.
	SystemKey string `yaml:"system_key"`
	// Arguments are static tool-call arguments merged into every call
	// (e.g. sandbox: danger-full-access, approval-policy: never).
	Arguments map[string]any `yaml:"arguments"`
}

// GitLabConfig is passed through to glab (which Codex calls). Host and Token are
// exported into the Codex process environment as GITLAB_HOST / GITLAB_TOKEN.
type GitLabConfig struct {
	Host    string `yaml:"host"`    // internal GitLab base URL, e.g. https://gitlab.internal
	Token   string `yaml:"token"`   // personal/project access token; provide via ${GITLAB_TOKEN}
	Project string `yaml:"project"` // target project path for issues, e.g. "team/incidents"
}

// PRConfig controls the pull_request playbooks: reviewing GitHub PRs with gh and
// mirroring/merging them as GitLab merge requests via glab + git. These values
// are surfaced to Codex through the prompt; Codex runs the actual commands.
type PRConfig struct {
	// ReviewMode is how gh posts the review. Currently "comment" (leave a review
	// comment without approving or blocking). Other values are advisory to Codex.
	ReviewMode string `yaml:"review_mode"`
	// GitLabProject is the GitLab project path that mirrored MRs live in,
	// e.g. "team/web-mirror".
	GitLabProject string `yaml:"gitlab_project"`
	// GitLabRepoURL is the git URL Codex pushes PR branches to (the mirror repo).
	// e.g. "https://gitlab.internal.corp/team/web-mirror.git". Codex injects the
	// token for auth; do not embed credentials here.
	GitLabRepoURL string `yaml:"gitlab_repo_url"`
	// TargetBranch is the base branch for mirrored MRs / merge sync. Default "main".
	TargetBranch string `yaml:"target_branch"`
}

// WorkerConfig controls the async processing pool. Webhooks are acknowledged
// immediately and processed here, because a Codex run takes far longer than
// GitHub's ~10s webhook timeout.
type WorkerConfig struct {
	Concurrency int `yaml:"concurrency"` // number of worker goroutines
	QueueSize   int `yaml:"queue_size"`  // bounded channel capacity
}

// Load reads, env-expands, parses, and validates the config file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	// Expand ${VAR} references against the environment so secrets stay in env.
	expanded := os.ExpandEnv(string(raw))

	cfg := Default()
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Default returns a Config pre-populated with sane defaults; fields present in
// the YAML override these.
func Default() *Config {
	return &Config{
		Server: ServerConfig{Addr: ":8080", Path: "/webhook"},
		PR:     PRConfig{ReviewMode: "comment", TargetBranch: "main"},
		Codex: CodexConfig{
			Bin:     "codex",
			Workdir: os.TempDir(),
			Timeout: 5 * time.Minute,
			MCP: MCPConfig{
				Args:      []string{"mcp"},
				ToolName:  "codex",
				PromptKey: "prompt",
				SystemKey: "developer-instructions",
			},
		},
		Worker: WorkerConfig{Concurrency: 2, QueueSize: 100},
	}
}

func (c *Config) validate() error {
	if c.GitHub.WebhookSecret == "" {
		return fmt.Errorf("github.webhook_secret is required (set GITHUB_WEBHOOK_SECRET)")
	}
	if c.GitLab.Project == "" {
		return fmt.Errorf("gitlab.project is required")
	}
	if c.GitLab.Host == "" {
		return fmt.Errorf("gitlab.host is required")
	}
	if c.Worker.Concurrency < 1 {
		c.Worker.Concurrency = 1
	}
	if c.Worker.QueueSize < 1 {
		c.Worker.QueueSize = 1
	}
	// Backstop MCP defaults in case a partial mcp: block cleared them.
	if len(c.Codex.MCP.Args) == 0 {
		c.Codex.MCP.Args = []string{"mcp"}
	}
	if c.Codex.MCP.ToolName == "" {
		c.Codex.MCP.ToolName = "codex"
	}
	if c.Codex.MCP.PromptKey == "" {
		c.Codex.MCP.PromptKey = "prompt"
	}
	if c.PR.ReviewMode == "" {
		c.PR.ReviewMode = "comment"
	}
	if c.PR.TargetBranch == "" {
		c.PR.TargetBranch = "main"
	}
	// The pull_request playbooks need gh auth and a mirror target.
	if f, ok := c.GitHub.Events["pull_request"]; ok && f.Enabled {
		if c.GitHub.Token == "" {
			return fmt.Errorf("github.token is required when pull_request is enabled (set GH_TOKEN)")
		}
		if c.PR.GitLabProject == "" || c.PR.GitLabRepoURL == "" {
			return fmt.Errorf("pr.gitlab_project and pr.gitlab_repo_url are required when pull_request is enabled")
		}
	}
	return nil
}
