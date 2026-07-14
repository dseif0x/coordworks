package server

import (
	"context"
	"os"
	"time"

	"github.com/dseif0x/coordworks/internal/domain"
	"github.com/dseif0x/coordworks/internal/runner"
	"github.com/dseif0x/coordworks/internal/store"
)

// The embedded runner executes jobs inside the control plane process so a
// single binary is a fully working system. Remote runners take priority is
// not needed — jobs are claimed first-come-first-served, and agents pinned to
// specific runners via selectors are never claimed by the embedded runner
// unless its labels match.
const embeddedRunnerID = "embedded"

// runEmbeddedRunner registers the in-process runner and loops claiming jobs.
func (s *Server) runEmbeddedRunner(ctx context.Context) {
	host, _ := os.Hostname()
	rn := &domain.Runner{
		ID:       embeddedRunnerID,
		Name:     "embedded (" + host + ")",
		Labels:   map[string]string{"runtime": "embedded", "host": host},
		Embedded: true,
	}
	if err := s.store.UpsertRunner(ctx, rn); err != nil {
		s.log.Error("embedded runner registration failed", "err", err)
		return
	}
	s.log.Info("embedded runner started")

	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	poll := time.NewTicker(1 * time.Second)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			_ = s.store.TouchRunner(ctx, embeddedRunnerID)
		case <-poll.C:
			s.claimAndRunEmbedded(ctx, rn)
		}
	}
}

func (s *Server) claimAndRunEmbedded(ctx context.Context, rn *domain.Runner) {
	job, err := s.store.ClaimJob(ctx, rn.ID, rn.Labels)
	if err != nil {
		if err != store.ErrNotFound {
			s.log.Error("embedded claim failed", "err", err)
		}
		return
	}
	bundle, err := s.orch.buildBundle(ctx, job)
	if err != nil {
		_ = s.orch.failJob(ctx, job, "cannot build job bundle: "+err.Error())
		return
	}
	s.orch.onJobClaimed(ctx, job)
	s.log.Info("embedded runner claimed job", "job", job.ID, "kind", job.Kind, "agent", bundle.Agent.Name)

	// Run async so one long LLM job doesn't block claiming further jobs.
	go func() {
		em := &embeddedEmitter{orch: s.orch, job: job, ctx: ctx}
		completion := runner.ExecuteJob(ctx, bundle, em)
		if err := s.orch.completeJob(ctx, job, completion); err != nil {
			s.log.Error("embedded completion failed", "job", job.ID, "err", err)
		}
	}()
}

// embeddedEmitter applies engine side effects directly through the
// orchestrator — same semantics as events arriving over the runner API.
type embeddedEmitter struct {
	orch *orchestrator
	job  *domain.Job
	ctx  context.Context
}

func (e *embeddedEmitter) apply(ev runner.Event) {
	if err := e.orch.applyEvent(e.ctx, e.job, ev); err != nil {
		// Losing a UI event is non-fatal; log via activity table is skipped.
		_ = err
	}
}

func (e *embeddedEmitter) Activity(kind, content string) {
	e.apply(runner.Event{Type: "activity", Kind: kind, Content: content})
}

func (e *embeddedEmitter) StepUpdate(stepIdx int, status, output string) {
	e.apply(runner.Event{Type: "step", StepIdx: stepIdx, StepStatus: status, Output: output})
}

func (e *embeddedEmitter) Delegate(title, description, assigneeID string) {
	e.apply(runner.Event{Type: "delegate", Title: title, Description: description, AssigneeID: assigneeID})
}

func (e *embeddedEmitter) Usage(in, out int64) {
	e.apply(runner.Event{Type: "usage", TokensIn: in, TokensOut: out})
}
