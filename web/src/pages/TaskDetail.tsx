import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, timeAgo, type Plan } from '../api'
import { useLive } from '../ws'
import PlanReview from '../components/PlanReview'
import { ErrorNote, PageTitle, StatusBadge, useData } from '../components/ui'

// TaskDetailPage is the "conference room": the task, every plan version with
// step progress, HITL controls, and the full activity feed.
export default function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const detail = useData(() => api.taskDetail(id!), [id])
  const agents = useData(() => api.agents())
  const [error, setError] = useState<string | null>(null)
  const [assignee, setAssignee] = useState('')

  useLive(['task', 'plan', 'activity'], detail.refetch)

  if (detail.error) return <ErrorNote msg={detail.error} />
  if (!detail.data) return <div className="text-dim text-sm">Loading…</div>

  const { task, plans, activity } = detail.data
  const agent = agents.data?.find((a) => a.id === task.agent_id)
  const agentName = (aid: string) => agents.data?.find((a) => a.id === aid)?.name ?? 'agent'
  const canReassign = ['inbox', 'failed', 'rejected'].includes(task.status)

  const reassign = async () => {
    setError(null)
    try {
      await api.assignTask(task.id, assignee || task.agent_id)
      detail.refetch()
    } catch (e) {
      setError(String((e as Error).message ?? e))
    }
  }

  const remove = async () => {
    try {
      await api.deleteTask(task.id)
      navigate('/tasks')
    } catch (e) {
      setError(String((e as Error).message ?? e))
    }
  }

  return (
    <div>
      <div className="mb-4">
        <Link to="/tasks" className="text-xs text-dim hover:text-ink">← all tasks</Link>
      </div>
      <PageTitle
        title={task.title}
        sub={task.description || undefined}
        action={
          <div className="flex items-center gap-2">
            <StatusBadge status={task.status} />
            <button className="btn-bad text-xs" onClick={remove}>Delete</button>
          </div>
        }
      />
      <ErrorNote msg={error} />

      <div className="text-xs text-dim mb-6 flex gap-4 flex-wrap">
        <span>assignee: <span className="text-ink">{agent?.name ?? 'unassigned'}</span></span>
        <span>priority: <span className="text-ink">{task.priority}</span></span>
        {task.created_by === 'agent' && <span>created by agent <span className="text-ink">{agentName(task.creator_id)}</span></span>}
        {task.parent_task_id && (
          <Link className="text-accent hover:underline" to={`/tasks/${task.parent_task_id}`}>parent task →</Link>
        )}
      </div>

      {canReassign && (
        <div className="card p-4 mb-6 flex items-end gap-3">
          <div className="flex-1 max-w-xs">
            <label className="label">{task.status === 'inbox' ? 'Assign to' : 'Retry with'}</label>
            <select className="input" value={assignee} onChange={(e) => setAssignee(e.target.value)}>
              <option value="">{agent ? agent.name : '— select agent —'}</option>
              {agents.data?.map((a) => (
                <option key={a.id} value={a.id}>{a.name} ({a.role})</option>
              ))}
            </select>
          </div>
          <button className="btn-primary" disabled={!assignee && !task.agent_id} onClick={reassign}>
            {task.status === 'inbox' ? 'Assign & plan' : 'Replan'}
          </button>
        </div>
      )}

      {task.result && (
        <div className={`card p-4 mb-6 border ${task.status === 'done' ? 'border-ok/50' : 'border-bad/50'}`}>
          <div className="text-xs uppercase tracking-wider text-dim mb-2">Result</div>
          <div className="text-sm whitespace-pre-wrap">{task.result}</div>
        </div>
      )}

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
        <section>
          <h2 className="text-sm font-semibold mb-3">Plans</h2>
          {plans.length === 0 && <div className="card p-6 text-center text-dim text-sm">No plan yet{task.status === 'planning' && ' — the agent is thinking…'}</div>}
          <div className="space-y-4">
            {plans.map((p) => (
              <PlanCard key={p.id} plan={p} onDecided={detail.refetch} />
            ))}
          </div>
        </section>

        <section>
          <h2 className="text-sm font-semibold mb-3">Activity</h2>
          <div className="card divide-y divide-edge">
            {activity.length === 0 && <div className="p-6 text-center text-dim text-sm">Nothing yet.</div>}
            {activity.map((a) => (
              <div key={a.id} className="px-4 py-2.5 text-sm">
                <span className="break-words whitespace-pre-wrap">{a.content}</span>
                <div className="text-[11px] text-dim mt-0.5">
                  {a.kind.replace(/_/g, ' ')} · {agentName(a.agent_id)} · {timeAgo(a.created_at)}
                </div>
              </div>
            ))}
          </div>
        </section>
      </div>
    </div>
  )
}

function PlanCard({ plan, onDecided }: { plan: Plan; onDecided: () => void }) {
  const pending = plan.status === 'pending_approval'
  return (
    <div className={`card p-4 ${pending ? 'border-warn/50' : ''}`}>
      <div className="flex items-center gap-2 mb-3">
        <span className="text-sm font-medium flex-1">{plan.summary}</span>
        <span className="text-xs text-dim font-mono">v{plan.version}</span>
        <StatusBadge status={plan.status} />
      </div>
      {pending ? (
        <PlanReview plan={plan} onDecided={onDecided} />
      ) : (
        <>
          <ol className="space-y-2">
            {plan.steps.map((s, i) => (
              <li key={i} className="text-sm">
                <div className="flex gap-2 items-baseline">
                  <span className="font-mono text-dim text-xs w-5 text-right shrink-0">{i + 1}.</span>
                  <span className="flex-1">{s.description}</span>
                  <StatusBadge status={s.status} />
                </div>
                {s.output && (
                  <details className="ml-7 mt-1">
                    <summary className="text-xs text-accent/80 cursor-pointer">output</summary>
                    <div className="text-xs text-dim whitespace-pre-wrap mt-1 bg-panel-2 rounded-lg p-3 border border-edge">{s.output}</div>
                  </details>
                )}
              </li>
            ))}
          </ol>
          {plan.feedback && (
            <div className="mt-3 text-xs text-dim border-t border-edge pt-2">
              <span className="uppercase tracking-wider">your feedback:</span> {plan.feedback}
            </div>
          )}
        </>
      )}
    </div>
  )
}
