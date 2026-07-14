// coordworks-runner is a distributed execution node. Point it at a control
// plane and it claims agent jobs matching its labels — run it on bare metal,
// in Docker, or as a Kubernetes deployment; scale by starting more replicas.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dseif0x/coordworks/internal/runner"
)

func main() {
	var cfg runner.RemoteConfig
	var labelsFlag string
	flag.StringVar(&cfg.ServerURL, "server", envOr("COORDWORKS_SERVER_URL", "http://localhost:8080"), "control plane URL")
	flag.StringVar(&cfg.Token, "token", envOr("COORDWORKS_RUNNER_TOKEN", ""), "runner token (required)")
	flag.StringVar(&cfg.Name, "name", envOr("COORDWORKS_RUNNER_NAME", ""), "runner name (defaults to hostname)")
	flag.StringVar(&labelsFlag, "labels", envOr("COORDWORKS_RUNNER_LABELS", ""), "labels, e.g. runtime=docker,gpu=true")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if cfg.Token == "" {
		log.Error("a runner token is required: set --token or COORDWORKS_RUNNER_TOKEN")
		os.Exit(1)
	}
	if cfg.Name == "" {
		host, _ := os.Hostname()
		cfg.Name = host
	}
	cfg.Labels = parseLabels(labelsFlag)
	if _, ok := cfg.Labels["runtime"]; !ok {
		cfg.Labels["runtime"] = "baremetal"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runner.NewRemote(cfg, log).Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("runner exited", "err", err)
		os.Exit(1)
	}
}

func parseLabels(s string) map[string]string {
	labels := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(pair), "="); ok {
			labels[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return labels
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
