// Package domain defines the core entities of CoordWorks: providers, teams,
// agents ("employees"), tasks, plans, jobs, runners and the activity log.
package domain

import "time"

// Provider kinds. "openai_compatible" covers any endpoint speaking the
// OpenAI chat-completions protocol (Ollama, vLLM, OpenRouter, Groq, ...).
const (
	ProviderAnthropic        = "anthropic"
	ProviderOpenAI           = "openai"
	ProviderOpenAICompatible = "openai_compatible"
)

// Provider is a configured LLM API endpoint plus credentials.
type Provider struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	BaseURL      string    `json:"base_url,omitempty"`
	APIKey       string    `json:"-"` // never serialized to clients
	HasAPIKey    bool      `json:"has_api_key"`
	Models       []string  `json:"models"`
	DefaultModel string    `json:"default_model"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Team is a department of agents.
type Team struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Color       string    `json:"color"`
	CreatedAt   time.Time `json:"created_at"`
}

// Agent autonomy levels.
const (
	AutonomyApprovalRequired = "approval_required" // every plan needs human sign-off
	AutonomyAutonomous       = "autonomous"        // plans are auto-approved
)

// Agent statuses.
const (
	AgentIdle    = "idle"
	AgentWorking = "working"
	AgentOffline = "offline"
)

// Agent is a hired AI employee: a role + persona bound to a provider/model,
// optionally pinned to a class of runners via a label selector.
type Agent struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	TeamID     string `json:"team_id"`
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	Persona    string `json:"persona"`
	Autonomy   string `json:"autonomy"`
	Status     string `json:"status"`
	// RunnerSelector pins this agent's jobs to runners whose labels contain
	// all key=value pairs, e.g. "runtime=docker,gpu=true". Empty = any runner.
	RunnerSelector string    `json:"runner_selector"`
	TokensIn       int64     `json:"tokens_in"`
	TokensOut      int64     `json:"tokens_out"`
	TasksDone      int64     `json:"tasks_done"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Task statuses form the HITL lifecycle.
const (
	TaskInbox            = "inbox"             // created, not yet planned
	TaskPlanning         = "planning"          // an agent is drafting a plan
	TaskAwaitingApproval = "awaiting_approval" // plan proposed, human decides
	TaskExecuting        = "executing"         // approved plan being executed
	TaskDone             = "done"
	TaskFailed           = "failed"
	TaskRejected         = "rejected"
)

// Task origin.
const (
	OriginUser  = "user"
	OriginAgent = "agent"
)

// Task is a unit of work assigned to an agent (directly or via a team).
type Task struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	TeamID       string    `json:"team_id"`
	AgentID      string    `json:"agent_id"`
	Status       string    `json:"status"`
	Priority     string    `json:"priority"`   // low|normal|high|urgent
	CreatedBy    string    `json:"created_by"` // user | agent
	CreatorID    string    `json:"creator_id"` // agent id when created_by=agent
	ParentTaskID string    `json:"parent_task_id"`
	Result       string    `json:"result"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Plan statuses.
const (
	PlanPendingApproval = "pending_approval"
	PlanApproved        = "approved"
	PlanRejected        = "rejected"
	PlanSuperseded      = "superseded" // replaced by a revised plan
	PlanCompleted       = "completed"
	PlanFailed          = "failed"
)

// Step statuses.
const (
	StepPending = "pending"
	StepRunning = "running"
	StepDone    = "done"
	StepFailed  = "failed"
	StepSkipped = "skipped"
)

// PlanStep is one executable step of a plan.
type PlanStep struct {
	Description string `json:"description"`
	Status      string `json:"status"`
	Output      string `json:"output,omitempty"`
}

// Plan is an agent's proposal for how to complete a task. Humans approve,
// amend (edit steps / leave feedback) or reject it before execution.
type Plan struct {
	ID        string     `json:"id"`
	TaskID    string     `json:"task_id"`
	AgentID   string     `json:"agent_id"`
	Summary   string     `json:"summary"`
	Steps     []PlanStep `json:"steps"`
	Status    string     `json:"status"`
	Feedback  string     `json:"feedback"` // human comment carried into execution/replan
	Version   int        `json:"version"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

// Job kinds and statuses. Jobs are the unit of distribution: runners claim
// them from the control plane and execute them.
const (
	JobPlan    = "plan"
	JobExecute = "execute"

	JobQueued  = "queued"
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

// Job is a schedulable work item (draft a plan / execute a plan).
type Job struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	TaskID    string    `json:"task_id"`
	PlanID    string    `json:"plan_id"`
	AgentID   string    `json:"agent_id"`
	Selector  string    `json:"selector"`
	Status    string    `json:"status"`
	RunnerID  string    `json:"runner_id"`
	Attempts  int       `json:"attempts"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Runner is a registered execution node (bare metal, docker, k8s pod, or the
// embedded in-process runner).
type Runner struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels"`
	Embedded  bool              `json:"embedded"`
	LastSeen  time.Time         `json:"last_seen"`
	CreatedAt time.Time         `json:"created_at"`
	Online    bool              `json:"online"` // derived from LastSeen
}

// Activity kinds.
const (
	ActSystem        = "system"
	ActAgentMessage  = "agent_message"
	ActPlanProposed  = "plan_proposed"
	ActPlanApproved  = "plan_approved"
	ActPlanRejected  = "plan_rejected"
	ActPlanAmended   = "plan_amended"
	ActStepStarted   = "step_started"
	ActStepCompleted = "step_completed"
	ActStepFailed    = "step_failed"
	ActTaskCreated   = "task_created"
	ActTaskCompleted = "task_completed"
	ActTaskFailed    = "task_failed"
	ActError         = "error"
)

// Activity is the audit/event log shown in agent and task feeds.
type Activity struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	AgentID   string    `json:"agent_id"`
	PlanID    string    `json:"plan_id"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Stats powers the dashboard header.
type Stats struct {
	Agents           int   `json:"agents"`
	AgentsWorking    int   `json:"agents_working"`
	Teams            int   `json:"teams"`
	OpenTasks        int   `json:"open_tasks"`
	PendingApprovals int   `json:"pending_approvals"`
	RunnersOnline    int   `json:"runners_online"`
	TasksDone        int   `json:"tasks_done"`
	TokensIn         int64 `json:"tokens_in"`
	TokensOut        int64 `json:"tokens_out"`
}
