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
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/worker"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	runner := codex.New(cfg)
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
