package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dseif0x/coordworks/internal/domain"
	"github.com/dseif0x/coordworks/internal/llm"
	"github.com/dseif0x/coordworks/internal/store"
)

func nowPtr() *time.Time {
	t := time.Now().UTC()
	return &t
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, store.ErrNotFound) {
		status = http.StatusNotFound
	}
	var badReq *badRequestError
	if errors.As(err, &badReq) {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

type badRequestError struct{ msg string }

func (e *badRequestError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &badRequestError{msg: fmt.Sprintf(format, args...)}
}

func decode[T any](r *http.Request) (*T, error) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		return nil, badRequest("invalid JSON body: %v", err)
	}
	return &v, nil
}

// ---------- providers ----------

type providerInput struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	BaseURL      string   `json:"base_url"`
	APIKey       string   `json:"api_key"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model"`
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.store.ListProviders(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, providers)
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	in, err := decode[providerInput](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := validateProviderInput(in); err != nil {
		writeErr(w, err)
		return
	}
	p := &domain.Provider{
		Name: in.Name, Kind: in.Kind, BaseURL: in.BaseURL, APIKey: in.APIKey,
		Models: in.Models, DefaultModel: in.DefaultModel,
	}
	if err := s.store.CreateProvider(r.Context(), p); err != nil {
		writeErr(w, err)
		return
	}
	p.HasAPIKey = p.APIKey != ""
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProvider(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	in, err := decode[providerInput](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := validateProviderInput(in); err != nil {
		writeErr(w, err)
		return
	}
	p.Name, p.Kind, p.BaseURL = in.Name, in.Kind, in.BaseURL
	p.Models, p.DefaultModel = in.Models, in.DefaultModel
	if in.APIKey != "" { // empty means "keep existing key"
		p.APIKey = in.APIKey
	}
	if err := s.store.UpdateProvider(r.Context(), p); err != nil {
		writeErr(w, err)
		return
	}
	p.HasAPIKey = p.APIKey != ""
	writeJSON(w, http.StatusOK, p)
}

func validateProviderInput(in *providerInput) error {
	if in.Name == "" {
		return badRequest("name is required")
	}
	switch in.Kind {
	case domain.ProviderAnthropic, domain.ProviderOpenAI, domain.ProviderOpenAICompatible:
	default:
		return badRequest("kind must be one of anthropic, openai, openai_compatible")
	}
	if in.Kind == domain.ProviderOpenAICompatible && in.BaseURL == "" {
		return badRequest("base_url is required for openai_compatible providers")
	}
	return nil
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteProvider(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestProvider fires a one-token completion to verify credentials.
func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProvider(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	in, err := decode[struct {
		Model string `json:"model"`
	}](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	model := in.Model
	if model == "" {
		model = p.DefaultModel
	}
	if model == "" {
		writeErr(w, badRequest("no model specified and provider has no default model"))
		return
	}
	client, err := llm.New(llm.FromProvider(p))
	if err != nil {
		writeErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := client.Complete(ctx, llm.Request{
		Model:     model,
		Messages:  []llm.Message{{Role: "user", Content: "Reply with the single word: OK"}},
		MaxTokens: 20,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reply": resp.Text})
}

// ---------- teams ----------

func (s *Server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := s.store.ListTeams(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, teams)
}

func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	t, err := decode[domain.Team](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if t.Name == "" {
		writeErr(w, badRequest("name is required"))
		return
	}
	if t.Color == "" {
		t.Color = "#6366f1"
	}
	if err := s.store.CreateTeam(r.Context(), t); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleUpdateTeam(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTeam(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	in, err := decode[domain.Team](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	t.Name, t.Description, t.Color = in.Name, in.Description, in.Color
	if err := s.store.UpdateTeam(r.Context(), t); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteTeam(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- agents (hiring desk) ----------

type agentInput struct {
	Name           string `json:"name"`
	Role           string `json:"role"`
	TeamID         string `json:"team_id"`
	ProviderID     string `json:"provider_id"`
	Model          string `json:"model"`
	Persona        string `json:"persona"`
	Autonomy       string `json:"autonomy"`
	RunnerSelector string `json:"runner_selector"`
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.ListAgents(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	in, err := decode[agentInput](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.validateAgentInput(r.Context(), in); err != nil {
		writeErr(w, err)
		return
	}
	a := &domain.Agent{
		Name: in.Name, Role: in.Role, TeamID: in.TeamID,
		ProviderID: in.ProviderID, Model: in.Model, Persona: in.Persona,
		Autonomy: in.Autonomy, RunnerSelector: in.RunnerSelector,
	}
	if err := s.store.CreateAgent(r.Context(), a); err != nil {
		writeErr(w, err)
		return
	}
	s.orch.activity(r.Context(), &domain.Activity{
		AgentID: a.ID, Kind: domain.ActSystem,
		Content: fmt.Sprintf("%s hired as %s", a.Name, a.Role),
	})
	s.hub.Broadcast("agent.updated", map[string]string{"id": a.ID})
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	in, err := decode[agentInput](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.validateAgentInput(r.Context(), in); err != nil {
		writeErr(w, err)
		return
	}
	a.Name, a.Role, a.TeamID = in.Name, in.Role, in.TeamID
	a.ProviderID, a.Model, a.Persona = in.ProviderID, in.Model, in.Persona
	a.Autonomy, a.RunnerSelector = in.Autonomy, in.RunnerSelector
	if err := s.store.UpdateAgent(r.Context(), a); err != nil {
		writeErr(w, err)
		return
	}
	s.hub.Broadcast("agent.updated", map[string]string{"id": a.ID})
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) validateAgentInput(ctx context.Context, in *agentInput) error {
	if in.Name == "" {
		return badRequest("name is required")
	}
	if in.ProviderID == "" {
		return badRequest("provider_id is required — configure a provider in settings first")
	}
	if _, err := s.store.GetProvider(ctx, in.ProviderID); err != nil {
		return badRequest("provider %s not found", in.ProviderID)
	}
	if in.Model == "" {
		return badRequest("model is required")
	}
	if in.TeamID != "" {
		if _, err := s.store.GetTeam(ctx, in.TeamID); err != nil {
			return badRequest("team %s not found", in.TeamID)
		}
	}
	switch in.Autonomy {
	case "", domain.AutonomyApprovalRequired, domain.AutonomyAutonomous:
	default:
		return badRequest("autonomy must be approval_required or autonomous")
	}
	if in.Autonomy == "" {
		in.Autonomy = domain.AutonomyApprovalRequired
	}
	return nil
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteAgent(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	s.hub.Broadcast("agent.updated", map[string]string{"id": r.PathValue("id")})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// ---------- tasks ----------

type taskInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	TeamID      string `json:"team_id"`
	AgentID     string `json:"agent_id"`
	Priority    string `json:"priority"`
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.ListTasks(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("agent_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

// handleCreateTask creates a task; if it has an assignee (directly or via
// team auto-assign) planning starts immediately.
func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	in, err := decode[taskInput](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if in.Title == "" {
		writeErr(w, badRequest("title is required"))
		return
	}
	t := &domain.Task{
		Title: in.Title, Description: in.Description,
		TeamID: in.TeamID, AgentID: in.AgentID, Priority: in.Priority,
		CreatedBy: domain.OriginUser,
	}
	// Auto-assign: task for a team without explicit assignee goes to an idle
	// teammate (falling back to the least-loaded one).
	if t.AgentID == "" && t.TeamID != "" {
		if agentID, ok := s.pickTeamAgent(r.Context(), t.TeamID); ok {
			t.AgentID = agentID
		}
	}
	if err := s.store.CreateTask(r.Context(), t); err != nil {
		writeErr(w, err)
		return
	}
	if t.AgentID != "" {
		if err := s.orch.startTask(r.Context(), t); err != nil {
			writeErr(w, err)
			return
		}
	}
	s.hub.Broadcast("task.updated", map[string]string{"id": t.ID})
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) pickTeamAgent(ctx context.Context, teamID string) (string, bool) {
	agents, err := s.store.ListAgentsByTeam(ctx, teamID)
	if err != nil || len(agents) == 0 {
		return "", false
	}
	for _, a := range agents {
		if a.Status == domain.AgentIdle {
			return a.ID, true
		}
	}
	return agents[0].ID, true
}

// taskDetail is the full drill-down view.
type taskDetail struct {
	Task     *domain.Task       `json:"task"`
	Plans    []*domain.Plan     `json:"plans"`
	Activity []*domain.Activity `json:"activity"`
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	plans, err := s.store.ListPlansByTask(r.Context(), t.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	activity, err := s.store.ListActivity(r.Context(), t.ID, "", 200)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, taskDetail{Task: t, Plans: plans, Activity: activity})
}

// handleAssignTask (re)assigns a task and kicks off planning. Also used to
// retry failed/rejected tasks.
func (s *Server) handleAssignTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	switch t.Status {
	case domain.TaskInbox, domain.TaskFailed, domain.TaskRejected:
	default:
		writeErr(w, badRequest("task in status %q cannot be (re)assigned", t.Status))
		return
	}
	in, err := decode[struct {
		AgentID string `json:"agent_id"`
	}](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if in.AgentID != "" {
		t.AgentID = in.AgentID
	}
	if t.AgentID == "" {
		writeErr(w, badRequest("agent_id is required"))
		return
	}
	if _, err := s.store.GetAgent(r.Context(), t.AgentID); err != nil {
		writeErr(w, badRequest("agent %s not found", t.AgentID))
		return
	}
	if err := s.store.UpdateTask(r.Context(), t); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.orch.startTask(r.Context(), t); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteTask(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	s.hub.Broadcast("task.updated", map[string]string{"id": r.PathValue("id")})
	w.WriteHeader(http.StatusNoContent)
}

// ---------- approvals (HITL) ----------

// approvalItem joins a pending plan with its task and agent for the inbox.
type approvalItem struct {
	Plan  *domain.Plan  `json:"plan"`
	Task  *domain.Task  `json:"task"`
	Agent *domain.Agent `json:"agent"`
}

func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.ListPlansByStatus(r.Context(), domain.PlanPendingApproval)
	if err != nil {
		writeErr(w, err)
		return
	}
	items := make([]approvalItem, 0, len(plans))
	for _, p := range plans {
		item := approvalItem{Plan: p}
		if t, err := s.store.GetTask(r.Context(), p.TaskID); err == nil {
			item.Task = t
		}
		if a, err := s.store.GetAgent(r.Context(), p.AgentID); err == nil {
			item.Agent = a
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, items)
}

type decisionInput struct {
	Feedback string   `json:"feedback"`
	Steps    []string `json:"steps"` // approve only: amended step list
}

// handlePlanDecision handles approve / reject / request_changes.
func (s *Server) handlePlanDecision(w http.ResponseWriter, r *http.Request) {
	plan, err := s.store.GetPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if plan.Status != domain.PlanPendingApproval {
		writeErr(w, badRequest("plan is %s, only pending_approval plans can be decided", plan.Status))
		return
	}
	in, err := decode[decisionInput](r)
	if err != nil {
		writeErr(w, err)
		return
	}
	switch r.PathValue("decision") {
	case "approve":
		err = s.orch.approvePlan(r.Context(), plan, in.Feedback, in.Steps)
	case "reject":
		err = s.orch.rejectPlan(r.Context(), plan, in.Feedback)
	case "request_changes":
		err = s.orch.requestChanges(r.Context(), plan, in.Feedback)
	default:
		err = badRequest("unknown decision %q", r.PathValue("decision"))
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// ---------- misc ----------

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleListActivity(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.store.ListActivity(r.Context(), r.URL.Query().Get("task_id"), r.URL.Query().Get("agent_id"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleListRunners(w http.ResponseWriter, r *http.Request) {
	runners, err := s.store.ListRunners(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runners)
}

func (s *Server) handleDeleteRunner(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRunner(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	s.hub.Broadcast("runner.updated", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListJobs(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}
