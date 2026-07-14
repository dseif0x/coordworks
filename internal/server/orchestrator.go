package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/dseif0x/coordworks/internal/agent"
	"github.com/dseif0x/coordworks/internal/domain"
	"github.com/dseif0x/coordworks/internal/llm"
	"github.com/dseif0x/coordworks/internal/runner"
	"github.com/dseif0x/coordworks/internal/store"
)

// maxDelegationDepth caps agent→agent task chains so a misbehaving model
// can't spawn work forever.
const maxDelegationDepth = 3

// maxJobAttempts before a job whose runner died is failed for good.
const maxJobAttempts = 3

// orchestrator owns the task/plan/job lifecycle. All state transitions go
// through here so the REST API, the runner API and the embedded runner agree.
type orchestrator struct {
	store *store.Store
	hub   *Hub
}

// startTask moves an assigned task into planning and queues a plan job.
func (o *orchestrator) startTask(ctx context.Context, task *domain.Task) error {
	if task.AgentID == "" {
		return fmt.Errorf("task has no agent assigned")
	}
	ag, err := o.store.GetAgent(ctx, task.AgentID)
	if err != nil {
		return fmt.Errorf("assigned agent: %w", err)
	}
	if err := o.store.SetTaskStatus(ctx, task.ID, domain.TaskPlanning); err != nil {
		return err
	}
	job := &domain.Job{
		Kind:     domain.JobPlan,
		TaskID:   task.ID,
		AgentID:  ag.ID,
		Selector: ag.RunnerSelector,
	}
	if err := o.store.CreateJob(ctx, job); err != nil {
		return err
	}
	o.activity(ctx, &domain.Activity{
		TaskID: task.ID, AgentID: ag.ID, Kind: domain.ActSystem,
		Content: fmt.Sprintf("Task assigned to %s for planning", ag.Name),
	})
	o.hub.Broadcast("task.updated", map[string]string{"id": task.ID})
	return nil
}

// buildBundle assembles everything a runner needs for a job.
func (o *orchestrator) buildBundle(ctx context.Context, job *domain.Job) (*agent.JobBundle, error) {
	task, err := o.store.GetTask(ctx, job.TaskID)
	if err != nil {
		return nil, fmt.Errorf("task: %w", err)
	}
	ag, err := o.store.GetAgent(ctx, job.AgentID)
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	prov, err := o.store.GetProvider(ctx, ag.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("provider for agent %s: %w", ag.Name, err)
	}
	bundle := &agent.JobBundle{
		Job:      *job,
		Task:     *task,
		Agent:    *ag,
		Provider: llm.FromProvider(prov),
	}
	if ag.TeamID != "" {
		if team, err := o.store.GetTeam(ctx, ag.TeamID); err == nil {
			bundle.TeamName = team.Name
		}
		mates, err := o.store.ListAgentsByTeam(ctx, ag.TeamID)
		if err == nil {
			for _, m := range mates {
				if m.ID == ag.ID {
					continue
				}
				bundle.Teammates = append(bundle.Teammates, agent.Teammate{ID: m.ID, Name: m.Name, Role: m.Role})
			}
		}
	}
	switch job.Kind {
	case domain.JobExecute:
		plan, err := o.store.GetPlan(ctx, job.PlanID)
		if err != nil {
			return nil, fmt.Errorf("plan: %w", err)
		}
		bundle.Plan = plan
	case domain.JobPlan:
		// Replans see the last decided plan and its human feedback.
		plans, err := o.store.ListPlansByTask(ctx, task.ID)
		if err == nil && len(plans) > 0 {
			latest := plans[0]
			if latest.Status == domain.PlanSuperseded || latest.Status == domain.PlanRejected {
				bundle.PriorPlan = latest
			}
		}
	}
	return bundle, nil
}

// onJobClaimed marks the working agent and logs.
func (o *orchestrator) onJobClaimed(ctx context.Context, job *domain.Job) {
	_ = o.store.SetAgentStatus(ctx, job.AgentID, domain.AgentWorking)
	o.hub.Broadcast("agent.updated", map[string]string{"id": job.AgentID})
}

// applyEvent handles one side effect streamed from a runner.
func (o *orchestrator) applyEvent(ctx context.Context, job *domain.Job, ev runner.Event) error {
	switch ev.Type {
	case "activity":
		o.activity(ctx, &domain.Activity{
			TaskID: job.TaskID, AgentID: job.AgentID, PlanID: job.PlanID,
			Kind: ev.Kind, Content: ev.Content,
		})
	case "usage":
		if err := o.store.AddAgentUsage(ctx, job.AgentID, ev.TokensIn, ev.TokensOut); err != nil {
			return err
		}
		o.hub.Broadcast("stats.updated", nil)
	case "step":
		if job.PlanID == "" {
			return nil
		}
		plan, err := o.store.GetPlan(ctx, job.PlanID)
		if err != nil {
			return err
		}
		if ev.StepIdx < 0 || ev.StepIdx >= len(plan.Steps) {
			return fmt.Errorf("step index %d out of range", ev.StepIdx)
		}
		plan.Steps[ev.StepIdx].Status = ev.StepStatus
		if ev.Output != "" {
			plan.Steps[ev.StepIdx].Output = ev.Output
		}
		if err := o.store.UpdatePlan(ctx, plan); err != nil {
			return err
		}
		o.hub.Broadcast("plan.updated", map[string]string{"id": plan.ID, "task_id": plan.TaskID})
	case "delegate":
		return o.delegate(ctx, job, ev)
	}
	return nil
}

// delegate creates a task on behalf of an agent (agent→team task creation).
func (o *orchestrator) delegate(ctx context.Context, job *domain.Job, ev runner.Event) error {
	depth, err := o.taskDepth(ctx, job.TaskID)
	if err != nil {
		return err
	}
	creator, err := o.store.GetAgent(ctx, job.AgentID)
	if err != nil {
		return err
	}
	if depth+1 > maxDelegationDepth {
		o.activity(ctx, &domain.Activity{
			TaskID: job.TaskID, AgentID: job.AgentID, Kind: domain.ActSystem,
			Content: fmt.Sprintf("Delegation %q blocked: max delegation depth (%d) reached", ev.Title, maxDelegationDepth),
		})
		return nil
	}
	task := &domain.Task{
		Title:        ev.Title,
		Description:  ev.Description,
		TeamID:       creator.TeamID,
		AgentID:      ev.AssigneeID,
		CreatedBy:    domain.OriginAgent,
		CreatorID:    creator.ID,
		ParentTaskID: job.TaskID,
	}
	if err := o.store.CreateTask(ctx, task); err != nil {
		return err
	}
	o.activity(ctx, &domain.Activity{
		TaskID: task.ID, AgentID: creator.ID, Kind: domain.ActTaskCreated,
		Content: fmt.Sprintf("Created by %s while working on another task", creator.Name),
	})
	o.hub.Broadcast("task.updated", map[string]string{"id": task.ID})
	// Delegated tasks with a concrete assignee start planning immediately;
	// they still go through their own human approval gate.
	if task.AgentID != "" {
		if err := o.startTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

func (o *orchestrator) taskDepth(ctx context.Context, taskID string) (int, error) {
	depth := 0
	id := taskID
	for id != "" && depth <= maxDelegationDepth+1 {
		t, err := o.store.GetTask(ctx, id)
		if err != nil {
			return depth, err
		}
		id = t.ParentTaskID
		if id != "" {
			depth++
		}
	}
	return depth, nil
}

// completeJob finalizes a job with the runner's completion report.
func (o *orchestrator) completeJob(ctx context.Context, job *domain.Job, c *runner.Completion) error {
	defer func() {
		_ = o.store.SetAgentStatus(ctx, job.AgentID, domain.AgentIdle)
		o.hub.Broadcast("agent.updated", map[string]string{"id": job.AgentID})
		o.hub.Broadcast("stats.updated", nil)
	}()

	if c.Status != domain.JobDone {
		return o.failJob(ctx, job, c.Error)
	}

	switch job.Kind {
	case domain.JobPlan:
		return o.completePlanJob(ctx, job, c)
	case domain.JobExecute:
		return o.completeExecuteJob(ctx, job, c)
	default:
		return o.failJob(ctx, job, "unknown job kind")
	}
}

func (o *orchestrator) completePlanJob(ctx context.Context, job *domain.Job, c *runner.Completion) error {
	if c.Plan == nil || len(c.Plan.Steps) == 0 {
		return o.failJob(ctx, job, "runner returned no plan")
	}
	version, err := o.store.LatestPlanVersion(ctx, job.TaskID)
	if err != nil {
		return err
	}
	steps := make([]domain.PlanStep, 0, len(c.Plan.Steps))
	for _, s := range c.Plan.Steps {
		if strings.TrimSpace(s) == "" {
			continue
		}
		steps = append(steps, domain.PlanStep{Description: s, Status: domain.StepPending})
	}
	plan := &domain.Plan{
		TaskID:  job.TaskID,
		AgentID: job.AgentID,
		Summary: c.Plan.Summary,
		Steps:   steps,
		Version: version + 1,
	}
	if err := o.store.CreatePlan(ctx, plan); err != nil {
		return err
	}
	if err := o.store.FinishJob(ctx, job.ID, domain.JobDone, ""); err != nil {
		return err
	}

	ag, err := o.store.GetAgent(ctx, job.AgentID)
	if err != nil {
		return err
	}
	if ag.Autonomy == domain.AutonomyAutonomous {
		// Autonomous agents skip the human gate.
		o.activity(ctx, &domain.Activity{
			TaskID: job.TaskID, AgentID: job.AgentID, PlanID: plan.ID, Kind: domain.ActPlanApproved,
			Content: fmt.Sprintf("Plan v%d auto-approved (%s is autonomous)", plan.Version, ag.Name),
		})
		return o.approvePlan(ctx, plan, "", nil)
	}
	if err := o.store.SetTaskStatus(ctx, job.TaskID, domain.TaskAwaitingApproval); err != nil {
		return err
	}
	o.hub.Broadcast("plan.updated", map[string]string{"id": plan.ID, "task_id": plan.TaskID})
	o.hub.Broadcast("task.updated", map[string]string{"id": job.TaskID})
	o.hub.Broadcast("approval.new", map[string]string{"plan_id": plan.ID})
	return nil
}

func (o *orchestrator) completeExecuteJob(ctx context.Context, job *domain.Job, c *runner.Completion) error {
	if c.Result == nil {
		return o.failJob(ctx, job, "runner returned no result")
	}
	if err := o.store.FinishJob(ctx, job.ID, domain.JobDone, ""); err != nil {
		return err
	}
	plan, err := o.store.GetPlan(ctx, job.PlanID)
	if err != nil {
		return err
	}
	planStatus, taskStatus, actKind := domain.PlanCompleted, domain.TaskDone, domain.ActTaskCompleted
	if !c.Result.Success {
		planStatus, taskStatus, actKind = domain.PlanFailed, domain.TaskFailed, domain.ActTaskFailed
	}
	plan.Status = planStatus
	if err := o.store.UpdatePlan(ctx, plan); err != nil {
		return err
	}
	if err := o.store.SetTaskResult(ctx, job.TaskID, taskStatus, c.Result.Result); err != nil {
		return err
	}
	if c.Result.Success {
		_ = o.store.IncrAgentTasksDone(ctx, job.AgentID)
	}
	o.activity(ctx, &domain.Activity{
		TaskID: job.TaskID, AgentID: job.AgentID, PlanID: job.PlanID, Kind: actKind,
		Content: c.Result.Result,
	})
	o.hub.Broadcast("task.updated", map[string]string{"id": job.TaskID})
	o.hub.Broadcast("plan.updated", map[string]string{"id": plan.ID, "task_id": plan.TaskID})
	return nil
}

func (o *orchestrator) failJob(ctx context.Context, job *domain.Job, msg string) error {
	if msg == "" {
		msg = "job failed"
	}
	if err := o.store.FinishJob(ctx, job.ID, domain.JobFailed, msg); err != nil {
		return err
	}
	if err := o.store.SetTaskResult(ctx, job.TaskID, domain.TaskFailed, msg); err != nil {
		return err
	}
	if job.PlanID != "" {
		if plan, err := o.store.GetPlan(ctx, job.PlanID); err == nil {
			plan.Status = domain.PlanFailed
			_ = o.store.UpdatePlan(ctx, plan)
		}
	}
	o.activity(ctx, &domain.Activity{
		TaskID: job.TaskID, AgentID: job.AgentID, PlanID: job.PlanID, Kind: domain.ActError,
		Content: msg,
	})
	o.hub.Broadcast("task.updated", map[string]string{"id": job.TaskID})
	return nil
}

// --- human-in-the-loop decisions ---

// approvePlan (optionally with amended steps and feedback) queues execution.
func (o *orchestrator) approvePlan(ctx context.Context, plan *domain.Plan, feedback string, amendedSteps []string) error {
	if len(amendedSteps) > 0 {
		steps := make([]domain.PlanStep, 0, len(amendedSteps))
		for _, s := range amendedSteps {
			if strings.TrimSpace(s) == "" {
				continue
			}
			steps = append(steps, domain.PlanStep{Description: s, Status: domain.StepPending})
		}
		if len(steps) == 0 {
			return fmt.Errorf("amended plan has no steps")
		}
		plan.Steps = steps
		o.activity(ctx, &domain.Activity{
			TaskID: plan.TaskID, AgentID: plan.AgentID, PlanID: plan.ID, Kind: domain.ActPlanAmended,
			Content: "Plan steps amended by human before approval",
		})
	}
	plan.Status = domain.PlanApproved
	plan.Feedback = feedback
	t := nowPtr()
	plan.DecidedAt = t
	if err := o.store.UpdatePlan(ctx, plan); err != nil {
		return err
	}
	if err := o.store.SetTaskStatus(ctx, plan.TaskID, domain.TaskExecuting); err != nil {
		return err
	}
	ag, err := o.store.GetAgent(ctx, plan.AgentID)
	if err != nil {
		return err
	}
	job := &domain.Job{
		Kind:     domain.JobExecute,
		TaskID:   plan.TaskID,
		PlanID:   plan.ID,
		AgentID:  plan.AgentID,
		Selector: ag.RunnerSelector,
	}
	if err := o.store.CreateJob(ctx, job); err != nil {
		return err
	}
	o.activity(ctx, &domain.Activity{
		TaskID: plan.TaskID, AgentID: plan.AgentID, PlanID: plan.ID, Kind: domain.ActPlanApproved,
		Content: approvalMessage("Plan approved", feedback),
	})
	o.hub.Broadcast("plan.updated", map[string]string{"id": plan.ID, "task_id": plan.TaskID})
	o.hub.Broadcast("task.updated", map[string]string{"id": plan.TaskID})
	return nil
}

// rejectPlan kills the task.
func (o *orchestrator) rejectPlan(ctx context.Context, plan *domain.Plan, feedback string) error {
	plan.Status = domain.PlanRejected
	plan.Feedback = feedback
	plan.DecidedAt = nowPtr()
	if err := o.store.UpdatePlan(ctx, plan); err != nil {
		return err
	}
	if err := o.store.SetTaskStatus(ctx, plan.TaskID, domain.TaskRejected); err != nil {
		return err
	}
	o.activity(ctx, &domain.Activity{
		TaskID: plan.TaskID, AgentID: plan.AgentID, PlanID: plan.ID, Kind: domain.ActPlanRejected,
		Content: approvalMessage("Plan rejected", feedback),
	})
	o.hub.Broadcast("plan.updated", map[string]string{"id": plan.ID, "task_id": plan.TaskID})
	o.hub.Broadcast("task.updated", map[string]string{"id": plan.TaskID})
	return nil
}

// requestChanges sends the plan back to the agent with feedback for a replan.
func (o *orchestrator) requestChanges(ctx context.Context, plan *domain.Plan, feedback string) error {
	if strings.TrimSpace(feedback) == "" {
		return fmt.Errorf("feedback is required when requesting changes")
	}
	plan.Status = domain.PlanSuperseded
	plan.Feedback = feedback
	plan.DecidedAt = nowPtr()
	if err := o.store.UpdatePlan(ctx, plan); err != nil {
		return err
	}
	if err := o.store.SetTaskStatus(ctx, plan.TaskID, domain.TaskPlanning); err != nil {
		return err
	}
	ag, err := o.store.GetAgent(ctx, plan.AgentID)
	if err != nil {
		return err
	}
	job := &domain.Job{
		Kind:     domain.JobPlan,
		TaskID:   plan.TaskID,
		AgentID:  plan.AgentID,
		Selector: ag.RunnerSelector,
	}
	if err := o.store.CreateJob(ctx, job); err != nil {
		return err
	}
	o.activity(ctx, &domain.Activity{
		TaskID: plan.TaskID, AgentID: plan.AgentID, PlanID: plan.ID, Kind: domain.ActPlanAmended,
		Content: approvalMessage("Changes requested, agent will replan", feedback),
	})
	o.hub.Broadcast("plan.updated", map[string]string{"id": plan.ID, "task_id": plan.TaskID})
	o.hub.Broadcast("task.updated", map[string]string{"id": plan.TaskID})
	return nil
}

func (o *orchestrator) activity(ctx context.Context, a *domain.Activity) {
	if err := o.store.AddActivity(ctx, a); err != nil {
		return
	}
	o.hub.Broadcast("activity.new", a)
}

func approvalMessage(prefix, feedback string) string {
	if strings.TrimSpace(feedback) == "" {
		return prefix
	}
	return prefix + " — " + feedback
}
