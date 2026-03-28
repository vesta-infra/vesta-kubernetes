import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'

export default function DashboardPage() {
  const { data: apps } = useQuery({ queryKey: ['apps'], queryFn: () => api.listApps() })
  const { data: projects } = useQuery({ queryKey: ['projects'], queryFn: () => api.listProjects() })

  const runningCount = apps?.items?.filter((a: any) => a.status?.phase === 'Running').length ?? 0

  return (
    <div className="space-y-8">
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <StatCard label="Projects" value={projects?.total ?? 0} href="/projects" />
        <StatCard label="Applications" value={apps?.total ?? 0} href="/apps" />
        <StatCard label="Running" value={runningCount} accent />
      </div>

      <section>
        <div className="flex items-center justify-between mb-4">
          <h3 className="section-title">Recent Applications</h3>
          {(apps?.total ?? 0) > 0 && (
            <Link to="/apps" className="text-xs text-accent hover:text-accent-glow transition-colors font-mono">
              View all &rarr;
            </Link>
          )}
        </div>

        {apps?.items?.length === 0 && (
          <div className="card px-6 py-12 text-center">
            <div className="w-10 h-10 rounded-xl bg-surface-3 flex items-center justify-center mx-auto mb-3">
              <svg className="w-5 h-5 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
              </svg>
            </div>
            <p className="text-sm text-text-secondary">No apps yet</p>
            <p className="text-xs text-text-tertiary mt-1">Create a project and deploy your first app.</p>
          </div>
        )}

        <div className="space-y-1.5">
          {apps?.items?.slice(0, 10).map((app: any) => (
            <Link
              key={app.id}
              to={`/apps/${app.id}`}
              className="card-hover flex items-center justify-between px-5 py-3.5 group"
            >
              <div className="flex items-center gap-4">
                <div className="w-8 h-8 rounded-lg bg-surface-3 flex items-center justify-center text-xs font-mono text-text-secondary group-hover:text-accent group-hover:bg-accent/10 transition-colors">
                  {app.name?.charAt(0)?.toUpperCase() || 'A'}
                </div>
                <div>
                  <p className="text-sm font-medium text-text-primary group-hover:text-accent transition-colors">
                    {app.name}
                  </p>
                  <p className="text-xs text-text-tertiary font-mono">
                    {app.projectName || app.project || '—'}
                  </p>
                </div>
              </div>
              <StatusBadge phase={app.status?.phase} />
            </Link>
          ))}
        </div>
      </section>
    </div>
  )
}

function StatCard({ label, value, href, accent }: { label: string; value: number; href?: string; accent?: boolean }) {
  const inner = (
    <div className={`card px-5 py-5 ${href ? 'hover:border-border-hover transition-all cursor-pointer' : ''}`}>
      <p className="text-xs font-mono text-text-tertiary uppercase tracking-wider">{label}</p>
      <p className={`text-3xl font-display mt-2 ${accent ? 'text-accent' : 'text-text-primary'}`}>
        {value}
      </p>
    </div>
  )
  return href ? <Link to={href}>{inner}</Link> : inner
}

function StatusBadge({ phase }: { phase?: string }) {
  const p = phase || 'Pending'
  const styles =
    p === 'Running'
      ? 'bg-status-running-bg text-status-running'
      : p === 'Failed'
      ? 'bg-status-failed-bg text-status-failed'
      : 'bg-status-pending-bg text-status-pending'

  return (
    <span className={`status-badge ${styles}`}>
      {p === 'Running' && <span className="inline-block w-1.5 h-1.5 rounded-full bg-current mr-1.5 animate-glow-pulse" />}
      {p}
    </span>
  )
}
