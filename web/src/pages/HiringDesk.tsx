import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, type Agent } from '../api'
import { Empty, ErrorNote, PageTitle, StatusBadge, useData } from '../components/ui'

const emptyForm = {
  name: '',
  role: '',
  team_id: '',
  provider_id: '',
  model: '',
  persona: '',
  autonomy: 'approval_required' as string,
  runner_selector: '',
}

// HiringDesk creates and edits agents ("employees"). Hiring picks the brain:
// any configured provider + model, so OpenAI and Anthropic staff mix freely.
export default function HiringDesk() {
  const teams = useData(() => api.teams())
  const providers = useData(() => api.providers())
  const agents = useData(() => api.agents())
  const [params, setParams] = useSearchParams()
  const [form, setForm] = useState({ ...emptyForm })
  const [editing, setEditing] = useState<Agent | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  // Deep link: /hiring?agent=<id> opens that agent for editing.
  useEffect(() => {
    const id = params.get('agent')
    if (id && agents.data) {
      const a = agents.data.find((x) => x.id === id)
      if (a) startEdit(a)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params, agents.data])

  const startEdit = (a: Agent) => {
    setEditing(a)
    setForm({
      name: a.name, role: a.role, team_id: a.team_id, provider_id: a.provider_id,
      model: a.model, persona: a.persona, autonomy: a.autonomy, runner_selector: a.runner_selector,
    })
  }

  const reset = () => {
    setEditing(null)
    setForm({ ...emptyForm })
    setParams({})
  }

  const provider = providers.data?.find((p) => p.id === form.provider_id)

  const save = async () => {
    setError(null)
    try {
      if (editing) await api.updateAgent(editing.id, form)
      else await api.createAgent(form)
      agents.refetch()
      reset()
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
    } catch (e) {
      setError(String((e as Error).message ?? e))
    }
  }

  const fire = async (a: Agent) => {
    setError(null)
    try {
      await api.deleteAgent(a.id)
      agents.refetch()
      if (editing?.id === a.id) reset()
    } catch (e) {
      setError(String((e as Error).message ?? e))
    }
  }

  return (
    <div>
      <PageTitle title="Hiring Desk" sub="Hire AI employees: pick a role, a team, and the model that powers them." />
      <ErrorNote msg={error} />
      {saved && <div className="mb-4 px-4 py-2 rounded-lg border border-ok/50 bg-ok/10 text-ok text-sm">Saved.</div>}

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
        <section className="card p-5 self-start">
          <h2 className="text-sm font-semibold mb-4">{editing ? `Edit ${editing.name}` : '+ Hire staff'}</h2>
          {providers.data?.length === 0 && (
            <div className="mb-4 px-4 py-2 rounded-lg border border-warn/50 bg-warn/10 text-warn text-sm">
              No LLM providers configured yet — add one in Settings first.
            </div>
          )}
          <div className="space-y-4">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label className="label">Name</label>
                <input className="input" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="e.g. Avery" />
              </div>
              <div>
                <label className="label">Role</label>
                <input className="input" value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value })} placeholder="e.g. Content Writer" />
              </div>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label className="label">Department</label>
                <select className="input" value={form.team_id} onChange={(e) => setForm({ ...form, team_id: e.target.value })}>
                  <option value="">— none —</option>
                  {teams.data?.map((t) => (
                    <option key={t.id} value={t.id}>{t.name}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="label">Autonomy</label>
                <select className="input" value={form.autonomy} onChange={(e) => setForm({ ...form, autonomy: e.target.value })}>
                  <option value="approval_required">Plans need my approval</option>
                  <option value="autonomous">Autonomous (auto-approve)</option>
                </select>
              </div>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label className="label">Provider</label>
                <select className="input" value={form.provider_id} onChange={(e) => setForm({ ...form, provider_id: e.target.value, model: '' })}>
                  <option value="">— select —</option>
                  {providers.data?.map((p) => (
                    <option key={p.id} value={p.id}>{p.name} ({p.kind})</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="label">Model</label>
                {provider && provider.models.length > 0 ? (
                  <select className="input" value={form.model} onChange={(e) => setForm({ ...form, model: e.target.value })}>
                    <option value="">— select —</option>
                    {provider.models.map((m) => (
                      <option key={m} value={m}>{m}</option>
                    ))}
                  </select>
                ) : (
                  <input className="input" value={form.model} onChange={(e) => setForm({ ...form, model: e.target.value })} placeholder={provider?.default_model || 'model id'} />
                )}
              </div>
            </div>
            <div>
              <label className="label">Persona / standing instructions</label>
              <textarea className="input" rows={4} value={form.persona} onChange={(e) => setForm({ ...form, persona: e.target.value })} placeholder="Voice, expertise, constraints. e.g. 'You are a meticulous senior copywriter. Always propose 3 variants…'" />
            </div>
            <div>
              <label className="label">Runner selector (optional)</label>
              <input className="input font-mono" value={form.runner_selector} onChange={(e) => setForm({ ...form, runner_selector: e.target.value })} placeholder="e.g. runtime=docker or gpu=true — empty runs anywhere" />
              <p className="text-xs text-dim mt-1">Pin this agent's work to runners with matching labels (docker / k8s / bare metal).</p>
            </div>
            <div className="flex gap-2">
              <button className="btn-primary" disabled={!form.name || !form.provider_id || !form.model} onClick={save}>
                {editing ? 'Save changes' : 'Hire'}
              </button>
              {editing && <button className="btn" onClick={reset}>Cancel</button>}
            </div>
          </div>
        </section>

        <section>
          <h2 className="text-sm font-semibold mb-3">Roster ({agents.data?.length ?? 0})</h2>
          {!agents.data || agents.data.length === 0 ? (
            <Empty>Nobody hired yet.</Empty>
          ) : (
            <div className="space-y-2">
              {agents.data.map((a) => (
                <div key={a.id} className="card p-3 flex items-center gap-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium">{a.name}</span>
                      <span className="text-xs text-dim">{a.role}</span>
                      <StatusBadge status={a.status} />
                    </div>
                    <div className="text-[11px] font-mono text-dim mt-0.5 truncate">
                      {providers.data?.find((p) => p.id === a.provider_id)?.name ?? '?'} / {a.model}
                      {a.runner_selector && ` · ${a.runner_selector}`}
                      {a.autonomy === 'autonomous' && ' · autonomous'}
                    </div>
                  </div>
                  <button className="btn text-xs" onClick={() => startEdit(a)}>Edit</button>
                  <button className="btn-bad text-xs" onClick={() => fire(a)}>Fire</button>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
