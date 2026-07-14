import { useCallback, useEffect, useState, type ReactNode } from 'react'

// useData fetches on mount and exposes a refetch used by live events.
export function useData<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const refetch = useCallback(() => {
    fn().then(setData).catch((e) => setError(String(e.message ?? e)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  useEffect(refetch, [refetch])
  return { data, error, refetch }
}

const statusStyles: Record<string, string> = {
  // tasks
  inbox: 'border-edge bg-panel-2 text-dim',
  planning: 'border-accent/50 bg-accent/10 text-accent',
  awaiting_approval: 'border-warn/50 bg-warn/10 text-warn',
  executing: 'border-accent/50 bg-accent/10 text-accent',
  done: 'border-ok/50 bg-ok/10 text-ok',
  failed: 'border-bad/50 bg-bad/10 text-bad',
  rejected: 'border-bad/40 bg-bad/5 text-bad',
  // plans
  pending_approval: 'border-warn/50 bg-warn/10 text-warn',
  approved: 'border-ok/50 bg-ok/10 text-ok',
  superseded: 'border-edge bg-panel-2 text-dim',
  completed: 'border-ok/50 bg-ok/10 text-ok',
  // steps
  pending: 'border-edge bg-panel-2 text-dim',
  running: 'border-accent/50 bg-accent/10 text-accent',
  // agents
  idle: 'border-edge bg-panel-2 text-dim',
  working: 'border-ok/50 bg-ok/10 text-ok',
  offline: 'border-bad/40 bg-bad/5 text-bad',
}

export function StatusBadge({ status }: { status: string }) {
  return <span className={`chip ${statusStyles[status] ?? 'border-edge text-dim'}`}>{status.replace(/_/g, ' ')}</span>
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="card p-10 text-center text-dim text-sm">{children}</div>
}

export function ErrorNote({ msg }: { msg: string | null }) {
  if (!msg) return null
  return <div className="mb-4 px-4 py-2 rounded-lg border border-bad/50 bg-bad/10 text-bad text-sm">{msg}</div>
}

export function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-6" onClick={onClose}>
      <div className="card w-full max-w-2xl max-h-[85vh] overflow-auto p-6" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-base font-semibold">{title}</h2>
          <button className="text-dim hover:text-ink" onClick={onClose}>✕</button>
        </div>
        {children}
      </div>
    </div>
  )
}

export function PageTitle({ title, sub, action }: { title: string; sub?: string; action?: ReactNode }) {
  return (
    <div className="flex items-start justify-between mb-6">
      <div>
        <h1 className="text-xl font-bold tracking-tight">{title}</h1>
        {sub && <p className="text-sm text-dim mt-1">{sub}</p>}
      </div>
      {action}
    </div>
  )
}
