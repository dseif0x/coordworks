// Package agent implements the agent brain: drafting plans for tasks and
// executing approved plans step by step, with the ability to delegate new
// tasks to teammates.
//
// The engine is deliberately runner-agnostic: it receives everything it needs
// in a JobBundle and reports all side effects through an Emitter, so the same
// code runs inside the control plane (embedded runner) and on remote runners
// (bare metal, Docker, Kubernetes) that talk back over HTTP.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dseif0x/coordworks/internal/domain"
	"github.com/dseif0x/coordworks/internal/llm"
)

// Teammate is the slice of an agent's team visible to prompts.
type Teammate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// JobBundle carries the full context a runner needs to execute one job.
type JobBundle struct {
	Job       domain.Job         `json:"job"`
	Task      domain.Task        `json:"task"`
	Agent     domain.Agent       `json:"agent"`
	Provider  llm.ProviderConfig `json:"provider"`
	Plan      *domain.Plan       `json:"plan,omitempty"`       // execute jobs
	PriorPlan *domain.Plan       `json:"prior_plan,omitempty"` // replans: last plan with human feedback
	TeamName  string             `json:"team_name"`
	Teammates []Teammate         `json:"teammates"`
}

// Emitter receives side effects while a job runs. Implementations are bound
// to the job's context (task/agent/plan ids).
type Emitter interface {
	Activity(kind, content string)
	StepUpdate(stepIdx int, status, output string)
	// Delegate asks the control plane to create a task for a teammate.
	Delegate(title, description, assigneeID string)
	Usage(in, out int64)
}

// PlanDraft is the result of a plan job.
type PlanDraft struct {
	Summary string   `json:"summary"`
	Steps   []string `json:"steps"`
}

// ExecResult is the result of an execute job.
type ExecResult struct {
	Success bool   `json:"success"`
	Result  string `json:"result"`
}

const maxStepOutputChars = 4000

// GeneratePlan asks the agent's model to draft a plan for the task.
func GeneratePlan(ctx context.Context, client llm.Client, b *JobBundle, em Emitter) (*PlanDraft, error) {
	em.Activity(domain.ActAgentMessage, fmt.Sprintf("%s is drafting a plan for %q", b.Agent.Name, b.Task.Title))

	var sb strings.Builder
	fmt.Fprintf(&sb, "Task: %s\n", b.Task.Title)
	if b.Task.Description != "" {
		fmt.Fprintf(&sb, "Details: %s\n", b.Task.Description)
	}
	fmt.Fprintf(&sb, "Priority: %s\n", b.Task.Priority)
	if b.PriorPlan != nil {
		sb.WriteString("\nYour previous plan was NOT approved. Previous plan:\n")
		for i, st := range b.PriorPlan.Steps {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, st.Description)
		}
		if b.PriorPlan.Feedback != "" {
			fmt.Fprintf(&sb, "Human feedback you MUST address: %s\n", b.PriorPlan.Feedback)
		}
		sb.WriteString("Draft a revised plan that addresses the feedback.\n")
	}
	sb.WriteString(`
Draft a concise, actionable plan to complete this task. Between 2 and 7 steps.
Each step must be a concrete action you can perform by reasoning and writing
(research, draft, analyze, review, summarize, delegate).

Respond with ONLY a JSON object in exactly this shape:
{"summary": "<one-sentence plan summary>", "steps": ["<step 1>", "<step 2>", ...]}`)

	req := llm.Request{
		Model:  b.Agent.Model,
		System: identityPrompt(b) + "\nYou are in PLANNING mode. Plans require human approval before execution, so be clear and specific.",
		Messages: []llm.Message{
			{Role: "user", Content: sb.String()},
		},
	}
	var draft PlanDraft
	if err := completeJSON(ctx, client, req, em, &draft); err != nil {
		return nil, err
	}
	if len(draft.Steps) == 0 {
		return nil, fmt.Errorf("model returned a plan with no steps")
	}
	if draft.Summary == "" {
		draft.Summary = draft.Steps[0]
	}
	em.Activity(domain.ActPlanProposed, fmt.Sprintf("%s proposes: %s (%d steps)", b.Agent.Name, draft.Summary, len(draft.Steps)))
	return &draft, nil
}

// stepReply is the JSON the model returns for each executed step.
type stepReply struct {
	Output      string `json:"output"`
	Status      string `json:"status"` // done | failed
	Delegations []struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Assignee    string `json:"assignee"` // teammate name or id
	} `json:"delegations"`
}

// ExecutePlan runs an approved plan step by step, emitting progress and
// delegations, and returns the final task result.
func ExecutePlan(ctx context.Context, client llm.Client, b *JobBundle, em Emitter) (*ExecResult, error) {
	if b.Plan == nil {
		return nil, fmt.Errorf("execute job without plan")
	}
	plan := b.Plan
	em.Activity(domain.ActAgentMessage, fmt.Sprintf("%s started executing the approved plan (v%d)", b.Agent.Name, plan.Version))

	system := identityPrompt(b) + `
You are in EXECUTION mode, carrying out an approved plan one step at a time.
You work by reasoning and writing: research notes, drafts, analyses, decisions.
You may delegate follow-up work to teammates listed above by adding a
delegation; delegated tasks go through their own plan/approval cycle.

For every step respond with ONLY a JSON object in exactly this shape:
{"output": "<the concrete work product / result of this step>",
 "status": "done" or "failed",
 "delegations": [{"title": "...", "description": "...", "assignee": "<teammate name>"}]}
Use an empty delegations array when there is nothing to hand off.`

	// The conversation accumulates so later steps see earlier outputs.
	messages := []llm.Message{{Role: "user", Content: executionContext(b)}}
	failures := 0

	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.Status == domain.StepDone || step.Status == domain.StepSkipped {
			continue // resumed job: skip already-finished steps
		}
		em.StepUpdate(i, domain.StepRunning, "")
		em.Activity(domain.ActStepStarted, fmt.Sprintf("Step %d/%d: %s", i+1, len(plan.Steps), step.Description))

		messages = append(messages, llm.Message{
			Role:    "user",
			Content: fmt.Sprintf("Execute step %d of %d now: %s", i+1, len(plan.Steps), step.Description),
		})
		req := llm.Request{Model: b.Agent.Model, System: system, Messages: messages}
		var reply stepReply
		if err := completeJSON(ctx, client, req, em, &reply); err != nil {
			em.StepUpdate(i, domain.StepFailed, err.Error())
			em.Activity(domain.ActStepFailed, fmt.Sprintf("Step %d failed: %v", i+1, err))
			return &ExecResult{Success: false, Result: fmt.Sprintf("failed at step %d: %v", i+1, err)}, nil
		}

		output := truncateText(reply.Output, maxStepOutputChars)
		// Feed the assistant's own (truncated) answer back for continuity.
		assistantJSON, _ := json.Marshal(map[string]any{"output": output, "status": reply.Status})
		messages = append(messages, llm.Message{Role: "assistant", Content: string(assistantJSON)})

		for _, d := range reply.Delegations {
			if strings.TrimSpace(d.Title) == "" {
				continue
			}
			assigneeID := resolveTeammate(b, d.Assignee)
			em.Delegate(d.Title, d.Description, assigneeID)
			em.Activity(domain.ActTaskCreated, fmt.Sprintf("%s delegated %q to %s", b.Agent.Name, d.Title, teammateName(b, assigneeID)))
		}

		if strings.EqualFold(reply.Status, "failed") {
			failures++
			em.StepUpdate(i, domain.StepFailed, output)
			em.Activity(domain.ActStepFailed, fmt.Sprintf("Step %d/%d reported failure", i+1, len(plan.Steps)))
			// Keep going: later steps may still salvage the task, and the
			// final summary reports what failed.
			continue
		}
		em.StepUpdate(i, domain.StepDone, output)
		em.Activity(domain.ActStepCompleted, fmt.Sprintf("Step %d/%d completed", i+1, len(plan.Steps)))
	}

	// Final wrap-up: one more call to produce the task result.
	messages = append(messages, llm.Message{
		Role: "user",
		Content: `All steps are finished. Summarize the outcome for your human manager.
Respond with ONLY a JSON object: {"result": "<final deliverable / outcome summary>", "success": true or false}`,
	})
	var wrap struct {
		Result  string `json:"result"`
		Success bool   `json:"success"`
	}
	req := llm.Request{Model: b.Agent.Model, System: system, Messages: messages}
	if err := completeJSON(ctx, client, req, em, &wrap); err != nil {
		return nil, err
	}
	success := wrap.Success && failures == 0
	return &ExecResult{Success: success, Result: wrap.Result}, nil
}

// identityPrompt builds the shared persona header.
func identityPrompt(b *JobBundle) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "You are %s, working as %s", b.Agent.Name, orDefault(b.Agent.Role, "a generalist"))
	if b.TeamName != "" {
		fmt.Fprintf(&sb, " on the %s team", b.TeamName)
	}
	sb.WriteString(" in an AI-run company orchestrated by CoordWorks.\n")
	if b.Agent.Persona != "" {
		fmt.Fprintf(&sb, "Your persona and standing instructions:\n%s\n", b.Agent.Persona)
	}
	if len(b.Teammates) > 0 {
		sb.WriteString("Your teammates (you may delegate tasks to them):\n")
		for _, t := range b.Teammates {
			fmt.Fprintf(&sb, "- %s (%s)\n", t.Name, orDefault(t.Role, "generalist"))
		}
	}
	return sb.String()
}

func executionContext(b *JobBundle) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Task: %s\n", b.Task.Title)
	if b.Task.Description != "" {
		fmt.Fprintf(&sb, "Details: %s\n", b.Task.Description)
	}
	fmt.Fprintf(&sb, "\nApproved plan: %s\n", b.Plan.Summary)
	for i, st := range b.Plan.Steps {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, st.Description)
	}
	if b.Plan.Feedback != "" {
		fmt.Fprintf(&sb, "\nInstructions from your human manager (follow them closely): %s\n", b.Plan.Feedback)
	}
	return sb.String()
}

// completeJSON calls the model, tracks usage, and unmarshals a JSON object
// from the reply, retrying once with a stricter instruction on parse failure.
func completeJSON(ctx context.Context, client llm.Client, req llm.Request, em Emitter, out any) error {
	resp, err := client.Complete(ctx, req)
	if err != nil {
		return err
	}
	em.Usage(resp.Usage.InputTokens, resp.Usage.OutputTokens)
	if jsonErr := unmarshalLoose(resp.Text, out); jsonErr == nil {
		return nil
	}
	retry := req
	retry.Messages = append(append([]llm.Message{}, req.Messages...),
		llm.Message{Role: "assistant", Content: resp.Text},
		llm.Message{Role: "user", Content: "Your reply was not valid JSON. Respond again with ONLY the JSON object, no prose, no code fences."})
	resp2, err := client.Complete(ctx, retry)
	if err != nil {
		return err
	}
	em.Usage(resp2.Usage.InputTokens, resp2.Usage.OutputTokens)
	if err := unmarshalLoose(resp2.Text, out); err != nil {
		return fmt.Errorf("model did not return valid JSON: %w", err)
	}
	return nil
}

// unmarshalLoose extracts the outermost JSON object from text that may be
// wrapped in prose or markdown fences.
func unmarshalLoose(text string, out any) error {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return fmt.Errorf("no JSON object found in reply")
	}
	return json.Unmarshal([]byte(text[start:end+1]), out)
}

// resolveTeammate maps a name/id the model mentioned to a teammate id;
// empty string means "let the control plane pick / leave on team backlog".
func resolveTeammate(b *JobBundle, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	for _, t := range b.Teammates {
		if strings.EqualFold(t.ID, ref) || strings.EqualFold(t.Name, ref) {
			return t.ID
		}
	}
	// Fuzzy: first-name match ("Avery" for "Avery Chen").
	lower := strings.ToLower(ref)
	for _, t := range b.Teammates {
		if strings.HasPrefix(strings.ToLower(t.Name), lower) {
			return t.ID
		}
	}
	return ""
}

func teammateName(b *JobBundle, id string) string {
	for _, t := range b.Teammates {
		if t.ID == id {
			return t.Name
		}
	}
	return "the team backlog"
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n[...truncated]"
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
