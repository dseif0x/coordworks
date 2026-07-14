import { Link } from 'react-router-dom'
import { api, timeAgo } from '../api'
import { useLive } from '../ws'
import PlanReview from '../components/PlanReview'
import { Empty, PageTitle, useData } from '../components/ui'

// Approvals is the HITL inbox: every proposed plan waits here for a human
// decision before any agent executes anything.
export default function Approvals() {
  const approvals = useData(() => api.approvals())
  useLive(['plan', 'approval', 'task'], approvals.refetch)

  return (
    <div>
      <PageTitle
        title="Approvals"
        sub="Agents propose, you dispose. Approve, amend the steps, send back with feedback, or reject."
      />
      {!approvals.data || approvals.data.length === 0 ? (
        <Empty>Inbox zero — no plans waiting for your sign-off.</Empty>
      ) : (
        <div className="space-y-5 max-w-4xl">
          {approvals.data.map(({ plan, task, agent }) => (
            <div key={plan.id} className="card p-5 border-warn/40">
              <div className="mb-1 text-sm">
                <span className="font-semibold">{agent?.name ?? 'Agent'}</span>
                <span className="text-dim"> ({agent?.role || 'generalist'}) proposes · plan v{plan.version} · {timeAgo(plan.created_at)}</span>
              </div>
              <div className="text-base font-medium mb-1">{plan.summary}</div>
              {task && (
                <div className="text-xs text-dim mb-4">
                  for task: <Link className="text-accent hover:underline" to={`/tasks/${task.id}`}>{task.title}</Link>
                </div>
              )}
              <PlanReview plan={plan} onDecided={approvals.refetch} />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
