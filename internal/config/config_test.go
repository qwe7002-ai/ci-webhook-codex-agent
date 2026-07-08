package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes a minimal valid config (workdir under dir) and returns its
// path. The webhook secret is intentionally omitted so the resolver runs.
func writeConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	body := "" +
		"codex:\n" +
		"  workdir: " + dir + "\n" +
		"gitlab:\n" +
		"  host: https://gitlab.example\n" +
		"  project: team/incidents\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadGeneratesAndPersistsWebhookSecret(t *testing.T) {
	tmp := t.TempDir()
	path := writeConfig(t, tmp)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GitHub.WebhookSecret == "" {
		t.Fatal("expected an auto-generated webhook secret")
	}
	secretFile := filepath.Join(tmp, webhookSecretFile)
	if cfg.WebhookSecretPath != secretFile {
		t.Fatalf("WebhookSecretPath = %q, want %q", cfg.WebhookSecretPath, secretFile)
	}
	if _, err := os.Stat(secretFile); err != nil {
		t.Fatalf("secret file not persisted: %v", err)
	}

	// A second load reuses the same secret rather than generating a new one.
	cfg2, err := Load(path)
	if err != nil {
		t.Fatalf("Load (2): %v", err)
	}
	if cfg2.GitHub.WebhookSecret != cfg.GitHub.WebhookSecret {
		t.Fatalf("secret changed across loads: %q != %q", cfg2.GitHub.WebhookSecret, cfg.GitHub.WebhookSecret)
	}
}

func TestLoadEnvSecretWins(t *testing.T) {
	tmp := t.TempDir()
	path := writeConfig(t, tmp)
	// Simulate ${GITHUB_WEBHOOK_SECRET} having expanded to a real value.
	if err := os.WriteFile(path, []byte(
		"github:\n  webhook_secret: pinned-secret\n"+
			"codex:\n  workdir: "+tmp+"\n"+
			"gitlab:\n  host: https://gitlab.example\n  project: team/incidents\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GitHub.WebhookSecret != "pinned-secret" {
		t.Fatalf("WebhookSecret = %q, want pinned-secret", cfg.GitHub.WebhookSecret)
	}
	if cfg.WebhookSecretPath != "" {
		t.Fatalf("WebhookSecretPath = %q, want empty (no file used)", cfg.WebhookSecretPath)
	}
	if _, err := os.Stat(filepath.Join(tmp, webhookSecretFile)); !os.IsNotExist(err) {
		t.Fatalf("secret file should not be created when pinned via env")
	}
}
