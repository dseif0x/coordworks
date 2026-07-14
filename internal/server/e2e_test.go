package server

import (
	"bytes"
	"context"
	"encoding/json"

	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dseif0x/coordworks/internal/domain"
	runnerpkg "github.com/dseif0x/coordworks/internal/runner"
	"github.com/dseif0x/coordworks/internal/store"
)

// fakeLLM emulates an OpenAI-compatible /chat/completions endpoint. The one
// response body satisfies every JSON shape the agent engine parses (plan
// draft, step reply, final wrap-up), so the full lifecycle runs offline.
func fakeLLM(t *testing.T) *httptest.Server {
	t.Helper()
	reply := map[string]any{
		"summary":     "Test plan for the task",
		"steps":       []string{"Research the topic", "Write the summary"},
		"output":      "Step finished: lorem ipsum findings.",
		"status":      "done",
		"delegations": []map[string]string{{"title": "Follow-up research", "description": "dig deeper", "assignee": ""}},
		"result":      "All done: delivered the summary.",
		"success":     true,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		content, _ := json.Marshal(reply)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": string(content)}}},
			"usage":   map[string]int{"prompt_tokens": 100, "completion_tokens": 50},
		})
	}))
}

func startServer(t *testing.T) (baseURL string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	cfg := Config{Addr: addr, RunnerToken: "test-token", EmbeddedRunner: true}
	srv := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx) }()

	baseURL = "http://" + addr
	waitFor(t, 5*time.Second, "server healthy", func() bool {
		resp, err := http.Get(baseURL + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	return baseURL
}

func call[T any](t *testing.T, method, url string, body any) T {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s %s: status %d: %s", method, url, resp.StatusCode, data)
	}
	var out T
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode %s: %v (%s)", url, err, data)
		}
	}
	return out
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestFullLifecycle drives the whole HITL loop over the public API:
// provider → team → agent → task → plan proposal → human approval →
// execution (with a delegated task) → done.
func TestFullLifecycle(t *testing.T) {
	llm := fakeLLM(t)
	defer llm.Close()
	base := startServer(t)

	provider := call[domain.Provider](t, "POST", base+"/api/providers", map[string]any{
		"name": "fake", "kind": "openai_compatible", "base_url": llm.URL,
		"api_key": "k", "models": []string{"test-model"}, "default_model": "test-model",
	})
	team := call[domain.Team](t, "POST", base+"/api/teams", map[string]any{
		"name": "Research", "color": "#22d3ee",
	})
	agent := call[domain.Agent](t, "POST", base+"/api/agents", map[string]any{
		"name": "Avery", "role": "Analyst", "team_id": team.ID,
		"provider_id": provider.ID, "model": "test-model",
	})
	task := call[domain.Task](t, "POST", base+"/api/tasks", map[string]any{
		"title": "Summarize the market", "description": "One page.", "team_id": team.ID, "agent_id": agent.ID,
	})

	// Embedded runner drafts the plan; it must land in the approval inbox.
	var approval approvalItem
	waitFor(t, 15*time.Second, "plan pending approval", func() bool {
		items := call[[]approvalItem](t, "GET", base+"/api/approvals", nil)
		if len(items) == 0 {
			return false
		}
		approval = items[0]
		return true
	})
	if approval.Task == nil || approval.Task.ID != task.ID {
		t.Fatalf("approval is for the wrong task: %+v", approval.Task)
	}
	if len(approval.Plan.Steps) != 2 {
		t.Fatalf("expected 2 plan steps, got %d", len(approval.Plan.Steps))
	}
	detail := call[taskDetail](t, "GET", base+"/api/tasks/"+task.ID, nil)
	if detail.Task.Status != domain.TaskAwaitingApproval {
		t.Fatalf("task status = %s, want awaiting_approval", detail.Task.Status)
	}

	// Human approves with feedback (HITL gate).
	call[domain.Plan](t, "POST", base+"/api/plans/"+approval.Plan.ID+"/approve", map[string]any{
		"feedback": "Keep it short.",
	})

	waitFor(t, 20*time.Second, "task done", func() bool {
		detail = call[taskDetail](t, "GET", base+"/api/tasks/"+task.ID, nil)
		return detail.Task.Status == domain.TaskDone
	})
	if detail.Task.Result == "" {
		t.Fatal("finished task has empty result")
	}
	if len(detail.Plans) == 0 || detail.Plans[0].Status != domain.PlanCompleted {
		t.Fatalf("plan not completed: %+v", detail.Plans)
	}
	for i, s := range detail.Plans[0].Steps {
		if s.Status != domain.StepDone {
			t.Fatalf("step %d status = %s, want done", i, s.Status)
		}
		if s.Output == "" {
			t.Fatalf("step %d has no recorded output", i)
		}
	}
	if len(detail.Activity) == 0 {
		t.Fatal("no activity recorded for task")
	}

	// The agent delegated follow-up work during execution.
	tasks := call[[]domain.Task](t, "GET", base+"/api/tasks", nil)
	var delegated *domain.Task
	for _, x := range tasks {
		if x.CreatedBy == domain.OriginAgent && x.ParentTaskID == task.ID {
			delegated = &x
			break
		}
	}
	if delegated == nil {
		t.Fatal("expected a delegated task created by the agent")
	}
	if delegated.TeamID != team.ID {
		t.Fatalf("delegated task team = %q, want %q", delegated.TeamID, team.ID)
	}

	// Token accounting flowed back to the agent.
	got := call[domain.Agent](t, "GET", base+"/api/agents/"+agent.ID, nil)
	if got.TokensIn == 0 || got.TokensOut == 0 {
		t.Fatalf("agent usage not recorded: in=%d out=%d", got.TokensIn, got.TokensOut)
	}
	if got.TasksDone != 1 {
		t.Fatalf("agent tasks_done = %d, want 1", got.TasksDone)
	}

	stats := call[domain.Stats](t, "GET", base+"/api/stats", nil)
	if stats.Agents != 1 || stats.TasksDone != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

// TestRejectAndReplan covers the other two HITL paths.
func TestRejectAndReplan(t *testing.T) {
	llm := fakeLLM(t)
	defer llm.Close()
	base := startServer(t)

	provider := call[domain.Provider](t, "POST", base+"/api/providers", map[string]any{
		"name": "fake", "kind": "openai_compatible", "base_url": llm.URL, "models": []string{"m"},
	})
	agent := call[domain.Agent](t, "POST", base+"/api/agents", map[string]any{
		"name": "Rem", "provider_id": provider.ID, "model": "m",
	})

	// --- request_changes triggers a replan with a new version ---
	task := call[domain.Task](t, "POST", base+"/api/tasks", map[string]any{
		"title": "Draft roadmap", "agent_id": agent.ID,
	})
	var planID string
	waitFor(t, 15*time.Second, "plan v1", func() bool {
		items := call[[]approvalItem](t, "GET", base+"/api/approvals", nil)
		for _, it := range items {
			if it.Task != nil && it.Task.ID == task.ID {
				planID = it.Plan.ID
				return true
			}
		}
		return false
	})
	call[domain.Plan](t, "POST", base+"/api/plans/"+planID+"/request_changes", map[string]any{
		"feedback": "Focus on Q3 only.",
	})
	waitFor(t, 15*time.Second, "plan v2 pending", func() bool {
		items := call[[]approvalItem](t, "GET", base+"/api/approvals", nil)
		for _, it := range items {
			if it.Task != nil && it.Task.ID == task.ID && it.Plan.Version == 2 {
				planID = it.Plan.ID
				return true
			}
		}
		return false
	})

	// --- reject kills the task ---
	call[domain.Plan](t, "POST", base+"/api/plans/"+planID+"/reject", map[string]any{
		"feedback": "Not now.",
	})
	detail := call[taskDetail](t, "GET", base+"/api/tasks/"+task.ID, nil)
	if detail.Task.Status != domain.TaskRejected {
		t.Fatalf("task status = %s, want rejected", detail.Task.Status)
	}

	// Amended approval: edit steps inline, then approve.
	task2 := call[domain.Task](t, "POST", base+"/api/tasks", map[string]any{
		"title": "Write blog post", "agent_id": agent.ID,
	})
	var plan2 string
	waitFor(t, 15*time.Second, "task2 plan pending", func() bool {
		items := call[[]approvalItem](t, "GET", base+"/api/approvals", nil)
		for _, it := range items {
			if it.Task != nil && it.Task.ID == task2.ID {
				plan2 = it.Plan.ID
				return true
			}
		}
		return false
	})
	call[domain.Plan](t, "POST", base+"/api/plans/"+plan2+"/approve", map[string]any{
		"feedback": "Amended.",
		"steps":    []string{"Only do this one amended step"},
	})
	var detail2 taskDetail
	waitFor(t, 20*time.Second, "task2 done", func() bool {
		detail2 = call[taskDetail](t, "GET", base+"/api/tasks/"+task2.ID, nil)
		return detail2.Task.Status == domain.TaskDone
	})
	if n := len(detail2.Plans[0].Steps); n != 1 {
		t.Fatalf("amended plan has %d steps, want 1", n)
	}
}

func TestSelectorMatching(t *testing.T) {
	cases := []struct {
		selector string
		labels   map[string]string
		want     bool
	}{
		{"", map[string]string{"runtime": "docker"}, true},
		{"runtime=docker", map[string]string{"runtime": "docker"}, true},
		{"runtime=docker", map[string]string{"runtime": "embedded"}, false},
		{"runtime=k8s,gpu=true", map[string]string{"runtime": "k8s", "gpu": "true"}, true},
		{"runtime=k8s,gpu=true", map[string]string{"runtime": "k8s"}, false},
		{" runtime = docker ", map[string]string{"runtime": "docker"}, true},
	}
	for _, c := range cases {
		if got := store.SelectorMatches(c.selector, c.labels); got != c.want {
			t.Errorf("SelectorMatches(%q, %v) = %v, want %v", c.selector, c.labels, got, c.want)
		}
	}
}

// TestRemoteRunner runs the same lifecycle with the embedded runner disabled
// and a real remote runner connected over the wire protocol, including a
// runner label selector on the agent.
func TestRemoteRunner(t *testing.T) {
	llm := fakeLLM(t)
	defer llm.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	cfg := Config{Addr: addr, RunnerToken: "remote-secret", EmbeddedRunner: false}
	srv := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx) }()
	base := "http://" + addr
	waitFor(t, 5*time.Second, "server healthy", func() bool {
		resp, err := http.Get(base + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	remote := runnerpkg.NewRemote(runnerpkg.RemoteConfig{
		ServerURL: base,
		Token:     "remote-secret",
		Name:      "test-remote",
		Labels:    map[string]string{"runtime": "docker"},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	go func() { _ = remote.Run(ctx) }()

	provider := call[domain.Provider](t, "POST", base+"/api/providers", map[string]any{
		"name": "fake", "kind": "openai_compatible", "base_url": llm.URL, "models": []string{"m"},
	})
	// Agent pinned to docker runners: only the remote runner may claim it.
	agent := call[domain.Agent](t, "POST", base+"/api/agents", map[string]any{
		"name": "Dock", "provider_id": provider.ID, "model": "m", "runner_selector": "runtime=docker",
	})
	task := call[domain.Task](t, "POST", base+"/api/tasks", map[string]any{
		"title": "Remote job", "agent_id": agent.ID,
	})

	var planID string
	waitFor(t, 20*time.Second, "remote plan pending", func() bool {
		items := call[[]approvalItem](t, "GET", base+"/api/approvals", nil)
		if len(items) == 0 {
			return false
		}
		planID = items[0].Plan.ID
		return true
	})
	call[domain.Plan](t, "POST", base+"/api/plans/"+planID+"/approve", map[string]any{})
	waitFor(t, 20*time.Second, "remote task done", func() bool {
		d := call[taskDetail](t, "GET", base+"/api/tasks/"+task.ID, nil)
		return d.Task.Status == domain.TaskDone
	})

	runners := call[[]domain.Runner](t, "GET", base+"/api/runners", nil)
	found := false
	for _, r := range runners {
		if r.Name == "test-remote" && r.Online && r.Labels["runtime"] == "docker" {
			found = true
		}
	}
	if !found {
		t.Fatalf("remote runner not registered/online: %+v", runners)
	}
}
