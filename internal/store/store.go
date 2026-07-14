// Package store persists all CoordWorks state in SQLite (pure-Go driver, no
// CGO) so the control plane is a single self-contained binary.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/dseif0x/coordworks/internal/domain"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// RunnerOnlineWindow is how recent a heartbeat must be for a runner to count
// as online.
const RunnerOnlineWindow = 30 * time.Second

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	// _pragma busy_timeout avoids SQLITE_BUSY under concurrent writers.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // serialize writes; SQLite handles this best
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS providers (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	kind TEXT NOT NULL,
	base_url TEXT NOT NULL DEFAULT '',
	api_key TEXT NOT NULL DEFAULT '',
	models TEXT NOT NULL DEFAULT '[]',
	default_model TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);
CREATE TABLE IF NOT EXISTS teams (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	color TEXT NOT NULL DEFAULT '#6366f1',
	created_at TIMESTAMP NOT NULL
);
CREATE TABLE IF NOT EXISTS agents (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT '',
	team_id TEXT NOT NULL DEFAULT '',
	provider_id TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	persona TEXT NOT NULL DEFAULT '',
	autonomy TEXT NOT NULL DEFAULT 'approval_required',
	status TEXT NOT NULL DEFAULT 'idle',
	runner_selector TEXT NOT NULL DEFAULT '',
	tokens_in INTEGER NOT NULL DEFAULT 0,
	tokens_out INTEGER NOT NULL DEFAULT 0,
	tasks_done INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);
CREATE TABLE IF NOT EXISTS tasks (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	team_id TEXT NOT NULL DEFAULT '',
	agent_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'inbox',
	priority TEXT NOT NULL DEFAULT 'normal',
	created_by TEXT NOT NULL DEFAULT 'user',
	creator_id TEXT NOT NULL DEFAULT '',
	parent_task_id TEXT NOT NULL DEFAULT '',
	result TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);
CREATE TABLE IF NOT EXISTS plans (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	steps TEXT NOT NULL DEFAULT '[]',
	status TEXT NOT NULL DEFAULT 'pending_approval',
	feedback TEXT NOT NULL DEFAULT '',
	version INTEGER NOT NULL DEFAULT 1,
	created_at TIMESTAMP NOT NULL,
	decided_at TIMESTAMP
);
CREATE TABLE IF NOT EXISTS jobs (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	task_id TEXT NOT NULL DEFAULT '',
	plan_id TEXT NOT NULL DEFAULT '',
	agent_id TEXT NOT NULL DEFAULT '',
	selector TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'queued',
	runner_id TEXT NOT NULL DEFAULT '',
	attempts INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);
CREATE TABLE IF NOT EXISTS runners (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	labels TEXT NOT NULL DEFAULT '{}',
	embedded INTEGER NOT NULL DEFAULT 0,
	last_seen TIMESTAMP NOT NULL,
	created_at TIMESTAMP NOT NULL
);
CREATE TABLE IF NOT EXISTS activity (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL DEFAULT '',
	agent_id TEXT NOT NULL DEFAULT '',
	plan_id TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL,
	content TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_plans_status ON plans(status);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_activity_task ON activity(task_id, created_at);
`)
	return err
}

func newID() string { return uuid.NewString() }

func now() time.Time { return time.Now().UTC() }

// ---------- providers ----------

func (s *Store) CreateProvider(ctx context.Context, p *domain.Provider) error {
	p.ID = newID()
	p.CreatedAt, p.UpdatedAt = now(), now()
	models, _ := json.Marshal(p.Models)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO providers (id,name,kind,base_url,api_key,models,default_model,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Kind, p.BaseURL, p.APIKey, string(models), p.DefaultModel, p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *Store) UpdateProvider(ctx context.Context, p *domain.Provider) error {
	p.UpdatedAt = now()
	models, _ := json.Marshal(p.Models)
	res, err := s.db.ExecContext(ctx,
		`UPDATE providers SET name=?,kind=?,base_url=?,api_key=?,models=?,default_model=?,updated_at=? WHERE id=?`,
		p.Name, p.Kind, p.BaseURL, p.APIKey, string(models), p.DefaultModel, p.UpdatedAt, p.ID)
	return checkAffected(res, err)
}

func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM providers WHERE id=?`, id)
	return checkAffected(res, err)
}

func (s *Store) GetProvider(ctx context.Context, id string) (*domain.Provider, error) {
	return scanOne(s.db.QueryRowContext(ctx, `SELECT id,name,kind,base_url,api_key,models,default_model,created_at,updated_at FROM providers WHERE id=?`, id), scanProvider)
}

func (s *Store) ListProviders(ctx context.Context) ([]*domain.Provider, error) {
	return queryMany(ctx, s, scanProvider, `SELECT id,name,kind,base_url,api_key,models,default_model,created_at,updated_at FROM providers ORDER BY created_at`)
}

func scanProvider(r rowScanner) (*domain.Provider, error) {
	var p domain.Provider
	var models string
	if err := r.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &models, &p.DefaultModel, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(models), &p.Models)
	p.HasAPIKey = p.APIKey != ""
	return &p, nil
}

// ---------- teams ----------

func (s *Store) CreateTeam(ctx context.Context, t *domain.Team) error {
	t.ID = newID()
	t.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO teams (id,name,description,color,created_at) VALUES (?,?,?,?,?)`,
		t.ID, t.Name, t.Description, t.Color, t.CreatedAt)
	return err
}

func (s *Store) UpdateTeam(ctx context.Context, t *domain.Team) error {
	res, err := s.db.ExecContext(ctx, `UPDATE teams SET name=?,description=?,color=? WHERE id=?`, t.Name, t.Description, t.Color, t.ID)
	return checkAffected(res, err)
}

func (s *Store) DeleteTeam(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM teams WHERE id=?`, id)
	return checkAffected(res, err)
}

func (s *Store) GetTeam(ctx context.Context, id string) (*domain.Team, error) {
	return scanOne(s.db.QueryRowContext(ctx, `SELECT id,name,description,color,created_at FROM teams WHERE id=?`, id), scanTeam)
}

func (s *Store) ListTeams(ctx context.Context) ([]*domain.Team, error) {
	return queryMany(ctx, s, scanTeam, `SELECT id,name,description,color,created_at FROM teams ORDER BY created_at`)
}

func scanTeam(r rowScanner) (*domain.Team, error) {
	var t domain.Team
	if err := r.Scan(&t.ID, &t.Name, &t.Description, &t.Color, &t.CreatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// ---------- agents ----------

const agentCols = `id,name,role,team_id,provider_id,model,persona,autonomy,status,runner_selector,tokens_in,tokens_out,tasks_done,created_at,updated_at`

func (s *Store) CreateAgent(ctx context.Context, a *domain.Agent) error {
	a.ID = newID()
	a.CreatedAt, a.UpdatedAt = now(), now()
	if a.Status == "" {
		a.Status = domain.AgentIdle
	}
	if a.Autonomy == "" {
		a.Autonomy = domain.AutonomyApprovalRequired
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (`+agentCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Name, a.Role, a.TeamID, a.ProviderID, a.Model, a.Persona, a.Autonomy, a.Status, a.RunnerSelector,
		a.TokensIn, a.TokensOut, a.TasksDone, a.CreatedAt, a.UpdatedAt)
	return err
}

func (s *Store) UpdateAgent(ctx context.Context, a *domain.Agent) error {
	a.UpdatedAt = now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE agents SET name=?,role=?,team_id=?,provider_id=?,model=?,persona=?,autonomy=?,status=?,runner_selector=?,updated_at=? WHERE id=?`,
		a.Name, a.Role, a.TeamID, a.ProviderID, a.Model, a.Persona, a.Autonomy, a.Status, a.RunnerSelector, a.UpdatedAt, a.ID)
	return checkAffected(res, err)
}

func (s *Store) SetAgentStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET status=?,updated_at=? WHERE id=?`, status, now(), id)
	return err
}

func (s *Store) AddAgentUsage(ctx context.Context, id string, in, out int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET tokens_in=tokens_in+?,tokens_out=tokens_out+?,updated_at=? WHERE id=?`, in, out, now(), id)
	return err
}

func (s *Store) IncrAgentTasksDone(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET tasks_done=tasks_done+1,updated_at=? WHERE id=?`, now(), id)
	return err
}

func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE id=?`, id)
	return checkAffected(res, err)
}

func (s *Store) GetAgent(ctx context.Context, id string) (*domain.Agent, error) {
	return scanOne(s.db.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents WHERE id=?`, id), scanAgent)
}

func (s *Store) ListAgents(ctx context.Context) ([]*domain.Agent, error) {
	return queryMany(ctx, s, scanAgent, `SELECT `+agentCols+` FROM agents ORDER BY created_at`)
}

func (s *Store) ListAgentsByTeam(ctx context.Context, teamID string) ([]*domain.Agent, error) {
	return queryMany(ctx, s, scanAgent, `SELECT `+agentCols+` FROM agents WHERE team_id=? ORDER BY created_at`, teamID)
}

func scanAgent(r rowScanner) (*domain.Agent, error) {
	var a domain.Agent
	if err := r.Scan(&a.ID, &a.Name, &a.Role, &a.TeamID, &a.ProviderID, &a.Model, &a.Persona, &a.Autonomy, &a.Status,
		&a.RunnerSelector, &a.TokensIn, &a.TokensOut, &a.TasksDone, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// ---------- tasks ----------

const taskCols = `id,title,description,team_id,agent_id,status,priority,created_by,creator_id,parent_task_id,result,created_at,updated_at`

func (s *Store) CreateTask(ctx context.Context, t *domain.Task) error {
	t.ID = newID()
	t.CreatedAt, t.UpdatedAt = now(), now()
	if t.Status == "" {
		t.Status = domain.TaskInbox
	}
	if t.Priority == "" {
		t.Priority = "normal"
	}
	if t.CreatedBy == "" {
		t.CreatedBy = domain.OriginUser
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (`+taskCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Title, t.Description, t.TeamID, t.AgentID, t.Status, t.Priority, t.CreatedBy, t.CreatorID, t.ParentTaskID, t.Result, t.CreatedAt, t.UpdatedAt)
	return err
}

func (s *Store) UpdateTask(ctx context.Context, t *domain.Task) error {
	t.UpdatedAt = now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET title=?,description=?,team_id=?,agent_id=?,status=?,priority=?,result=?,updated_at=? WHERE id=?`,
		t.Title, t.Description, t.TeamID, t.AgentID, t.Status, t.Priority, t.Result, t.UpdatedAt, t.ID)
	return checkAffected(res, err)
}

func (s *Store) SetTaskStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET status=?,updated_at=? WHERE id=?`, status, now(), id)
	return err
}

func (s *Store) SetTaskResult(ctx context.Context, id, status, result string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET status=?,result=?,updated_at=? WHERE id=?`, status, result, now(), id)
	return err
}

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id=?`, id)
	return checkAffected(res, err)
}

func (s *Store) GetTask(ctx context.Context, id string) (*domain.Task, error) {
	return scanOne(s.db.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE id=?`, id), scanTask)
}

// ListTasks optionally filters by status and agent.
func (s *Store) ListTasks(ctx context.Context, status, agentID string) ([]*domain.Task, error) {
	q := `SELECT ` + taskCols + ` FROM tasks`
	var conds []string
	var args []any
	if status != "" {
		conds = append(conds, `status=?`)
		args = append(args, status)
	}
	if agentID != "" {
		conds = append(conds, `agent_id=?`)
		args = append(args, agentID)
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY created_at DESC LIMIT 500`
	return queryMany(ctx, s, scanTask, q, args...)
}

func (s *Store) CountTasks(ctx context.Context, statuses ...string) (int, error) {
	q := `SELECT COUNT(*) FROM tasks`
	var args []any
	if len(statuses) > 0 {
		q += ` WHERE status IN (?` + strings.Repeat(",?", len(statuses)-1) + `)`
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	var n int
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

func scanTask(r rowScanner) (*domain.Task, error) {
	var t domain.Task
	if err := r.Scan(&t.ID, &t.Title, &t.Description, &t.TeamID, &t.AgentID, &t.Status, &t.Priority,
		&t.CreatedBy, &t.CreatorID, &t.ParentTaskID, &t.Result, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// ---------- plans ----------

const planCols = `id,task_id,agent_id,summary,steps,status,feedback,version,created_at,decided_at`

func (s *Store) CreatePlan(ctx context.Context, p *domain.Plan) error {
	p.ID = newID()
	p.CreatedAt = now()
	if p.Status == "" {
		p.Status = domain.PlanPendingApproval
	}
	if p.Version == 0 {
		p.Version = 1
	}
	steps, _ := json.Marshal(p.Steps)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO plans (`+planCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.TaskID, p.AgentID, p.Summary, string(steps), p.Status, p.Feedback, p.Version, p.CreatedAt, p.DecidedAt)
	return err
}

func (s *Store) UpdatePlan(ctx context.Context, p *domain.Plan) error {
	steps, _ := json.Marshal(p.Steps)
	res, err := s.db.ExecContext(ctx,
		`UPDATE plans SET summary=?,steps=?,status=?,feedback=?,decided_at=? WHERE id=?`,
		p.Summary, string(steps), p.Status, p.Feedback, p.DecidedAt, p.ID)
	return checkAffected(res, err)
}

func (s *Store) GetPlan(ctx context.Context, id string) (*domain.Plan, error) {
	return scanOne(s.db.QueryRowContext(ctx, `SELECT `+planCols+` FROM plans WHERE id=?`, id), scanPlan)
}

func (s *Store) ListPlansByTask(ctx context.Context, taskID string) ([]*domain.Plan, error) {
	return queryMany(ctx, s, scanPlan, `SELECT `+planCols+` FROM plans WHERE task_id=? ORDER BY version DESC, created_at DESC`, taskID)
}

func (s *Store) ListPlansByStatus(ctx context.Context, status string) ([]*domain.Plan, error) {
	return queryMany(ctx, s, scanPlan, `SELECT `+planCols+` FROM plans WHERE status=? ORDER BY created_at`, status)
}

func (s *Store) CountPlansByStatus(ctx context.Context, status string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plans WHERE status=?`, status).Scan(&n)
	return n, err
}

// LatestPlanVersion returns the max plan version for a task (0 when none).
func (s *Store) LatestPlanVersion(ctx context.Context, taskID string) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM plans WHERE task_id=?`, taskID).Scan(&v)
	return int(v.Int64), err
}

func scanPlan(r rowScanner) (*domain.Plan, error) {
	var p domain.Plan
	var steps string
	var decided sql.NullTime
	if err := r.Scan(&p.ID, &p.TaskID, &p.AgentID, &p.Summary, &steps, &p.Status, &p.Feedback, &p.Version, &p.CreatedAt, &decided); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(steps), &p.Steps)
	if decided.Valid {
		t := decided.Time
		p.DecidedAt = &t
	}
	return &p, nil
}

// ---------- jobs ----------

const jobCols = `id,kind,task_id,plan_id,agent_id,selector,status,runner_id,attempts,error,created_at,updated_at`

func (s *Store) CreateJob(ctx context.Context, j *domain.Job) error {
	j.ID = newID()
	j.CreatedAt, j.UpdatedAt = now(), now()
	if j.Status == "" {
		j.Status = domain.JobQueued
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (`+jobCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Kind, j.TaskID, j.PlanID, j.AgentID, j.Selector, j.Status, j.RunnerID, j.Attempts, j.Error, j.CreatedAt, j.UpdatedAt)
	return err
}

// ClaimJob atomically hands the oldest matching queued job to a runner.
// Selector matching happens in Go because labels are JSON.
func (s *Store) ClaimJob(ctx context.Context, runnerID string, labels map[string]string) (*domain.Job, error) {
	jobs, err := queryMany(ctx, s, scanJob, `SELECT `+jobCols+` FROM jobs WHERE status='queued' ORDER BY created_at LIMIT 50`)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if !SelectorMatches(j.Selector, labels) {
			continue
		}
		res, err := s.db.ExecContext(ctx,
			`UPDATE jobs SET status='running',runner_id=?,attempts=attempts+1,updated_at=? WHERE id=? AND status='queued'`,
			runnerID, now(), j.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			j.Status = domain.JobRunning
			j.RunnerID = runnerID
			j.Attempts++
			return j, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) FinishJob(ctx context.Context, id, status, errMsg string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?,error=?,updated_at=? WHERE id=?`, status, errMsg, now(), id)
	return checkAffected(res, err)
}

// RequeueStaleJobs returns running jobs whose runner went offline back to the
// queue (up to maxAttempts), otherwise fails them. Returns requeued/failed ids.
func (s *Store) RequeueStaleJobs(ctx context.Context, maxAttempts int) (requeued, failed []string, err error) {
	cutoff := now().Add(-RunnerOnlineWindow * 2)
	rows, err := s.db.QueryContext(ctx, `
		SELECT j.id, j.attempts FROM jobs j
		LEFT JOIN runners r ON r.id = j.runner_id
		WHERE j.status='running' AND (r.id IS NULL OR r.last_seen < ?)`, cutoff)
	if err != nil {
		return nil, nil, err
	}
	type row struct {
		id       string
		attempts int
	}
	var stale []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.attempts); err != nil {
			rows.Close()
			return nil, nil, err
		}
		stale = append(stale, r)
	}
	rows.Close()
	for _, r := range stale {
		if r.attempts >= maxAttempts {
			if _, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='failed',error='runner went offline',updated_at=? WHERE id=?`, now(), r.id); err != nil {
				return requeued, failed, err
			}
			failed = append(failed, r.id)
		} else {
			if _, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='queued',runner_id='',updated_at=? WHERE id=?`, now(), r.id); err != nil {
				return requeued, failed, err
			}
			requeued = append(requeued, r.id)
		}
	}
	return requeued, failed, nil
}

func (s *Store) GetJob(ctx context.Context, id string) (*domain.Job, error) {
	return scanOne(s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id=?`, id), scanJob)
}

func (s *Store) ListJobs(ctx context.Context, status string) ([]*domain.Job, error) {
	if status != "" {
		return queryMany(ctx, s, scanJob, `SELECT `+jobCols+` FROM jobs WHERE status=? ORDER BY created_at DESC LIMIT 200`, status)
	}
	return queryMany(ctx, s, scanJob, `SELECT `+jobCols+` FROM jobs ORDER BY created_at DESC LIMIT 200`)
}

func scanJob(r rowScanner) (*domain.Job, error) {
	var j domain.Job
	if err := r.Scan(&j.ID, &j.Kind, &j.TaskID, &j.PlanID, &j.AgentID, &j.Selector, &j.Status, &j.RunnerID, &j.Attempts, &j.Error, &j.CreatedAt, &j.UpdatedAt); err != nil {
		return nil, err
	}
	return &j, nil
}

// SelectorMatches reports whether runner labels satisfy a "k=v,k2=v2"
// selector. An empty selector matches every runner.
func SelectorMatches(selector string, labels map[string]string) bool {
	if strings.TrimSpace(selector) == "" {
		return true
	}
	for _, pair := range strings.Split(selector, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			continue
		}
		if labels[strings.TrimSpace(k)] != strings.TrimSpace(v) {
			return false
		}
	}
	return true
}

// ---------- runners ----------

func (s *Store) UpsertRunner(ctx context.Context, r *domain.Runner) error {
	if r.ID == "" {
		r.ID = newID()
	}
	r.CreatedAt, r.LastSeen = now(), now()
	labels, _ := json.Marshal(r.Labels)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runners (id,name,labels,embedded,last_seen,created_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,labels=excluded.labels,last_seen=excluded.last_seen`,
		r.ID, r.Name, string(labels), boolToInt(r.Embedded), r.LastSeen, r.CreatedAt)
	return err
}

func (s *Store) TouchRunner(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE runners SET last_seen=? WHERE id=?`, now(), id)
	return checkAffected(res, err)
}

func (s *Store) GetRunner(ctx context.Context, id string) (*domain.Runner, error) {
	return scanOne(s.db.QueryRowContext(ctx, `SELECT id,name,labels,embedded,last_seen,created_at FROM runners WHERE id=?`, id), scanRunner)
}

func (s *Store) ListRunners(ctx context.Context) ([]*domain.Runner, error) {
	return queryMany(ctx, s, scanRunner, `SELECT id,name,labels,embedded,last_seen,created_at FROM runners ORDER BY created_at`)
}

func (s *Store) DeleteRunner(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM runners WHERE id=?`, id)
	return checkAffected(res, err)
}

func scanRunner(r rowScanner) (*domain.Runner, error) {
	var rn domain.Runner
	var labels string
	var embedded int
	if err := r.Scan(&rn.ID, &rn.Name, &labels, &embedded, &rn.LastSeen, &rn.CreatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(labels), &rn.Labels)
	rn.Embedded = embedded == 1
	rn.Online = time.Since(rn.LastSeen) < RunnerOnlineWindow
	return &rn, nil
}

// ---------- activity ----------

func (s *Store) AddActivity(ctx context.Context, a *domain.Activity) error {
	a.ID = newID()
	a.CreatedAt = now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO activity (id,task_id,agent_id,plan_id,kind,content,created_at) VALUES (?,?,?,?,?,?,?)`,
		a.ID, a.TaskID, a.AgentID, a.PlanID, a.Kind, a.Content, a.CreatedAt)
	return err
}

func (s *Store) ListActivity(ctx context.Context, taskID, agentID string, limit int) ([]*domain.Activity, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id,task_id,agent_id,plan_id,kind,content,created_at FROM activity`
	var conds []string
	var args []any
	if taskID != "" {
		conds = append(conds, `task_id=?`)
		args = append(args, taskID)
	}
	if agentID != "" {
		conds = append(conds, `agent_id=?`)
		args = append(args, agentID)
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	return queryMany(ctx, s, scanActivity, q, args...)
}

func scanActivity(r rowScanner) (*domain.Activity, error) {
	var a domain.Activity
	if err := r.Scan(&a.ID, &a.TaskID, &a.AgentID, &a.PlanID, &a.Kind, &a.Content, &a.CreatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// ---------- stats ----------

func (s *Store) Stats(ctx context.Context) (*domain.Stats, error) {
	st := &domain.Stats{}
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(tokens_in),0), COALESCE(SUM(tokens_out),0), COALESCE(SUM(tasks_done),0) FROM agents`)
	if err := row.Scan(&st.Agents, &st.TokensIn, &st.TokensOut, &st.TasksDone); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE status='working'`).Scan(&st.AgentsWorking); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams`).Scan(&st.Teams); err != nil {
		return nil, err
	}
	var err error
	if st.OpenTasks, err = s.CountTasks(ctx, domain.TaskInbox, domain.TaskPlanning, domain.TaskAwaitingApproval, domain.TaskExecuting); err != nil {
		return nil, err
	}
	if st.PendingApprovals, err = s.CountPlansByStatus(ctx, domain.PlanPendingApproval); err != nil {
		return nil, err
	}
	cutoff := now().Add(-RunnerOnlineWindow)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runners WHERE last_seen >= ?`, cutoff).Scan(&st.RunnersOnline); err != nil {
		return nil, err
	}
	return st, nil
}

// ---------- scan helpers ----------

type rowScanner interface{ Scan(dest ...any) error }

func scanOne[T any](row *sql.Row, scan func(rowScanner) (*T, error)) (*T, error) {
	v, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func queryMany[T any](ctx context.Context, s *Store, scan func(rowScanner) (*T, error), q string, args ...any) ([]*T, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func checkAffected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
