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

// CodexConfig controls how the Codex CLI is invoked.
type CodexConfig struct {
	Bin     string        `yaml:"bin"`      // path to the codex binary
	Args    []string      `yaml:"args"`     // base args, e.g. ["exec", "--skip-git-repo-check"]
	Model   string        `yaml:"model"`    // optional model override, passed as -m
	Workdir string        `yaml:"workdir"`  // working dir for the codex process
	Timeout time.Duration `yaml:"timeout"`  // per-invocation timeout
}

// GitLabConfig is passed through to glab (which Codex calls). Host and Token are
// exported into the Codex process environment as GITLAB_HOST / GITLAB_TOKEN.
type GitLabConfig struct {
	Host    string `yaml:"host"`    // internal GitLab base URL, e.g. https://gitlab.internal
	Token   string `yaml:"token"`   // personal/project access token; provide via ${GITLAB_TOKEN}
	Project string `yaml:"project"` // target project path for issues, e.g. "team/incidents"
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
		Codex: CodexConfig{
			Bin:     "codex",
			Args:    []string{"exec", "--skip-git-repo-check"},
			Workdir: os.TempDir(),
			Timeout: 5 * time.Minute,
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
	return nil
}
