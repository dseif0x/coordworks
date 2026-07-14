package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dseif0x/coordworks/internal/agent"
)

// RemoteConfig configures a runner process connecting to a control plane.
type RemoteConfig struct {
	ServerURL string
	Token     string
	Name      string
	Labels    map[string]string
}

// Remote is a runner that talks to the control plane over HTTP.
type Remote struct {
	cfg      RemoteConfig
	hc       *http.Client
	runnerID string
	log      *slog.Logger
}

func NewRemote(cfg RemoteConfig, log *slog.Logger) *Remote {
	cfg.ServerURL = strings.TrimSuffix(cfg.ServerURL, "/")
	return &Remote{cfg: cfg, hc: &http.Client{Timeout: 10 * time.Minute}, log: log}
}

// Run registers and then loops forever claiming and executing jobs,
// re-registering if the server loses track of us.
func (r *Remote) Run(ctx context.Context) error {
	for {
		if err := r.register(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.log.Error("register failed, retrying", "err", err)
			if !sleepCtx(ctx, 5*time.Second) {
				return ctx.Err()
			}
			continue
		}
		r.log.Info("registered with control plane", "runner_id", r.runnerID, "labels", r.cfg.Labels)
		if err := r.loop(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.log.Error("runner loop error, re-registering", "err", err)
			if !sleepCtx(ctx, 5*time.Second) {
				return ctx.Err()
			}
		}
	}
}

func (r *Remote) loop(ctx context.Context) error {
	// Heartbeat runs independently so a long-running job or long-poll claim
	// never lets the server think we went offline.
	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				if err := r.post(hbCtx, fmt.Sprintf("/api/runner/%s/heartbeat", r.runnerID), nil, nil); err != nil {
					r.log.Warn("heartbeat failed", "err", err)
				}
			}
		}
	}()

	consecutiveErrs := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		bundle, err := r.claim(ctx)
		if err != nil {
			consecutiveErrs++
			if consecutiveErrs >= 5 {
				return fmt.Errorf("claim failing repeatedly: %w", err)
			}
			if !sleepCtx(ctx, 3*time.Second) {
				return ctx.Err()
			}
			continue
		}
		consecutiveErrs = 0
		if bundle == nil {
			continue // long-poll timed out with no work
		}
		r.log.Info("claimed job", "job", bundle.Job.ID, "kind", bundle.Job.Kind, "task", bundle.Task.Title, "agent", bundle.Agent.Name)
		em := &remoteEmitter{r: r, jobID: bundle.Job.ID, ctx: ctx}
		completion := ExecuteJob(ctx, bundle, em)
		if err := r.complete(ctx, bundle.Job.ID, completion); err != nil {
			r.log.Error("failed to report completion", "job", bundle.Job.ID, "err", err)
		} else {
			r.log.Info("job finished", "job", bundle.Job.ID, "status", completion.Status)
		}
	}
}

func (r *Remote) register(ctx context.Context) error {
	var resp RegisterResponse
	err := r.post(ctx, "/api/runner/register", RegisterRequest{Name: r.cfg.Name, Labels: r.cfg.Labels}, &resp)
	if err != nil {
		return err
	}
	r.runnerID = resp.RunnerID
	return nil
}

// claim long-polls the server for a job. nil bundle = nothing available.
func (r *Remote) claim(ctx context.Context) (*agent.JobBundle, error) {
	body, _ := json.Marshal(map[string]any{})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.cfg.ServerURL+fmt.Sprintf("/api/runner/%s/claim", r.runnerID), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Runner-Token", r.cfg.Token)
	resp, err := r.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		var bundle agent.JobBundle
		if err := json.Unmarshal(data, &bundle); err != nil {
			return nil, err
		}
		return &bundle, nil
	case http.StatusNoContent:
		return nil, nil
	case http.StatusNotFound:
		return nil, errors.New("runner unknown to server")
	default:
		return nil, fmt.Errorf("claim: status %d: %s", resp.StatusCode, string(data))
	}
}

func (r *Remote) complete(ctx context.Context, jobID string, c *Completion) error {
	// Retry a few times: losing a completion strands the job until requeue.
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if err = r.post(ctx, "/api/runner/jobs/"+jobID+"/complete", c, nil); err == nil {
			return nil
		}
		if !sleepCtx(ctx, time.Duration(1<<attempt)*time.Second) {
			return ctx.Err()
		}
	}
	return err
}

func (r *Remote) post(ctx context.Context, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.ServerURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Runner-Token", r.cfg.Token)
	resp, err := r.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: status %d: %s", path, resp.StatusCode, string(data))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// remoteEmitter forwards engine side effects to the control plane as they
// happen so the UI updates live.
type remoteEmitter struct {
	r     *Remote
	jobID string
	ctx   context.Context
}

func (e *remoteEmitter) send(ev Event) {
	if err := e.r.post(e.ctx, "/api/runner/jobs/"+e.jobID+"/events", []Event{ev}, nil); err != nil {
		e.r.log.Warn("failed to send event", "job", e.jobID, "type", ev.Type, "err", err)
	}
}

func (e *remoteEmitter) Activity(kind, content string) {
	e.send(Event{Type: "activity", Kind: kind, Content: content})
}

func (e *remoteEmitter) StepUpdate(stepIdx int, status, output string) {
	e.send(Event{Type: "step", StepIdx: stepIdx, StepStatus: status, Output: output})
}

func (e *remoteEmitter) Delegate(title, description, assigneeID string) {
	e.send(Event{Type: "delegate", Title: title, Description: description, AssigneeID: assigneeID})
}

func (e *remoteEmitter) Usage(in, out int64) {
	e.send(Event{Type: "usage", TokensIn: in, TokensOut: out})
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
