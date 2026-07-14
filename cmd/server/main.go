// coordworks-server is the CoordWorks control plane: API, UI, approval
// workflow, job queue, and (optionally) an embedded runner.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dseif0x/coordworks/internal/server"
	"github.com/dseif0x/coordworks/internal/store"
)

func main() {
	var cfg server.Config
	var dbPath string
	flag.StringVar(&cfg.Addr, "addr", envOr("COORDWORKS_ADDR", ":8080"), "listen address")
	flag.StringVar(&dbPath, "db", envOr("COORDWORKS_DB", "coordworks.db"), "SQLite database path")
	flag.StringVar(&cfg.RunnerToken, "runner-token", envOr("COORDWORKS_RUNNER_TOKEN", ""), "shared secret for runner registration (required)")
	flag.StringVar(&cfg.APIToken, "api-token", envOr("COORDWORKS_API_TOKEN", ""), "optional bearer token protecting the UI API")
	flag.StringVar(&cfg.StaticDir, "static", envOr("COORDWORKS_STATIC_DIR", "web/dist"), "built frontend directory ('' disables)")
	flag.BoolVar(&cfg.EmbeddedRunner, "embedded-runner", envOr("COORDWORKS_EMBEDDED_RUNNER", "true") == "true", "run an in-process runner")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if cfg.RunnerToken == "" {
		log.Error("a runner token is required: set --runner-token or COORDWORKS_RUNNER_TOKEN")
		os.Exit(1)
	}
	if _, err := os.Stat(cfg.StaticDir); cfg.StaticDir != "" && os.IsNotExist(err) {
		log.Warn("static dir not found, serving API only", "dir", cfg.StaticDir)
		cfg.StaticDir = ""
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.New(cfg, st, log).Run(ctx); err != nil {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
