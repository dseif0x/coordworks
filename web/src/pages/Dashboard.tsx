import { Link } from 'react-router-dom'
import { api, timeAgo } from '../api'
import { useLive } from '../ws'
import { Empty, PageTitle, StatusBadge, useData } from '../components/ui'

const actIcons: Record<string, string> = {
  system: '·',
  agent_message: '💬',
  plan_proposed: '📋',
  plan_approved: '✅',
  plan_rejected: '⛔',
  plan_amended: '✏️',
  step_started: '▶',
  step_completed: '✔',
  step_failed: '✖',
  task_created: '＋',
  task_completed: '🏁',
  task_failed: '🔥',
  error: '⚠',
}

export default function Dashboard() {
  const approvals = useData(() => api.approvals())
  const activity = useData(() => api.activity({ limit: 30 }))
  const agents = useData(() => api.agents())

  useLive(['plan', 'approval', 'task'], approvals.refetch)
  useLive(['activity'], activity.refetch)
  useLive(['agent'], agents.refetch)

  const agentName = (id: string) => agents.data?.find((a) => a.id === id)?.name ?? ''
  const working = agents.data?.filter((a) => a.status === 'working') ?? []

  return (
    <div>
      <PageTitle title="Command" sub="Run the company — approvals first, then watch the floor." />

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
        <section>
          <h2 className="text-sm font-semibold text-warn mb-3">
            Needs you {approvals.data && approvals.data.length > 0 && `(${approvals.data.length})`}
          </h2>
          {!approvals.data || approvals.data.length === 0 ? (
            <Empty>No pending approvals. Your staff is either working or waiting for tasks.</Empty>
          ) : (
            <div className="space-y-3">
              {approvals.data.slice(0, 5).map((item) => (
                <Link key={item.plan.id} to="/approvals" className="card p-4 block hover:border-warn/50 transition-colors">
                  <div className="text-sm font-medium">
                    {item.agent?.name ?? 'Agent'} proposes: {item.plan.summary}
                  </div>
                  <div className="text-xs text-dim mt-1">
                    task: {item.task?.title} · {item.plan.steps.length} steps · plan v{item.plan.version}
                  </div>
                </Link>
              ))}
              {approvals.data.length > 5 && (
                <Link to="/approvals" className="block text-center text-sm text-warn hover:underline">
                  +{approvals.data.length - 5} more in the approval inbox →
                </Link>
              )}
            </div>
          )}

          <h2 className="text-sm font-semibold text-ok mt-8 mb-3">On the clock {working.length > 0 && `(${working.length})`}</h2>
          {working.length === 0 ? (
            <Empty>Nobody is actively working right now.</Empty>
          ) : (
            <div className="space-y-2">
              {working.map((a) => (
                <div key={a.id} className="card p-3 flex items-center gap-3">
                  <span className="w-2 h-2 rounded-full bg-ok animate-pulse" />
                  <span className="text-sm font-medium">{a.name}</span>
                  <span className="text-xs text-dim">{a.role}</span>
                  <StatusBadge status={a.status} />
                </div>
              ))}
            </div>
          )}
        </section>

        <section>
          <h2 className="text-sm font-semibold text-accent mb-3">Live activity</h2>
          {!activity.data || activity.data.length === 0 ? (
            <Empty>
              Quiet so far. Configure a <Link className="text-accent hover:underline" to="/settings">provider</Link>,{' '}
              <Link className="text-accent hover:underline" to="/hiring">hire staff</Link> and{' '}
              <Link className="text-accent hover:underline" to="/tasks">create a task</Link>.
            </Empty>
          ) : (
            <div className="card divide-y divide-edge">
              {activity.data.map((a) => (
                <div key={a.id} className="px-4 py-2.5 flex gap-3 text-sm items-baseline">
                  <span className="w-5 text-center shrink-0">{actIcons[a.kind] ?? '·'}</span>
                  <div className="min-w-0 flex-1">
                    <span className="break-words">{a.content}</span>
                    <div className="text-xs text-dim mt-0.5">
                      {agentName(a.agent_id)} {a.task_id && (
                        <Link to={`/tasks/${a.task_id}`} className="text-accent/70 hover:underline">view task</Link>
                      )}{' '}
                      · {timeAgo(a.created_at)}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
