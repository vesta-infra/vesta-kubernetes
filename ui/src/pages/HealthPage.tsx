import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'

export default function HealthPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['healthDashboard'],
    queryFn: () => api.getHealthDashboard(),
    refetchInterval: 15000,
  })

  const summary = data?.summary || {}
  const apps = data?.apps || []

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-display italic text-text-primary">Health Dashboard</h2>

      {/* Summary cards */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <SummaryCard label="Running" value={summary.Running || 0} color="text-status-running" />
        <SummaryCard label="Pending" value={summary.Pending || 0} color="text-status-pending" />
        <SummaryCard label="Failed" value={summary.Failed || 0} color="text-status-failed" />
        <SummaryCard label="Sleeping" value={summary.Sleeping || 0} color="text-text-tertiary" />
      </div>

      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <svg className="w-5 h-5 animate-spin text-accent" fill="none" viewBox="0 0 24 24">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
        </div>
      )}

      {apps.length === 0 && !isLoading && (
        <div className="card px-6 py-12 text-center">
          <p className="text-sm text-text-tertiary">No apps found</p>
        </div>
      )}

      {/* App health table */}
      {apps.length > 0 && (
        <div className="card overflow-hidden">
          <table className="w-full">
            <thead>
              <tr className="border-b border-border bg-surface-1">
                <th className="text-left px-5 py-3 text-[11px] font-mono text-text-tertiary uppercase tracking-wider">App</th>
                <th className="text-left px-5 py-3 text-[11px] font-mono text-text-tertiary uppercase tracking-wider">Project</th>
                <th className="text-left px-5 py-3 text-[11px] font-mono text-text-tertiary uppercase tracking-wider">Status</th>
                <th className="text-left px-5 py-3 text-[11px] font-mono text-text-tertiary uppercase tracking-wider">Pods</th>
                <th className="text-left px-5 py-3 text-[11px] font-mono text-text-tertiary uppercase tracking-wider">Restarts</th>
                <th className="text-left px-5 py-3 text-[11px] font-mono text-text-tertiary uppercase tracking-wider">Last Deploy</th>
              </tr>
            </thead>
            <tbody>
              {apps.map((app: any) => (
                <tr key={app.id} className="border-b border-border-subtle hover:bg-surface-1/50 transition-colors">
                  <td className="px-5 py-3">
                    <Link to={`/apps/${app.id}`} className="text-sm font-medium text-text-primary hover:text-accent transition-colors">
                      {app.name}
                    </Link>
                    {app.sleepMode && (
                      <span className="ml-2 text-[10px] font-mono bg-surface-3 text-text-tertiary px-1.5 py-0.5 rounded">sleep</span>
                    )}
                  </td>
                  <td className="px-5 py-3">
                    <span className="text-xs font-mono text-text-tertiary">{app.project}</span>
                  </td>
                  <td className="px-5 py-3">
                    <PhaseIndicator phase={app.phase} />
                  </td>
                  <td className="px-5 py-3">
                    <PodBar ready={app.readyPods} total={app.totalPods} />
                  </td>
                  <td className="px-5 py-3">
                    <span className={`text-xs font-mono ${app.restarts > 5 ? 'text-status-failed' : app.restarts > 0 ? 'text-status-pending' : 'text-text-tertiary'}`}>
                      {app.restarts}
                    </span>
                  </td>
                  <td className="px-5 py-3">
                    <span className="text-xs text-text-tertiary">
                      {app.lastDeployedAt ? new Date(app.lastDeployedAt).toLocaleDateString() : '—'}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function SummaryCard({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div className="card px-5 py-4">
      <p className="text-[10px] font-mono text-text-tertiary uppercase tracking-[0.15em] mb-2">{label}</p>
      <p className={`text-3xl font-display italic ${color}`}>{value}</p>
    </div>
  )
}

function PhaseIndicator({ phase }: { phase: string }) {
  const styles =
    phase === 'Running'
      ? 'bg-status-running-bg text-status-running border-status-running/10'
      : phase === 'Failed'
      ? 'bg-status-failed-bg text-status-failed border-status-failed/10'
      : phase === 'Sleeping'
      ? 'bg-surface-3 text-text-tertiary border-border'
      : 'bg-status-pending-bg text-status-pending border-status-pending/10'

  return (
    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-mono border ${styles}`}>
      {phase === 'Running' && <span className="inline-block w-1.5 h-1.5 rounded-full bg-current animate-glow-pulse" />}
      {phase}
    </span>
  )
}

function PodBar({ ready, total }: { ready: number; total: number }) {
  if (total === 0) return <span className="text-xs text-text-tertiary font-mono">0/0</span>

  const pct = Math.round((ready / total) * 100)
  const color = pct === 100 ? 'bg-status-running' : pct > 50 ? 'bg-status-pending' : 'bg-status-failed'

  return (
    <div className="flex items-center gap-2">
      <div className="w-16 h-1.5 rounded-full bg-surface-3 overflow-hidden">
        <div className={`h-full rounded-full ${color} transition-all`} style={{ width: `${pct}%` }} />
      </div>
      <span className="text-xs font-mono text-text-secondary">{ready}/{total}</span>
    </div>
  )
}
