// Package runner contains the wire protocol between the control plane and
// execution runners, plus the job executor and the remote runner loop.
//
// Distribution model: runners are stateless workers. They register with the
// control plane using a shared token, long-poll to claim jobs that match
// their labels, execute them with the agent engine (all LLM calls happen on
// the runner), and stream side effects back over HTTP. The same runner binary
// runs on bare metal, in Docker, or on Kubernetes.
package runner

import (
	"context"
	"fmt"

	"github.com/dseif0x/coordworks/internal/agent"
	"github.com/dseif0x/coordworks/internal/domain"
	"github.com/dseif0x/coordworks/internal/llm"
)

// Event is one side effect emitted while a job runs.
type Event struct {
	Type string `json:"type"` // activity | step | delegate | usage

	// activity
	Kind    string `json:"kind,omitempty"`
	Content string `json:"content,omitempty"`

	// step
	StepIdx    int    `json:"step_idx,omitempty"`
	StepStatus string `json:"step_status,omitempty"`
	Output     string `json:"output,omitempty"`

	// delegate
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	AssigneeID  string `json:"assignee_id,omitempty"`

	// usage
	TokensIn  int64 `json:"tokens_in,omitempty"`
	TokensOut int64 `json:"tokens_out,omitempty"`
}

// Completion is the terminal report for a job.
type Completion struct {
	Status string            `json:"status"` // done | failed
	Error  string            `json:"error,omitempty"`
	Plan   *agent.PlanDraft  `json:"plan,omitempty"`
	Result *agent.ExecResult `json:"result,omitempty"`
}

// RegisterRequest / RegisterResponse handshake a runner with the server.
type RegisterRequest struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type RegisterResponse struct {
	RunnerID string `json:"runner_id"`
}

// ExecuteJob runs one job bundle to completion using the agent engine.
// It never returns an error: failures are folded into the Completion so the
// control plane always learns the outcome.
func ExecuteJob(ctx context.Context, b *agent.JobBundle, em agent.Emitter) *Completion {
	client, err := llm.New(b.Provider)
	if err != nil {
		return &Completion{Status: domain.JobFailed, Error: fmt.Sprintf("init LLM client: %v", err)}
	}
	switch b.Job.Kind {
	case domain.JobPlan:
		draft, err := agent.GeneratePlan(ctx, client, b, em)
		if err != nil {
			return &Completion{Status: domain.JobFailed, Error: err.Error()}
		}
		return &Completion{Status: domain.JobDone, Plan: draft}
	case domain.JobExecute:
		res, err := agent.ExecutePlan(ctx, client, b, em)
		if err != nil {
			return &Completion{Status: domain.JobFailed, Error: err.Error()}
		}
		return &Completion{Status: domain.JobDone, Result: res}
	default:
		return &Completion{Status: domain.JobFailed, Error: fmt.Sprintf("unknown job kind %q", b.Job.Kind)}
	}
}
