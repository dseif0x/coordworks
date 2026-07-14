import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, fmtTokens, type Agent, type Team } from '../api'
import { useLive } from '../ws'
import { Empty, ErrorNote, Modal, PageTitle, StatusBadge, useData } from '../components/ui'

const teamColors = ['#22d3ee', '#a78bfa', '#f472b6', '#fbbf24', '#34d399', '#fb923c', '#60a5fa', '#f87171']

// Org renders the departments floor: one panel per team with its agents.
export default function Org() {
  const teams = useData(() => api.teams())
  const agents = useData(() => api.agents())
  const [editTeam, setEditTeam] = useState<Team | 'new' | null>(null)
  const [error, setError] = useState<string | null>(null)

  useLive(['agent'], agents.refetch)

  const byTeam = (teamID: string) => agents.data?.filter((a) => a.team_id === teamID) ?? []
  const unassigned = agents.data?.filter((a) => !a.team_id || !teams.data?.some((t) => t.id === a.team_id)) ?? []

  return (
    <div>
      <PageTitle
        title="Organization"
        sub="Departments and the staff working in them."
        action={<button className="btn-primary" onClick={() => setEditTeam('new')}>+ New department</button>}
      />
      <ErrorNote msg={error} />

      {teams.data?.length === 0 && unassigned.length === 0 && (
        <Empty>
          No departments yet. Create one, then <Link to="/hiring" className="text-accent hover:underline">hire staff</Link> into it.
        </Empty>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-2 2xl:grid-cols-3 gap-5">
        {teams.data?.map((team) => (
          <div key={team.id} className="card overflow-hidden" style={{ borderColor: `${team.color}55` }}>
            <div className="px-4 py-3 flex items-center gap-2 border-b border-edge" style={{ background: `${team.color}14` }}>
              <span className="w-2.5 h-2.5 rounded-sm" style={{ background: team.color }} />
              <span className="font-semibold text-sm uppercase tracking-wide">{team.name}</span>
              <span className="text-xs text-dim">{byTeam(team.id).length} staff</span>
              <button className="ml-auto text-dim hover:text-ink text-xs" onClick={() => setEditTeam(team)}>edit</button>
            </div>
            {team.description && <div className="px-4 pt-2 text-xs text-dim">{team.description}</div>}
            <div className="p-4 grid grid-cols-1 sm:grid-cols-2 gap-3">
              {byTeam(team.id).map((a) => (
                <AgentCard key={a.id} agent={a} />
              ))}
              {byTeam(team.id).length === 0 && <div className="text-xs text-dim col-span-2">No staff yet — hire someone into this department.</div>}
            </div>
          </div>
        ))}

        {unassigned.length > 0 && (
          <div className="card overflow-hidden">
            <div className="px-4 py-3 border-b border-edge text-sm font-semibold uppercase tracking-wide text-dim">
              Unassigned ({unassigned.length})
            </div>
            <div className="p-4 grid grid-cols-1 sm:grid-cols-2 gap-3">
              {unassigned.map((a) => (
                <AgentCard key={a.id} agent={a} />
              ))}
            </div>
          </div>
        )}
      </div>

      {editTeam && (
        <TeamModal
          team={editTeam === 'new' ? null : editTeam}
          onClose={() => setEditTeam(null)}
          onSaved={() => {
            setEditTeam(null)
            teams.refetch()
          }}
          onError={setError}
        />
      )}
    </div>
  )
}

function AgentCard({ agent }: { agent: Agent }) {
  return (
    <Link to={`/hiring?agent=${agent.id}`} className="bg-panel-2 border border-edge rounded-lg p-3 hover:border-accent/50 transition-colors block">
      <div className="flex items-center gap-2">
        <span className={`w-2 h-2 rounded-full ${agent.status === 'working' ? 'bg-ok animate-pulse' : agent.status === 'idle' ? 'bg-dim' : 'bg-bad'}`} />
        <span className="text-sm font-medium truncate">{agent.name}</span>
      </div>
      <div className="text-xs text-dim mt-1 truncate">{agent.role || 'generalist'}</div>
      <div className="flex items-center justify-between mt-2">
        <span className="text-[10px] font-mono text-dim truncate">{agent.model}</span>
        <StatusBadge status={agent.status} />
      </div>
      <div className="text-[10px] text-dim mt-1 font-mono">
        {agent.tasks_done} done · {fmtTokens(agent.tokens_in + agent.tokens_out)} tok
      </div>
    </Link>
  )
}

function TeamModal({ team, onClose, onSaved, onError }: { team: Team | null; onClose: () => void; onSaved: () => void; onError: (e: string) => void }) {
  const [name, setName] = useState(team?.name ?? '')
  const [description, setDescription] = useState(team?.description ?? '')
  const [color, setColor] = useState(team?.color ?? teamColors[Math.floor(Math.random() * teamColors.length)])

  const save = async () => {
    try {
      const body = { name, description, color }
      if (team) await api.updateTeam(team.id, body)
      else await api.createTeam(body)
      onSaved()
    } catch (e) {
      onError(String((e as Error).message ?? e))
      onClose()
    }
  }

  const remove = async () => {
    if (!team) return
    try {
      await api.deleteTeam(team.id)
      onSaved()
    } catch (e) {
      onError(String((e as Error).message ?? e))
      onClose()
    }
  }

  return (
    <Modal title={team ? `Edit ${team.name}` : 'New department'} onClose={onClose}>
      <div className="space-y-4">
        <div>
          <label className="label">Name</label>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Marketing" />
        </div>
        <div>
          <label className="label">Description</label>
          <input className="input" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What this department is responsible for" />
        </div>
        <div>
          <label className="label">Color</label>
          <div className="flex gap-2">
            {teamColors.map((c) => (
              <button key={c} className={`w-7 h-7 rounded-md border-2 ${color === c ? 'border-ink' : 'border-transparent'}`} style={{ background: c }} onClick={() => setColor(c)} />
            ))}
          </div>
        </div>
        <div className="flex gap-2 pt-2">
          <button className="btn-primary" disabled={!name.trim()} onClick={save}>
            {team ? 'Save' : 'Create'}
          </button>
          {team && <button className="btn-bad ml-auto" onClick={remove}>Delete</button>}
        </div>
      </div>
    </Modal>
  )
}
