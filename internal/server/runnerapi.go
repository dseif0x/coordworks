package server

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/dseif0x/coordworks/internal/domain"
	"github.com/dseif0x/coordworks/internal/runner"
	"github.com/dseif0x/coordworks/internal/store"
)

// claimPollInterval / claimMaxWait implement long-polling for job claims so
// remote runners get work within a second of it being queued without hot
// loops.
const (
	claimPollInterval = 1 * time.Second
	claimMaxWait      = 25 * time.Second
)

// requireRunnerToken guards all runner endpoints with the shared token.
func (s *Server) requireRunnerToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Runner-Token")
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.RunnerToken)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid runner token"})
			return
		}
		next(w, r)
	}
}

func (s *Server) handleRunnerRegister(w http.ResponseWriter, r *http.Request) {
	in, err := decode[runner.RegisterRequest](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if in.Name == "" {
		writeErr(w, badRequest("name is required"))
		return
	}
	rn := &domain.Runner{Name: in.Name, Labels: in.Labels}
	if err := s.store.UpsertRunner(r.Context(), rn); err != nil {
		writeErr(w, err)
		return
	}
	s.hub.Broadcast("runner.updated", nil)
	writeJSON(w, http.StatusOK, runner.RegisterResponse{RunnerID: rn.ID})
}

func (s *Server) handleRunnerHeartbeat(w http.ResponseWriter, r *http.Request) {
	if err := s.store.TouchRunner(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunnerClaim long-polls for a queued job matching the runner's labels
// and responds with the full job bundle.
func (s *Server) handleRunnerClaim(w http.ResponseWriter, r *http.Request) {
	rn, err := s.store.GetRunner(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	deadline := time.Now().Add(claimMaxWait)
	for {
		_ = s.store.TouchRunner(r.Context(), rn.ID)
		job, err := s.store.ClaimJob(r.Context(), rn.ID, rn.Labels)
		if err == nil {
			bundle, err := s.orch.buildBundle(r.Context(), job)
			if err != nil {
				// Bundle can't be built (agent/provider deleted): fail the
				// job so it doesn't wedge the queue.
				_ = s.orch.failJob(r.Context(), job, "cannot build job bundle: "+err.Error())
				continue
			}
			s.orch.onJobClaimed(r.Context(), job)
			writeJSON(w, http.StatusOK, bundle)
			return
		}
		if err != store.ErrNotFound {
			writeErr(w, err)
			return
		}
		if time.Now().After(deadline) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(claimPollInterval):
		}
	}
}

// handleRunnerEvents ingests side effects streamed while a job runs.
func (s *Server) handleRunnerEvents(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.GetJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	events, err := decode[[]runner.Event](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, ev := range *events {
		if err := s.orch.applyEvent(r.Context(), job, ev); err != nil {
			s.log.Warn("apply runner event failed", "job", job.ID, "type", ev.Type, "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunnerComplete finalizes a job.
func (s *Server) handleRunnerComplete(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.GetJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if job.Status != domain.JobRunning {
		writeErr(w, badRequest("job is %s, not running", job.Status))
		return
	}
	completion, err := decode[runner.Completion](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.orch.completeJob(r.Context(), job, completion); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
