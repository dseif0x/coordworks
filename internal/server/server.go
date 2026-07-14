// Package server is the CoordWorks control plane: REST + WebSocket API for
// the UI, the runner protocol for distributed execution, and an optional
// embedded runner.
package server

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dseif0x/coordworks/internal/store"
)

// Config for the control plane.
type Config struct {
	Addr           string
	RunnerToken    string // shared secret runners use to authenticate
	APIToken       string // optional bearer token protecting the UI API
	StaticDir      string // built frontend; empty = API only
	EmbeddedRunner bool
}

type Server struct {
	cfg   Config
	store *store.Store
	hub   *Hub
	orch  *orchestrator
	log   *slog.Logger
}

func New(cfg Config, st *store.Store, log *slog.Logger) *Server {
	hub := NewHub(log)
	return &Server{
		cfg:   cfg,
		store: st,
		hub:   hub,
		orch:  &orchestrator{store: st, hub: hub},
		log:   log,
	}
}

// Run starts background loops and serves HTTP until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	if s.cfg.EmbeddedRunner {
		go s.runEmbeddedRunner(ctx)
	}
	go s.requeueLoop(ctx)

	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	s.log.Info("control plane listening", "addr", s.cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// requeueLoop returns jobs from dead runners to the queue.
func (s *Server) requeueLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			requeued, failed, err := s.store.RequeueStaleJobs(ctx, maxJobAttempts)
			if err != nil {
				s.log.Error("requeue stale jobs failed", "err", err)
				continue
			}
			for _, id := range requeued {
				s.log.Warn("job requeued after runner went offline", "job", id)
			}
			for _, id := range failed {
				if job, err := s.store.GetJob(ctx, id); err == nil {
					_ = s.orch.failJob(ctx, job, "runner went offline repeatedly")
				}
			}
			if len(requeued)+len(failed) > 0 {
				s.hub.Broadcast("runner.updated", nil)
			}
		}
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// UI/API surface (optionally bearer-token protected).
	api := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requireAPIToken(h))
	}

	api("GET /api/stats", s.handleStats)
	api("GET /api/activity", s.handleListActivity)

	api("GET /api/providers", s.handleListProviders)
	api("POST /api/providers", s.handleCreateProvider)
	api("PUT /api/providers/{id}", s.handleUpdateProvider)
	api("DELETE /api/providers/{id}", s.handleDeleteProvider)
	api("POST /api/providers/{id}/test", s.handleTestProvider)

	api("GET /api/teams", s.handleListTeams)
	api("POST /api/teams", s.handleCreateTeam)
	api("PUT /api/teams/{id}", s.handleUpdateTeam)
	api("DELETE /api/teams/{id}", s.handleDeleteTeam)

	api("GET /api/agents", s.handleListAgents)
	api("POST /api/agents", s.handleCreateAgent)
	api("GET /api/agents/{id}", s.handleGetAgent)
	api("PUT /api/agents/{id}", s.handleUpdateAgent)
	api("DELETE /api/agents/{id}", s.handleDeleteAgent)

	api("GET /api/tasks", s.handleListTasks)
	api("POST /api/tasks", s.handleCreateTask)
	api("GET /api/tasks/{id}", s.handleGetTask)
	api("POST /api/tasks/{id}/assign", s.handleAssignTask)
	api("DELETE /api/tasks/{id}", s.handleDeleteTask)

	api("GET /api/approvals", s.handleListApprovals)
	api("POST /api/plans/{id}/{decision}", s.handlePlanDecision)

	api("GET /api/runners", s.handleListRunners)
	api("DELETE /api/runners/{id}", s.handleDeleteRunner)
	api("GET /api/jobs", s.handleListJobs)

	// Live events. Token (when set) is checked via query param because
	// browsers can't set WS headers.
	mux.HandleFunc("GET /api/ws", func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIToken != "" {
			token := r.URL.Query().Get("token")
			if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.APIToken)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		s.hub.HandleWS(w, r)
	})

	// Runner protocol (shared-token protected).
	mux.HandleFunc("POST /api/runner/register", s.requireRunnerToken(s.handleRunnerRegister))
	mux.HandleFunc("POST /api/runner/{id}/heartbeat", s.requireRunnerToken(s.handleRunnerHeartbeat))
	mux.HandleFunc("POST /api/runner/{id}/claim", s.requireRunnerToken(s.handleRunnerClaim))
	mux.HandleFunc("POST /api/runner/jobs/{jobID}/events", s.requireRunnerToken(s.handleRunnerEvents))
	mux.HandleFunc("POST /api/runner/jobs/{jobID}/complete", s.requireRunnerToken(s.handleRunnerComplete))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Static frontend with SPA fallback.
	if s.cfg.StaticDir != "" {
		mux.Handle("/", spaHandler(s.cfg.StaticDir))
	}

	return withCORS(mux)
}

// requireAPIToken enforces the optional UI API bearer token.
func (s *Server) requireAPIToken(next http.HandlerFunc) http.HandlerFunc {
	if s.cfg.APIToken == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(auth), []byte(s.cfg.APIToken)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

// withCORS allows the Vite dev server (different port) to call the API.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Runner-Token")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// spaHandler serves the built frontend, falling back to index.html for
// client-side routes.
func spaHandler(dir string) http.Handler {
	fileServer := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}
