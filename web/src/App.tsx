import { useCallback, useEffect, useState } from 'react'
import { NavLink, Route, Routes } from 'react-router-dom'
import { api, fmtTokens, type Stats } from './api'
import { useLive } from './ws'
import Dashboard from './pages/Dashboard'
import Org from './pages/Org'
import HiringDesk from './pages/HiringDesk'
import Tasks from './pages/Tasks'
import TaskDetailPage from './pages/TaskDetail'
import Approvals from './pages/Approvals'
import Runners from './pages/Runners'
import Settings from './pages/Settings'

const nav = [
  { to: '/', label: 'Command', icon: '◉' },
  { to: '/org', label: 'Organization', icon: '▦' },
  { to: '/hiring', label: 'Hiring Desk', icon: '✚' },
  { to: '/tasks', label: 'Tasks', icon: '☰' },
  { to: '/approvals', label: 'Approvals', icon: '✓' },
  { to: '/runners', label: 'Runners', icon: '⬢' },
  { to: '/settings', label: 'Settings', icon: '⚙' },
]

export default function App() {
  const [stats, setStats] = useState<Stats | null>(null)
  const [navOpen, setNavOpen] = useState(false)

  const refresh = useCallback(() => {
    api.stats().then(setStats).catch(() => {})
  }, [])

  useEffect(refresh, [refresh])
  useLive(['*'], refresh)

  return (
    <div className="min-h-screen flex flex-col lg:flex-row">
      {/* Mobile top bar */}
      <div className="lg:hidden sticky top-0 z-40 flex items-center gap-3 px-4 py-3 border-b border-edge bg-panel">
        <button
          className="btn px-2.5 py-1.5"
          aria-label="Open navigation"
          onClick={() => setNavOpen(true)}
        >
          ☰
        </button>
        <div className="text-base font-bold tracking-tight">
          <span className="text-accent">Coord</span>Works
        </div>
        {(stats?.pending_approvals ?? 0) > 0 && (
          <NavLink to="/approvals" className="ml-auto chip border-warn/50 bg-warn/15 text-warn">
            {stats!.pending_approvals} need you
          </NavLink>
        )}
      </div>

      {/* Mobile drawer */}
      {navOpen && (
        <div className="lg:hidden fixed inset-0 z-50 flex">
          <div className="w-64 max-w-[80vw] bg-panel border-r border-edge flex flex-col">
            <SideNav stats={stats} onNavigate={() => setNavOpen(false)} />
          </div>
          <div className="flex-1 bg-black/60" onClick={() => setNavOpen(false)} />
        </div>
      )}

      {/* Desktop sidebar */}
      <aside className="hidden lg:flex w-56 shrink-0 border-r border-edge bg-panel flex-col">
        <SideNav stats={stats} />
      </aside>

      <div className="flex-1 flex flex-col min-w-0">
        <header className="border-b border-edge bg-panel/60 backdrop-blur px-4 sm:px-6 py-3 flex flex-wrap items-center gap-x-5 gap-y-1.5 text-sm">
          <Stat label="staff" value={stats?.agents ?? 0} />
          <Stat label="active" value={stats?.agents_working ?? 0} accent={(stats?.agents_working ?? 0) > 0 ? 'text-ok' : ''} />
          <Stat label="open tasks" value={stats?.open_tasks ?? 0} />
          <Stat label="need you" value={stats?.pending_approvals ?? 0} accent={(stats?.pending_approvals ?? 0) > 0 ? 'text-warn' : ''} />
          <Stat label="done" value={stats?.tasks_done ?? 0} />
          <div className="hidden md:block ml-auto text-xs text-dim whitespace-nowrap">
            AI spend (tokens): <span className="font-mono text-ink">{fmtTokens(stats?.tokens_in ?? 0)} in / {fmtTokens(stats?.tokens_out ?? 0)} out</span>
          </div>
        </header>
        <main className="flex-1 overflow-auto p-4 sm:p-6">
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/org" element={<Org />} />
            <Route path="/hiring" element={<HiringDesk />} />
            <Route path="/tasks" element={<Tasks />} />
            <Route path="/tasks/:id" element={<TaskDetailPage />} />
            <Route path="/approvals" element={<Approvals />} />
            <Route path="/runners" element={<Runners />} />
            <Route path="/settings" element={<Settings />} />
          </Routes>
        </main>
      </div>
    </div>
  )
}

// SideNav is shared between the desktop sidebar and the mobile drawer.
function SideNav({ stats, onNavigate }: { stats: Stats | null; onNavigate?: () => void }) {
  return (
    <>
      <div className="px-5 py-5 border-b border-edge">
        <div className="text-lg font-bold tracking-tight">
          <span className="text-accent">Coord</span>Works
        </div>
        <div className="text-xs text-dim mt-0.5">Command Center</div>
      </div>
      <nav className="flex-1 py-3 overflow-y-auto">
        {nav.map((n) => (
          <NavLink
            key={n.to}
            to={n.to}
            end={n.to === '/'}
            onClick={onNavigate}
            className={({ isActive }) =>
              `flex items-center gap-3 px-5 py-2.5 text-sm transition-colors ${
                isActive ? 'text-accent bg-accent/10 border-r-2 border-accent' : 'text-dim hover:text-ink'
              }`
            }
          >
            <span className="w-4 text-center">{n.icon}</span>
            {n.label}
            {n.to === '/approvals' && (stats?.pending_approvals ?? 0) > 0 && (
              <span className="ml-auto chip border-warn/50 bg-warn/15 text-warn">{stats!.pending_approvals}</span>
            )}
          </NavLink>
        ))}
      </nav>
      <div className="px-5 py-4 border-t border-edge text-xs text-dim space-y-1">
        <div>
          tokens <span className="text-ink font-mono">{fmtTokens((stats?.tokens_in ?? 0) + (stats?.tokens_out ?? 0))}</span>
        </div>
        <div>
          runners online <span className={`font-mono ${(stats?.runners_online ?? 0) > 0 ? 'text-ok' : 'text-bad'}`}>{stats?.runners_online ?? 0}</span>
        </div>
      </div>
    </>
  )
}

function Stat({ label, value, accent = '' }: { label: string; value: number; accent?: string }) {
  return (
    <div className="flex items-baseline gap-1.5 whitespace-nowrap">
      <span className={`font-mono text-base font-semibold ${accent || 'text-ink'}`}>{value}</span>
      <span className="text-dim text-xs">{label}</span>
    </div>
  )
}
