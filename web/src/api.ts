// API client + types mirroring the Go domain models.

export interface Provider {
  id: string
  name: string
  kind: 'anthropic' | 'openai' | 'openai_compatible'
  base_url: string
  has_api_key: boolean
  models: string[]
  default_model: string
}

export interface Team {
  id: string
  name: string
  description: string
  color: string
}

export interface Agent {
  id: string
  name: string
  role: string
  team_id: string
  provider_id: string
  model: string
  persona: string
  autonomy: 'approval_required' | 'autonomous'
  status: 'idle' | 'working' | 'offline'
  runner_selector: string
  tokens_in: number
  tokens_out: number
  tasks_done: number
}

export type TaskStatus =
  | 'inbox'
  | 'planning'
  | 'awaiting_approval'
  | 'executing'
  | 'done'
  | 'failed'
  | 'rejected'

export interface Task {
  id: string
  title: string
  description: string
  team_id: string
  agent_id: string
  status: TaskStatus
  priority: string
  created_by: 'user' | 'agent'
  creator_id: string
  parent_task_id: string
  result: string
  created_at: string
}

export interface PlanStep {
  description: string
  status: 'pending' | 'running' | 'done' | 'failed' | 'skipped'
  output?: string
}

export interface Plan {
  id: string
  task_id: string
  agent_id: string
  summary: string
  steps: PlanStep[]
  status: 'pending_approval' | 'approved' | 'rejected' | 'superseded' | 'completed' | 'failed'
  feedback: string
  version: number
  created_at: string
}

export interface Activity {
  id: string
  task_id: string
  agent_id: string
  plan_id: string
  kind: string
  content: string
  created_at: string
}

export interface Runner {
  id: string
  name: string
  labels: Record<string, string>
  embedded: boolean
  online: boolean
  last_seen: string
}

export interface Stats {
  agents: number
  agents_working: number
  teams: number
  open_tasks: number
  pending_approvals: number
  runners_online: number
  tasks_done: number
  tokens_in: number
  tokens_out: number
}

export interface TaskDetail {
  task: Task
  plans: Plan[]
  activity: Activity[]
}

export interface ApprovalItem {
  plan: Plan
  task: Task | null
  agent: Agent | null
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`
    try {
      const body = await res.json()
      if (body.error) msg = body.error
    } catch {
      /* not JSON */
    }
    throw new Error(msg)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

export const api = {
  stats: () => request<Stats>('/api/stats'),
  activity: (params: { task_id?: string; agent_id?: string; limit?: number } = {}) => {
    const q = new URLSearchParams()
    if (params.task_id) q.set('task_id', params.task_id)
    if (params.agent_id) q.set('agent_id', params.agent_id)
    if (params.limit) q.set('limit', String(params.limit))
    return request<Activity[]>(`/api/activity?${q}`)
  },

  providers: () => request<Provider[]>('/api/providers'),
  createProvider: (p: object) =>
    request<Provider>('/api/providers', { method: 'POST', body: JSON.stringify(p) }),
  updateProvider: (id: string, p: object) =>
    request<Provider>(`/api/providers/${id}`, { method: 'PUT', body: JSON.stringify(p) }),
  deleteProvider: (id: string) => request<void>(`/api/providers/${id}`, { method: 'DELETE' }),
  testProvider: (id: string, model?: string) =>
    request<{ ok: boolean; reply?: string; error?: string }>(`/api/providers/${id}/test`, {
      method: 'POST',
      body: JSON.stringify({ model: model ?? '' }),
    }),

  teams: () => request<Team[]>('/api/teams'),
  createTeam: (t: object) => request<Team>('/api/teams', { method: 'POST', body: JSON.stringify(t) }),
  updateTeam: (id: string, t: object) =>
    request<Team>(`/api/teams/${id}`, { method: 'PUT', body: JSON.stringify(t) }),
  deleteTeam: (id: string) => request<void>(`/api/teams/${id}`, { method: 'DELETE' }),

  agents: () => request<Agent[]>('/api/agents'),
  createAgent: (a: object) => request<Agent>('/api/agents', { method: 'POST', body: JSON.stringify(a) }),
  updateAgent: (id: string, a: object) =>
    request<Agent>(`/api/agents/${id}`, { method: 'PUT', body: JSON.stringify(a) }),
  deleteAgent: (id: string) => request<void>(`/api/agents/${id}`, { method: 'DELETE' }),

  tasks: (params: { status?: string; agent_id?: string } = {}) => {
    const q = new URLSearchParams()
    if (params.status) q.set('status', params.status)
    if (params.agent_id) q.set('agent_id', params.agent_id)
    return request<Task[]>(`/api/tasks?${q}`)
  },
  createTask: (t: object) => request<Task>('/api/tasks', { method: 'POST', body: JSON.stringify(t) }),
  taskDetail: (id: string) => request<TaskDetail>(`/api/tasks/${id}`),
  assignTask: (id: string, agentId: string) =>
    request<Task>(`/api/tasks/${id}/assign`, { method: 'POST', body: JSON.stringify({ agent_id: agentId }) }),
  deleteTask: (id: string) => request<void>(`/api/tasks/${id}`, { method: 'DELETE' }),

  approvals: () => request<ApprovalItem[]>('/api/approvals'),
  decidePlan: (id: string, decision: 'approve' | 'reject' | 'request_changes', body: { feedback?: string; steps?: string[] }) =>
    request<Plan>(`/api/plans/${id}/${decision}`, { method: 'POST', body: JSON.stringify(body) }),

  runners: () => request<Runner[]>('/api/runners'),
  deleteRunner: (id: string) => request<void>(`/api/runners/${id}`, { method: 'DELETE' }),
}

export function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}

export function timeAgo(iso: string): string {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 60) return `${Math.floor(s)}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}
