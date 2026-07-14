import { api, timeAgo } from '../api'
import { useLive } from '../ws'
import { Empty, PageTitle, useData } from '../components/ui'

// Runners shows the distributed execution fleet: embedded, bare metal,
// Docker, Kubernetes — anything that registered with the control plane.
export default function Runners() {
  const runners = useData(() => api.runners())
  useLive(['runner'], runners.refetch)

  return (
    <div>
      <PageTitle
        title="Runners"
        sub="Execution nodes that carry out agent work. Start more anywhere with the coordworks-runner binary or container."
      />

      {!runners.data || runners.data.length === 0 ? (
        <Empty>No runners registered. The embedded runner starts with the server unless disabled.</Empty>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {runners.data.map((r) => (
            <div key={r.id} className={`card p-4 ${r.online ? '' : 'opacity-60'}`}>
              <div className="flex items-center gap-2">
                <span className={`w-2.5 h-2.5 rounded-full ${r.online ? 'bg-ok' : 'bg-bad'}`} />
                <span className="text-sm font-medium flex-1 truncate">{r.name}</span>
                {r.embedded && <span className="chip border-accent/40 bg-accent/10 text-accent">embedded</span>}
              </div>
              <div className="mt-3 flex flex-wrap gap-1.5">
                {Object.entries(r.labels ?? {}).map(([k, v]) => (
                  <span key={k} className="chip border-edge bg-panel-2 text-dim font-mono">{k}={v}</span>
                ))}
              </div>
              <div className="mt-3 flex items-center justify-between">
                <span className="text-[11px] text-dim">last seen {timeAgo(r.last_seen)}</span>
                {!r.online && !r.embedded && (
                  <button className="btn-bad text-xs" onClick={() => api.deleteRunner(r.id).then(runners.refetch)}>
                    Remove
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      <div className="card p-5 mt-8 max-w-3xl">
        <h2 className="text-sm font-semibold mb-2">Add a runner</h2>
        <p className="text-xs text-dim mb-3">
          Runners authenticate with the shared runner token and advertise labels; agents with a runner selector
          (e.g. <code className="font-mono text-accent">runtime=docker</code>) only run on matching nodes.
        </p>
        <pre className="bg-panel-2 border border-edge rounded-lg p-4 text-xs font-mono overflow-auto">
{`# bare metal
coordworks-runner --server https://your-server:8080 --token $RUNNER_TOKEN --labels runtime=baremetal

# docker
docker run -e COORDWORKS_SERVER_URL=... -e COORDWORKS_RUNNER_TOKEN=... \\
  -e COORDWORKS_RUNNER_LABELS=runtime=docker ghcr.io/dseif0x/coordworks-runner

# kubernetes — see deploy/k8s/runner-deployment.yaml`}
        </pre>
      </div>
    </div>
  )
}
