// Package codex invokes the Codex CLI in non-interactive ("exec") mode to run
// the triage and create the GitLab issue via glab.
package codex

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/github"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/prompt"
)

// Runner runs Codex for incidents. It is safe for concurrent use.
type Runner struct {
	cfg *config.Config
}

func New(cfg *config.Config) *Runner { return &Runner{cfg: cfg} }

// Result is the parsed outcome of a Codex run.
type Result struct {
	IssueURL string // parsed from an "ISSUE_URL:" line, if present
	Summary  string // parsed from a "SUMMARY:" line, if present
	Stdout   string // full stdout, for logging/debugging
}

// Run renders the prompt, invokes the Codex CLI with a timeout, and returns the
// parsed result. The GitLab host/token are injected into the child environment
// so glab (called by Codex) can talk to the internal instance without an
// interactive login.
func (r *Runner) Run(ctx context.Context, inc github.Incident) (*Result, error) {
	promptText, err := prompt.Render(inc, r.cfg.GitLab.Project)
	if err != nil {
		return nil, fmt.Errorf("render prompt: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, r.cfg.Codex.Timeout)
	defer cancel()

	args := append([]string{}, r.cfg.Codex.Args...)
	if r.cfg.Codex.Model != "" {
		args = append(args, "-m", r.cfg.Codex.Model)
	}
	// The prompt is passed on stdin so we never hit argv length limits or need
	// to shell-escape multi-line, attacker-influenced payload text.
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, r.cfg.Codex.Bin, args...)
	cmd.Dir = r.cfg.Codex.Workdir
	cmd.Stdin = strings.NewReader(promptText)
	cmd.Env = r.childEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codex exec failed: %w\nstderr:\n%s", err, truncate(stderr.String(), 4000))
	}

	out := stdout.String()
	res := &Result{Stdout: out}
	res.IssueURL = extractField(out, "ISSUE_URL:")
	res.Summary = extractField(out, "SUMMARY:")
	if errLine := extractField(out, "ERROR:"); errLine != "" && res.IssueURL == "" {
		return res, fmt.Errorf("codex reported error: %s", errLine)
	}
	return res, nil
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
