import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, timeAgo, type Task, type TaskStatus } from '../api'
import { useLive } from '../ws'
import { ErrorNote, Modal, PageTitle, StatusBadge, useData } from '../components/ui'

const columns: { title: string; statuses: TaskStatus[] }[] = [
  { title: 'Inbox', statuses: ['inbox'] },
  { title: 'Planning', statuses: ['planning', 'awaiting_approval'] },
  { title: 'Executing', statuses: ['executing'] },
  { title: 'Finished', statuses: ['done', 'failed', 'rejected'] },
]

// Tasks is the work board: give instructions to your staff here.
export default function Tasks() {
  const tasks = useData(() => api.tasks())
  const agents = useData(() => api.agents())
  const teams = useData(() => api.teams())
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useLive(['task', 'plan'], tasks.refetch)

  const agentName = (id: string) => agents.data?.find((a) => a.id === id)?.name

  return (
    <div>
      <PageTitle
        title="Tasks"
        sub="Give instructions; agents plan, you approve, they execute. Agents can also open tasks for their team."
        action={<button className="btn-primary" onClick={() => setCreating(true)}>+ New task</button>}
      />
      <ErrorNote msg={error} />

      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
        {columns.map((col) => {
          const items = tasks.data?.filter((t) => col.statuses.includes(t.status)) ?? []
          return (
            <div key={col.title} className="min-w-0">
              <div className="text-xs uppercase tracking-wider text-dim mb-2">
                {col.title} <span className="font-mono">({items.length})</span>
              </div>
              <div className="space-y-2">
                {items.map((t) => (
                  <TaskCard key={t.id} task={t} agentName={agentName(t.agent_id)} />
                ))}
                {items.length === 0 && <div className="card p-4 text-center text-xs text-dim">empty</div>}
              </div>
            </div>
          )
        })}
      </div>

      {creating && (
        <NewTaskModal
          teams={teams.data ?? []}
          agents={agents.data ?? []}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false)
            tasks.refetch()
          }}
          onError={(e) => {
            setError(e)
            setCreating(false)
          }}
        />
      )}
    </div>
  )
}

function TaskCard({ task, agentName }: { task: Task; agentName?: string }) {
  return (
    <Link to={`/tasks/${task.id}`} className="card p-3 block hover:border-accent/50 transition-colors">
      <div className="text-sm font-medium leading-snug">{task.title}</div>
      <div className="flex items-center gap-2 mt-2 flex-wrap">
        <StatusBadge status={task.status} />
        {task.priority !== 'normal' && <span className="chip border-warn/40 bg-warn/10 text-warn">{task.priority}</span>}
        {task.created_by === 'agent' && <span className="chip border-accent/40 bg-accent/10 text-accent">by agent</span>}
      </div>
      <div className="text-[11px] text-dim mt-2">
        {agentName ?? 'unassigned'} · {timeAgo(task.created_at)}
      </div>
    </Link>
  )
}

function NewTaskModal({
  teams, agents, onClose, onCreated, onError,
}: {
  teams: { id: string; name: string }[]
  agents: { id: string; name: string; role: string; team_id: string }[]
  onClose: () => void
  onCreated: () => void
  onError: (e: string) => void
}) {
  const [form, setForm] = useState({ title: '', description: '', team_id: '', agent_id: '', priority: 'normal' })

  const teamAgents = form.team_id ? agents.filter((a) => a.team_id === form.team_id) : agents

  const create = async () => {
    try {
      await api.createTask(form)
      onCreated()
    } catch (e) {
      onError(String((e as Error).message ?? e))
    }
  }

  return (
    <Modal title="New task" onClose={onClose}>
      <div className="space-y-4">
        <div>
          <label className="label">Title</label>
          <input className="input" value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} placeholder="What needs to get done?" autoFocus />
        </div>
        <div>
          <label className="label">Instructions</label>
          <textarea className="input" rows={4} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} placeholder="Context, constraints, expected outcome…" />
        </div>
        <div className="grid grid-cols-3 gap-4">
          <div>
            <label className="label">Department</label>
            <select className="input" value={form.team_id} onChange={(e) => setForm({ ...form, team_id: e.target.value, agent_id: '' })}>
              <option value="">— any —</option>
              {teams.map((t) => (
                <option key={t.id} value={t.id}>{t.name}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="label">Assignee</label>
            <select className="input" value={form.agent_id} onChange={(e) => setForm({ ...form, agent_id: e.target.value })}>
              <option value="">auto-assign</option>
              {teamAgents.map((a) => (
                <option key={a.id} value={a.id}>{a.name} ({a.role})</option>
              ))}
            </select>
          </div>
          <div>
            <label className="label">Priority</label>
            <select className="input" value={form.priority} onChange={(e) => setForm({ ...form, priority: e.target.value })}>
              <option value="low">low</option>
              <option value="normal">normal</option>
              <option value="high">high</option>
              <option value="urgent">urgent</option>
            </select>
          </div>
        </div>
        <p className="text-xs text-dim">
          Assigned tasks start planning immediately. The plan lands in your approval inbox before anything executes
          (unless the agent is autonomous).
        </p>
        <button className="btn-primary" disabled={!form.title.trim()} onClick={create}>Create task</button>
      </div>
    </Modal>
  )
}
