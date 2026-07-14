# CoordWorks

Run a company staffed by AI agents. Hire employees, organize them into
departments, give them tasks — they draft plans, **you approve, amend or
reject** before anything executes, and they can delegate follow-up tasks to
their teammates. Execution is distributed: agents run on any mix of bare
metal, Docker and Kubernetes runners.

## Highlights

- **Hiring desk** — create agents with a name, role, department, persona and
  the model that powers them.
- **Multi-provider** — configure any number of LLM providers in settings:
  Anthropic (Messages API), OpenAI (Chat Completions) and any
  OpenAI-compatible endpoint (Ollama, vLLM, OpenRouter, Groq, …). Mix Claude
  staff with GPT staff with local-model staff freely.
- **Human-in-the-loop** — every task goes: agent drafts a plan → plan lands in
  your approval inbox → you approve, edit the steps inline, send it back with
  feedback for a replan, or reject. Agents you mark *autonomous* skip the gate.
- **Agent delegation** — while executing, agents can open tasks for their
  teammates; delegated tasks go through their own plan/approval cycle
  (delegation depth is capped to prevent runaway chains).
- **Distributed runners** — a stateless `coordworks-runner` binary registers
  with the control plane, heartbeats, and claims jobs via long-polling. Pin
  agents to runner classes with label selectors (`runtime=docker`,
  `gpu=true`). An embedded runner in the server means a single binary is a
  fully working system.
- **Live command center** — dashboard with staff/approval/token stats, org
  chart, task board, and per-task drill-down with plan versions, step outputs
  and a full activity feed, all updating over WebSocket.

## Architecture

```
                 ┌────────────────────────────────────────────┐
                 │            control plane (Go)              │
   React UI ◄──► │  REST + WebSocket API                      │
                 │  task/plan lifecycle + approval workflow   │
                 │  job queue (SQLite)      embedded runner   │
                 └──────────────┬─────────────────────────────┘
                                │ runner protocol (HTTP, token auth,
                                │ long-poll claim / events / complete)
              ┌─────────────────┼──────────────────┐
        ┌─────┴─────┐     ┌─────┴─────┐      ┌─────┴─────┐
        │  runner    │     │  runner   │      │  runner   │
        │ bare metal │     │  docker   │      │    k8s    │
        └─────┬─────┘     └─────┬─────┘      └─────┬─────┘
              └────────── LLM APIs (Anthropic / OpenAI / compatible)
```

- **Control plane** (`cmd/server`): owns all state (SQLite, pure-Go driver),
  serves the UI, exposes the runner protocol, and enforces the HITL workflow.
- **Runners** (`cmd/runner`): stateless workers. They receive a full job
  bundle (task, agent, provider credentials, plan), make the LLM calls
  locally, and stream side effects (activity, step progress, delegations,
  token usage) back to the control plane.
- **Jobs**: `plan` (draft a plan for a task) and `execute` (run an approved
  plan). Jobs from agents with a runner selector only match runners whose
  labels satisfy it. Jobs whose runner dies are requeued automatically.

### Task lifecycle

```
inbox → planning → awaiting_approval → executing → done
                     │        ▲            └────→ failed
   approve/amend ────┘        │ request changes (replan with your feedback)
   reject ────────────────────┴──→ rejected
```

## Quick start

### Docker (fastest)

```bash
docker build -t coordworks .
docker run -p 8080:8080 \
  -e COORDWORKS_RUNNER_TOKEN=$(openssl rand -hex 16) \
  -v coordworks-data:/data coordworks
# open http://localhost:8080
```

The root `Dockerfile` builds the all-in-one image: control plane + web UI +
embedded runner. SQLite state lives in the `/data` volume.

### From source

```bash
# 1. build everything (Go 1.25+, Node 22+)
make build

# 2. run the control plane (embedded runner included)
COORDWORKS_RUNNER_TOKEN=$(openssl rand -hex 16) ./bin/coordworks-server

# 3. open http://localhost:8080
#    Settings → add a provider (e.g. Anthropic + API key + models)
#    Organization → create a department
#    Hiring Desk → hire an agent (pick provider + model)
#    Tasks → create a task → watch the plan arrive in Approvals
```

### Docker Compose

```bash
COORDWORKS_RUNNER_TOKEN=$(openssl rand -hex 16) docker compose up --build
# scale the docker runner fleet:
docker compose up --scale runner=4
```

### Kubernetes (Helm)

The chart deploys the control plane (with persistent SQLite storage) plus a
scalable runner fleet, and supports Ingress with TLS. It is published to
GitHub Pages via chart-releaser:

```bash
helm repo add coordworks https://dseif0x.github.io/coordworks
helm install coordworks coordworks/coordworks \
  --set auth.runnerToken=$(openssl rand -hex 16) \
  --set runner.replicas=3
```

With Ingress + TLS (e.g. nginx + cert-manager):

```bash
helm install coordworks coordworks/coordworks \
  --set auth.runnerToken=$(openssl rand -hex 16) \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set 'ingress.annotations.cert-manager\.io/cluster-issuer=letsencrypt-prod' \
  --set ingress.hosts[0].host=coordworks.example.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set ingress.tls[0].secretName=coordworks-tls \
  --set ingress.tls[0].hosts[0]=coordworks.example.com
```

See `charts/coordworks/values.yaml` for all options (existing secrets,
persistence, runner labels/placement, resources). To install straight from
the repo without the chart repository: `helm install coordworks
charts/coordworks --set auth.runnerToken=...`.

Plain manifests for just a runner fleet (no Helm) remain in
`deploy/k8s/runner-deployment.yaml`:

```bash
kubectl apply -f deploy/k8s/runner-deployment.yaml
```

### Bare-metal runner

```bash
./bin/coordworks-runner \
  --server https://coordworks.example.com \
  --token "$COORDWORKS_RUNNER_TOKEN" \
  --labels runtime=baremetal,gpu=true
```

## Configuration

### Server (`coordworks-server`)

| Flag | Env | Default | Purpose |
| --- | --- | --- | --- |
| `--addr` | `COORDWORKS_ADDR` | `:8080` | listen address |
| `--db` | `COORDWORKS_DB` | `coordworks.db` | SQLite path |
| `--runner-token` | `COORDWORKS_RUNNER_TOKEN` | — (required) | shared secret for runners |
| `--api-token` | `COORDWORKS_API_TOKEN` | empty (off) | bearer token for the UI API |
| `--static` | `COORDWORKS_STATIC_DIR` | `web/dist` | built frontend dir |
| `--embedded-runner` | `COORDWORKS_EMBEDDED_RUNNER` | `true` | in-process runner |

### Runner (`coordworks-runner`)

| Flag | Env | Default | Purpose |
| --- | --- | --- | --- |
| `--server` | `COORDWORKS_SERVER_URL` | `http://localhost:8080` | control plane URL |
| `--token` | `COORDWORKS_RUNNER_TOKEN` | — (required) | shared secret |
| `--name` | `COORDWORKS_RUNNER_NAME` | hostname | display name |
| `--labels` | `COORDWORKS_RUNNER_LABELS` | `runtime=baremetal` | `k=v,k2=v2` labels for job placement |

## Development

```bash
# backend with auto-created dev token
make dev

# frontend with hot reload (proxies /api to :8080)
cd web && npm run dev

# tests
go test ./...
```

## Repository layout

```
cmd/server/          control plane entrypoint
cmd/runner/          distributed runner entrypoint
internal/domain/     core entities (providers, teams, agents, tasks, plans, jobs, runners)
internal/store/      SQLite persistence
internal/llm/        provider abstraction (anthropic, openai, openai-compatible)
internal/agent/      agent brain: plan generation + step execution + delegation
internal/runner/     runner protocol + remote runner loop
internal/server/     REST/WS API, orchestrator (task/plan/job lifecycle), embedded runner
web/                 React + TypeScript + Vite + Tailwind command center UI
deploy/              Dockerfiles, docker-compose, k8s manifests
```

## Security notes

- Provider API keys are stored in the control plane database and shipped to
  runners inside job bundles over the runner protocol; run the control plane
  behind TLS (reverse proxy) in production.
- API keys are never returned to the browser (`has_api_key` only).
- Set `COORDWORKS_API_TOKEN` to protect the UI API; runners authenticate with
  `COORDWORKS_RUNNER_TOKEN`.
