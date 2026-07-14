import { useState } from 'react'
import { api, type Plan } from '../api'
import { StatusBadge } from './ui'

// PlanReview is the HITL heart of the app: view a proposed plan, amend the
// steps inline, leave feedback, then approve / request changes / reject.
export default function PlanReview({ plan, onDecided }: { plan: Plan; onDecided: () => void }) {
  const [feedback, setFeedback] = useState('')
  const [editing, setEditing] = useState(false)
  const [steps, setSteps] = useState<string[]>(plan.steps.map((s) => s.description))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const decide = async (decision: 'approve' | 'reject' | 'request_changes') => {
    setBusy(true)
    setError(null)
    try {
      await api.decidePlan(plan.id, decision, {
        feedback,
        steps: decision === 'approve' && editing ? steps : undefined,
      })
      onDecided()
    } catch (e) {
      setError(String((e as Error).message ?? e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <div className="mb-3">
        {editing ? (
          <div className="space-y-2">
            {steps.map((s, i) => (
              <div key={i} className="flex gap-2 items-start">
                <span className="font-mono text-dim text-xs mt-2.5 w-5 text-right">{i + 1}.</span>
                <textarea
                  className="input min-h-[2.5rem]"
                  value={s}
                  rows={2}
                  onChange={(e) => setSteps(steps.map((v, j) => (j === i ? e.target.value : v)))}
                />
                <button
                  className="btn-bad px-2 py-1 mt-1"
                  title="Remove step"
                  onClick={() => setSteps(steps.filter((_, j) => j !== i))}
                >
                  ✕
                </button>
              </div>
            ))}
            <button className="btn text-xs" onClick={() => setSteps([...steps, ''])}>
              + Add step
            </button>
          </div>
        ) : (
          <ol className="space-y-1.5">
            {plan.steps.map((s, i) => (
              <li key={i} className="flex gap-2 text-sm items-baseline">
                <span className="font-mono text-dim text-xs w-5 text-right shrink-0">{i + 1}.</span>
                <span className="flex-1">{s.description}</span>
                {s.status !== 'pending' && <StatusBadge status={s.status} />}
              </li>
            ))}
          </ol>
        )}
      </div>

      <textarea
        className="input mb-3"
        placeholder="Feedback / instructions for the agent (required to request changes, optional otherwise)…"
        rows={2}
        value={feedback}
        onChange={(e) => setFeedback(e.target.value)}
      />
      {error && <div className="text-bad text-xs mb-2">{error}</div>}

      <div className="flex flex-wrap gap-2">
        <button className="btn-ok" disabled={busy} onClick={() => decide('approve')}>
          ✓ Approve{editing ? ' amended plan' : ''}
        </button>
        <button className="btn" disabled={busy} onClick={() => setEditing(!editing)}>
          {editing ? 'Discard edits' : '✎ Amend steps'}
        </button>
        <button className="btn-warn" disabled={busy || !feedback.trim()} onClick={() => decide('request_changes')} title="Send back to the agent with feedback for a new plan">
          ↺ Request changes
        </button>
        <button className="btn-bad" disabled={busy} onClick={() => decide('reject')}>
          ✕ Reject
        </button>
      </div>
    </div>
  )
}
