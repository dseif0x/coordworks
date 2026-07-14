import { useState } from 'react'
import { api, type Provider } from '../api'
import { Empty, ErrorNote, Modal, PageTitle, useData } from '../components/ui'

const kindLabels: Record<string, string> = {
  anthropic: 'Anthropic (Messages API)',
  openai: 'OpenAI (Chat Completions)',
  openai_compatible: 'OpenAI-compatible (Ollama, vLLM, OpenRouter, …)',
}

// Settings manages LLM providers. Multiple providers can coexist, so the
// company can mix Claude staff with GPT staff with local-model staff.
export default function Settings() {
  const providers = useData(() => api.providers())
  const [editing, setEditing] = useState<Provider | 'new' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<Record<string, string>>({})

  const test = async (p: Provider) => {
    setTestResult((r) => ({ ...r, [p.id]: '…testing' }))
    try {
      const res = await api.testProvider(p.id)
      setTestResult((r) => ({ ...r, [p.id]: res.ok ? `✓ ok — "${res.reply?.trim()}"` : `✕ ${res.error}` }))
    } catch (e) {
      setTestResult((r) => ({ ...r, [p.id]: `✕ ${String((e as Error).message ?? e)}` }))
    }
  }

  return (
    <div>
      <PageTitle
        title="Settings"
        sub="LLM providers. Every agent is hired onto one provider + model; mix as many as you like."
        action={<button className="btn-primary" onClick={() => setEditing('new')}>+ Add provider</button>}
      />
      <ErrorNote msg={error} />

      {!providers.data || providers.data.length === 0 ? (
        <Empty>No providers yet. Add your Anthropic or OpenAI API key (or a local OpenAI-compatible endpoint) to get started.</Empty>
      ) : (
        <div className="space-y-3 max-w-3xl">
          {providers.data.map((p) => (
            <div key={p.id} className="card p-4">
              <div className="flex items-center gap-3">
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium">{p.name}</div>
                  <div className="text-xs text-dim mt-0.5">
                    {kindLabels[p.kind]} {p.base_url && <span className="font-mono">· {p.base_url}</span>}
                    {' · '}
                    {p.has_api_key ? <span className="text-ok">key set</span> : <span className="text-warn">no key</span>}
                  </div>
                  {p.models.length > 0 && (
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      {p.models.map((m) => (
                        <span key={m} className={`chip font-mono ${m === p.default_model ? 'border-accent/50 bg-accent/10 text-accent' : 'border-edge bg-panel-2 text-dim'}`}>{m}</span>
                      ))}
                    </div>
                  )}
                </div>
                <button className="btn text-xs" onClick={() => test(p)}>Test</button>
                <button className="btn text-xs" onClick={() => setEditing(p)}>Edit</button>
                <button
                  className="btn-bad text-xs"
                  onClick={() => api.deleteProvider(p.id).then(providers.refetch).catch((e) => setError(String(e.message ?? e)))}
                >
                  Delete
                </button>
              </div>
              {testResult[p.id] && (
                <div className={`text-xs mt-2 ${testResult[p.id].startsWith('✓') ? 'text-ok' : testResult[p.id].startsWith('✕') ? 'text-bad' : 'text-dim'}`}>
                  {testResult[p.id]}
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {editing && (
        <ProviderModal
          provider={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            providers.refetch()
          }}
          onError={(e) => {
            setError(e)
            setEditing(null)
          }}
        />
      )}
    </div>
  )
}

function ProviderModal({ provider, onClose, onSaved, onError }: { provider: Provider | null; onClose: () => void; onSaved: () => void; onError: (e: string) => void }) {
  const [form, setForm] = useState({
    name: provider?.name ?? '',
    kind: provider?.kind ?? 'anthropic',
    base_url: provider?.base_url ?? '',
    api_key: '',
    models: provider?.models.join(', ') ?? '',
    default_model: provider?.default_model ?? '',
  })

  const save = async () => {
    const body = {
      ...form,
      models: form.models.split(',').map((m) => m.trim()).filter(Boolean),
    }
    try {
      if (provider) await api.updateProvider(provider.id, body)
      else await api.createProvider(body)
      onSaved()
    } catch (e) {
      onError(String((e as Error).message ?? e))
    }
  }

  const placeholderModels: Record<string, string> = {
    anthropic: 'claude-sonnet-4-5, claude-opus-4-6',
    openai: 'gpt-5.2, gpt-5-mini',
    openai_compatible: 'llama3.3:70b, qwen2.5-coder',
  }

  return (
    <Modal title={provider ? `Edit ${provider.name}` : 'Add provider'} onClose={onClose}>
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="label">Name</label>
            <input className="input" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="e.g. Anthropic prod" />
          </div>
          <div>
            <label className="label">Kind</label>
            <select className="input" value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value as Provider['kind'] })}>
              {Object.entries(kindLabels).map(([k, v]) => (
                <option key={k} value={k}>{v}</option>
              ))}
            </select>
          </div>
        </div>
        {form.kind !== 'anthropic' || form.base_url ? (
          <div>
            <label className="label">Base URL {form.kind === 'openai_compatible' ? '(required)' : '(optional override)'}</label>
            <input className="input font-mono" value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} placeholder="e.g. http://localhost:11434/v1" />
          </div>
        ) : null}
        <div>
          <label className="label">API key {provider?.has_api_key && '(leave empty to keep current)'}</label>
          <input className="input font-mono" type="password" value={form.api_key} onChange={(e) => setForm({ ...form, api_key: e.target.value })} placeholder="sk-…" />
          <p className="text-xs text-dim mt-1">Stored server-side, never sent back to the browser.</p>
        </div>
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="label">Models (comma-separated)</label>
            <input className="input font-mono" value={form.models} onChange={(e) => setForm({ ...form, models: e.target.value })} placeholder={placeholderModels[form.kind]} />
          </div>
          <div>
            <label className="label">Default model</label>
            <input className="input font-mono" value={form.default_model} onChange={(e) => setForm({ ...form, default_model: e.target.value })} placeholder="used for test calls" />
          </div>
        </div>
        <button className="btn-primary" disabled={!form.name.trim()} onClick={save}>{provider ? 'Save' : 'Add provider'}</button>
      </div>
    </Modal>
  )
}
