// Package config loads runtime configuration from a YAML file. Secrets are kept
// out of the file: any ${VAR} in the YAML is expanded from the process
// environment at load time. The only secret the agent itself handles is the
// webhook secret; gh/glab authenticate from their own CLI config.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// webhookSecretFile is the basename, under codex.workdir, of the auto-managed
// webhook secret. It is created on first run when no secret was supplied and
// reused on every subsequent run (and by -setup-webhook), so both sides agree.
const webhookSecretFile = ".webhook-secret"

// Config is the top-level runtime configuration.
type Config struct {
	Server ServerConfig `yaml:"server"`
	GitHub GitHubConfig `yaml:"github"`
	Codex  CodexConfig  `yaml:"codex"`
	GitLab GitLabConfig `yaml:"gitlab"`
	Worker WorkerConfig `yaml:"worker"`

	// WebhookSecretPath is the file the webhook secret was auto-loaded from or
	// written to, or "" when the secret came from the environment. Set by Load;
	// not read from YAML. Callers may log it so operators can find the secret.
	WebhookSecretPath string `yaml:"-"`
}

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	Addr string `yaml:"addr"` // e.g. ":8080"
	Path string `yaml:"path"` // webhook path, e.g. "/webhook"
	// PublicURL is the externally reachable URL of this service, used by
	// -setup-webhook as the webhook's delivery target. It may be just the base
	// (scheme+host, e.g. https://ci.example.com) — Path is appended — or the full
	// endpoint URL. Provide via ${PUBLIC_URL}. Empty unless -setup-webhook is used.
	PublicURL string `yaml:"public_url"`
}

// GitHubConfig controls webhook verification and which events are processed.
type GitHubConfig struct {
	// WebhookSecret is the shared secret used to verify the X-Hub-Signature-256
	// header. Provide it via ${GITHUB_WEBHOOK_SECRET} to pin a specific value;
	// if left empty the agent generates one on first run and persists it under
	// codex.workdir (see Load), so operators need not manage it by hand.
	WebhookSecret string `yaml:"webhook_secret"`

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

// GitLabConfig is passed through to glab (which Codex calls). Host is exported
// into the Codex process environment as GITLAB_HOST so glab targets the right
// instance for repo-less commands; glab supplies its own credentials from
// `glab auth login`, so no token lives here.
type GitLabConfig struct {
	Host    string `yaml:"host"`    // internal GitLab base URL, e.g. https://gitlab.internal
	Project string `yaml:"project"` // target project path for issues, e.g. "team/incidents"
}

// The pull_request playbooks (PR review + GitLab mirror/merge) take all their
// per-repo parameters — the target GitLab project, the mirror git URL, and the
// MR target branch — from projects.toml, which Codex reads at runtime. None of
// that lives in this config anymore.

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
	if err := cfg.resolveWebhookSecret(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// resolveWebhookSecret ensures GitHub.WebhookSecret is set. An explicit value
// (from ${GITHUB_WEBHOOK_SECRET}) always wins. Otherwise it loads a persisted
// secret from codex.workdir/.webhook-secret, generating and saving a random one
// the first time so the operator never has to invent or track it. The same file
// is reused on later runs and by -setup-webhook, keeping GitHub and the agent in
// sync automatically.
func (c *Config) resolveWebhookSecret() error {
	if c.GitHub.WebhookSecret != "" {
		return nil // pinned via the environment; leave it alone.
	}
	dir := c.Codex.Workdir
	if dir == "" {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, webhookSecretFile)
	c.WebhookSecretPath = path

	switch b, err := os.ReadFile(path); {
	case err == nil:
		if s := strings.TrimSpace(string(b)); s != "" {
			c.GitHub.WebhookSecret = s
			return nil
		}
		// Empty/corrupt file: fall through and regenerate.
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("read webhook secret %s: %w", path, err)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("generate webhook secret: %w", err)
	}
	secret := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return fmt.Errorf("write webhook secret %s: %w", path, err)
	}
	c.GitHub.WebhookSecret = secret
	return nil
}

// Default returns a Config pre-populated with sane defaults; fields present in
// the YAML override these.
func Default() *Config {
	return &Config{
		Server: ServerConfig{Addr: ":8080", Path: "/webhook"},
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
	// github.webhook_secret is not required here: resolveWebhookSecret (run after
	// validation) auto-generates and persists one when it is empty.
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
	// The pull_request playbooks resolve their GitLab project, mirror URL, and
	// target branch from projects.toml at runtime (see AGENTS.md), so there is
	// nothing PR-specific to validate here.
	return nil
}
