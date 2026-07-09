// Command server is the webhook agent: it receives GitHub webhooks, filters and
// normalizes them, and hands each qualifying event to Codex, which does an
// initial triage and opens an issue on the internal GitLab via glab.
//
// With no subcommand it runs the HTTP server. The `setup-webhook` subcommand
// registers the GitHub webhook on a repo (via gh) and exits.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/codex"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/server"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/webhook"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/worker"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cmd := &cli.Command{
		Name:  "ci-webhook-codex-agent",
		Usage: "receive GitHub webhooks and drive Codex triage / PR mirroring",
		Flags: []cli.Flag{
			&cli.StringFlag{
				// A root flag is persistent by default (available to subcommands),
				// and the env source + default mean the path rarely needs to be
				// passed on the command line.
				Name:    "config",
				Aliases: []string{"c"},
				Value:   "config.yaml",
				Sources: cli.EnvVars("CI_WEBHOOK_CONFIG"),
				Usage:   "path to the YAML config file",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := loadConfig(cmd, log)
			if err != nil {
				return err
			}
			return runServer(ctx, cfg, log)
		},
		Commands: []*cli.Command{
			{
				Name:  "setup-webhook",
				Usage: "register the GitHub webhook on a repo via gh, then exit",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "repo",
						Aliases: []string{"r"},
						Sources: cli.EnvVars("GITHUB_REPO"),
						Usage:   "owner/repo to register the webhook on",
					},
					&cli.StringFlag{
						Name:    "webhook-url",
						Aliases: []string{"u"},
						Usage:   "override the delivery URL (default: server.public_url + server.path)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := loadConfig(cmd, log)
					if err != nil {
						return err
					}
					url := cmd.String("webhook-url")
					if url == "" {
						url = webhook.EndpointURL(cfg) // from server.public_url + server.path
					}
					ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
					defer cancel()
					return webhook.Setup(ctx, cfg, cmd.String("repo"), url, log)
				},
			},
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// loadConfig loads and validates the config named by the --config flag, logging
// where the auto-managed webhook secret lives.
func loadConfig(cmd *cli.Command, log *slog.Logger) (*config.Config, error) {
	cfg, err := config.Load(cmd.String("config"))
	if err != nil {
		return nil, err
	}
	if cfg.WebhookSecretPath != "" {
		log.Info("webhook secret auto-managed", "path", cfg.WebhookSecretPath)
	}
	return cfg, nil
}

// runServer starts the worker pool and HTTP listener, blocking until a signal
// triggers a graceful shutdown.
func runServer(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	runner := codex.New(cfg, log)
	pool := worker.New(cfg, runner, log)

	// Root context cancelled on SIGINT/SIGTERM; workers stop accepting new runs.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool.Start(ctx, cfg.Worker.Concurrency)

	srv := server.New(cfg, pool, log)
	go func() {
		log.Info("listening", "addr", cfg.Server.Addr, "path", cfg.Server.Path)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, server.ErrServerClosed) {
			log.Error("http server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	// Stop accepting new requests, then drain in-flight triage jobs.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "err", err)
	}
	pool.Stop()
	log.Info("bye")
	return nil
}
