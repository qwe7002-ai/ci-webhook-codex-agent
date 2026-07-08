// Command server is the webhook agent: it receives GitHub webhooks, filters and
// normalizes them, and hands each qualifying event to Codex, which does an
// initial triage and opens an issue on the internal GitLab via glab.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/codex"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/server"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/webhook"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/worker"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	setupWebhook := flag.Bool("setup-webhook", false, "register the GitHub webhook via gh for -repo, then exit")
	repo := flag.String("repo", "", "owner/repo to register the webhook on (with -setup-webhook)")
	webhookURL := flag.String("webhook-url", "", "override the webhook delivery URL (defaults to server.public_url + server.path)")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}
	if cfg.WebhookSecretPath != "" {
		log.Info("webhook secret auto-managed", "path", cfg.WebhookSecretPath)
	}

	// -setup-webhook is a one-shot admin action: register the hook and exit,
	// reusing the same secret the server will verify with.
	if *setupWebhook {
		url := *webhookURL
		if url == "" {
			url = webhook.EndpointURL(cfg) // from server.public_url + server.path
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := webhook.Setup(ctx, cfg, *repo, url, log); err != nil {
			log.Error("setup webhook", "err", err)
			os.Exit(1)
		}
		return
	}

	runner := codex.New(cfg, log)
	pool := worker.New(cfg, runner, log)

	// Root context cancelled on SIGINT/SIGTERM; workers stop accepting new runs.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
}
