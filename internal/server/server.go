// Package server wires the HTTP webhook endpoint to the worker pool.
package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/github"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/worker"
)

// maxBody caps the webhook body we will read (GitHub payloads are well under
// this; the cap bounds memory from hostile requests).
const maxBody = 8 << 20 // 8 MiB

type Server struct {
	cfg  *config.Config
	pool *worker.Pool
	log  *slog.Logger
	http *http.Server
}

func New(cfg *config.Config, pool *worker.Pool, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, pool: pool, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST "+cfg.Server.Path, s.handleWebhook)
	s.http = &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

func (s *Server) ListenAndServe() error { return s.http.ListenAndServe() }

func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	// Verify the signature over the raw body before trusting anything in it.
	sig := r.Header.Get(github.SignatureHeader)
	if err := github.VerifySignature(s.cfg.GitHub.WebhookSecret, body, sig); err != nil {
		s.log.Warn("rejected webhook: bad signature", "remote", r.RemoteAddr)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if eventType == "ping" {
		// GitHub sends a ping on webhook creation; acknowledge it.
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "pong")
		return
	}

	decision, err := github.Evaluate(s.cfg, eventType, deliveryID, body)
	if err != nil {
		s.log.Error("evaluate failed", "event", eventType, "delivery", deliveryID, "err", err)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if !decision.Process {
		s.log.Info("event skipped", "event", eventType, "delivery", deliveryID, "reason", decision.Reason)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "skipped: "+decision.Reason)
		return
	}

	if !s.pool.Submit(decision.Incident) {
		s.log.Error("queue full, dropping", "delivery", deliveryID)
		http.Error(w, "queue full", http.StatusServiceUnavailable)
		return
	}

	s.log.Info("accepted for triage", "event", eventType, "delivery", deliveryID, "repo", decision.Incident.Repo)
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, "accepted")
}

// ErrServerClosed is re-exported so callers can detect a clean shutdown without
// importing net/http.
var ErrServerClosed = http.ErrServerClosed
