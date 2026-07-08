// Package worker runs incident processing off the webhook request path, so the
// HTTP handler can acknowledge GitHub within its short timeout while a Codex run
// (which can take minutes) proceeds in the background.
package worker

import (
	"context"
	"log/slog"
	"sync"

	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/codex"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/config"
	"github.com/qwe7002-ai/ci-webhook-codex-agent/internal/github"
)

// Pool is a bounded queue fed by the webhook handler and drained by a fixed set
// of workers.
type Pool struct {
	jobs   chan github.Incident
	runner *codex.Runner
	log    *slog.Logger
	wg     sync.WaitGroup

	dedup *dedup
}

func New(cfg *config.Config, runner *codex.Runner, log *slog.Logger) *Pool {
	return &Pool{
		jobs:   make(chan github.Incident, cfg.Worker.QueueSize),
		runner: runner,
		log:    log,
		dedup:  newDedup(1024),
	}
}

// Start launches n worker goroutines. Call Stop to drain and shut down.
func (p *Pool) Start(ctx context.Context, n int) {
	for i := 0; i < n; i++ {
		p.wg.Add(1)
		go p.work(ctx, i)
	}
}

// Submit enqueues an incident. It returns false if the queue is full (the
// caller should surface backpressure rather than block the request).
func (p *Pool) Submit(inc github.Incident) bool {
	if p.dedup.seen(inc.DeliveryID) {
		p.log.Info("skip duplicate delivery", "delivery", inc.DeliveryID)
		return true // acknowledged: it's a redelivery, not an error
	}
	select {
	case p.jobs <- inc:
		return true
	default:
		return false
	}
}

// Stop closes the queue and waits for in-flight work to finish.
func (p *Pool) Stop() {
	close(p.jobs)
	p.wg.Wait()
}

func (p *Pool) work(ctx context.Context, id int) {
	defer p.wg.Done()
	for inc := range p.jobs {
		log := p.log.With("worker", id, "delivery", inc.DeliveryID,
			"event", inc.EventType, "repo", inc.Repo, "playbook", inc.Playbook)
		log.Info("processing incident", "title", inc.Title)

		res, err := p.runner.Run(ctx, inc)
		if err != nil {
			log.Error("codex run failed", "err", err)
			continue
		}
		log.Info("playbook complete", "link", res.Link(), "summary", res.Summary)
	}
}

// dedup is a small fixed-capacity set of recently seen delivery IDs. GitHub may
// redeliver a webhook; this prevents creating duplicate GitLab issues within the
// window of the last `cap` deliveries.
type dedup struct {
	mu    sync.Mutex
	set   map[string]struct{}
	order []string
	cap   int
}

func newDedup(capacity int) *dedup {
	return &dedup{set: make(map[string]struct{}, capacity), cap: capacity}
}

func (d *dedup) seen(id string) bool {
	if id == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.set[id]; ok {
		return true
	}
	d.set[id] = struct{}{}
	d.order = append(d.order, id)
	if len(d.order) > d.cap {
		old := d.order[0]
		d.order = d.order[1:]
		delete(d.set, old)
	}
	return false
}
