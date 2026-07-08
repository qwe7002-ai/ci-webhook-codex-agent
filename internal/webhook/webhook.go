// Package webhook registers this agent's GitHub webhook on a repository. It
// drives the GitHub REST API through the `gh` CLI, reusing gh's own stored
// credentials (`gh auth login`) so no token is handled here — the same design
// as the rest of the agent. Setup is idempotent: an existing hook pointing at
// the same delivery URL is updated in place rather than duplicated.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
)

// hookPayload is the request body for creating/updating a repository webhook.
type hookPayload struct {
	Name   string     `json:"name,omitempty"` // "web" only on create
	Active bool       `json:"active"`
	Events []string   `json:"events"`
	Config hookConfig `json:"config"`
}

type hookConfig struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Secret      string `json:"secret,omitempty"`
	InsecureSSL string `json:"insecure_ssl"`
}

// existingHook is the subset of GitHub's hook object we need to match by URL.
type existingHook struct {
	ID     int64 `json:"id"`
	Config struct {
		URL string `json:"url"`
	} `json:"config"`
}

// EnabledEvents returns the GitHub event names the config acts on, sorted, so
// the webhook is subscribed to exactly what the agent will process.
func EnabledEvents(cfg *config.Config) []string {
	evs := make([]string, 0, len(cfg.GitHub.Events))
	for name, f := range cfg.GitHub.Events {
		if f.Enabled {
			evs = append(evs, name)
		}
	}
	sort.Strings(evs)
	return evs
}

// Setup registers (or updates) repo's webhook so GitHub delivers the enabled
// events to url, signed with the resolved webhook secret. It requires `gh` to
// be installed and authenticated with admin access to repo.
func Setup(ctx context.Context, cfg *config.Config, repo, url string, log *slog.Logger) error {
	if repo == "" || url == "" {
		return fmt.Errorf("both -repo (owner/repo) and -webhook-url are required")
	}
	if cfg.GitHub.WebhookSecret == "" {
		return fmt.Errorf("no webhook secret resolved") // resolveWebhookSecret should have set one
	}
	events := EnabledEvents(cfg)
	if len(events) == 0 {
		return fmt.Errorf("no enabled events in config.github.events — nothing to subscribe to")
	}

	id, err := findHook(ctx, repo, url)
	if err != nil {
		return err
	}

	body := hookPayload{
		Active: true,
		Events: events,
		Config: hookConfig{
			URL:         url,
			ContentType: "json",
			Secret:      cfg.GitHub.WebhookSecret,
			InsecureSSL: "0",
		},
	}

	var method, path string
	if id == 0 {
		body.Name = "web"
		method, path = "POST", "repos/"+repo+"/hooks"
	} else {
		method, path = "PATCH", fmt.Sprintf("repos/%s/hooks/%d", repo, id)
	}
	if err := ghAPIWithBody(ctx, method, path, body); err != nil {
		return err
	}

	if id == 0 {
		log.Info("created webhook", "repo", repo, "url", url, "events", events)
	} else {
		log.Info("updated webhook", "repo", repo, "url", url, "events", events, "hook_id", id)
	}
	return nil
}

// findHook returns the id of the repo hook whose delivery URL equals url, or 0
// if none exists.
func findHook(ctx context.Context, repo, url string) (int64, error) {
	out, err := ghAPI(ctx, nil, "--method", "GET", "repos/"+repo+"/hooks", "--paginate")
	if err != nil {
		return 0, err
	}
	var hooks []existingHook
	if err := json.Unmarshal(out, &hooks); err != nil {
		return 0, fmt.Errorf("parse existing hooks: %w", err)
	}
	for _, h := range hooks {
		if h.Config.URL == url {
			return h.ID, nil
		}
	}
	return 0, nil
}

// ghAPIWithBody sends body as JSON to a gh api endpoint via stdin (--input -),
// which keeps the secret out of the process argument list.
func ghAPIWithBody(ctx context.Context, method, path string, body hookPayload) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal hook payload: %w", err)
	}
	_, err = ghAPI(ctx, payload, "--method", method, path, "--input", "-")
	return err
}

// ghAPI runs `gh api <args...>` with optional stdin, returning stdout. On
// failure the returned error carries gh's stderr.
func ghAPI(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", append([]string{"api"}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh api %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}
