import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'

export default function DashboardPage() {
  const { data: apps } = useQuery({ queryKey: ['apps'], queryFn: () => api.listApps() })
  const { data: projects } = useQuery({ queryKey: ['projects'], queryFn: () => api.listProjects() })

  const runningCount = apps?.items?.filter((a: any) => a.status?.phase === 'Running').length ?? 0

  return (
    <div className="space-y-10">
      {/* Stats row */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-5">
        <StatCard label="Projects" value={projects?.total ?? 0} href="/projects" delay={0} />
        <StatCard label="Applications" value={apps?.total ?? 0} href="/apps" delay={1} />
        <StatCard label="Running" value={runningCount} accent delay={2} />
      </div>

      {/* Recent apps */}
      <section>
        <div className="flex items-center justify-between mb-5">
          <h3 className="section-title">Recent Applications</h3>
          {(apps?.total ?? 0) > 0 && (
            <Link to="/apps" className="text-[11px] text-accent hover:text-accent-glow transition-colors font-mono tracking-wider uppercase">
              View all &rarr;
            </Link>
          )}
        </div>

        {apps?.items?.length === 0 && (
          <div className="card px-6 py-16 text-center gradient-border">
            <div className="w-12 h-12 rounded-xl bg-surface-3 border border-border flex items-center justify-center mx-auto mb-4">
              <svg className="w-5 h-5 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
              </svg>
            </div>
            <p className="text-sm text-text-secondary font-medium">No apps yet</p>
            <p className="text-xs text-text-tertiary mt-1.5">Create a project and deploy your first app.</p>
          </div>
        )}

        <div className="space-y-2">
          {apps?.items?.slice(0, 10).map((app: any, i: number) => (
            <Link
              key={app.id}
              to={`/apps/${app.id}`}
              className="card-hover flex items-center justify-between px-5 py-4 group"
              style={{ animationDelay: `${i * 0.03}s` }}
            >
              <div className="flex items-center gap-4">
                <div className="w-9 h-9 rounded-lg bg-surface-3 border border-border flex items-center justify-center text-xs font-mono font-semibold text-text-tertiary group-hover:text-accent group-hover:bg-accent/10 group-hover:border-accent/20 transition-all duration-300">
                  {app.name?.charAt(0)?.toUpperCase() || 'A'}
                </div>
                <div>
                  <p className="text-sm font-semibold text-text-primary group-hover:text-accent transition-colors duration-200">
                    {app.name}
                  </p>
                  <p className="text-[11px] text-text-tertiary font-mono mt-0.5">
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

function StatCard({ label, value, href, accent, delay }: { label: string; value: number; href?: string; accent?: boolean; delay?: number }) {
  const inner = (
    <div className={`card px-6 py-6 relative overflow-hidden group ${href ? 'hover:border-accent/20 hover:shadow-card-hover transition-all duration-300 cursor-pointer' : ''}`}
      style={delay !== undefined ? { animationDelay: `${delay * 0.08}s` } : undefined}
    >
      {/* Subtle corner accent */}
      {accent && (
        <div className="absolute top-0 right-0 w-20 h-20 bg-gradient-radial from-accent/[0.06] to-transparent" />
      )}
      <p className="text-[10px] font-mono text-text-tertiary uppercase tracking-[0.15em] mb-3">{label}</p>
      <p className={`text-4xl font-display italic ${accent ? 'text-gradient' : 'text-text-primary'}`}>
        {value}
      </p>
      {href && (
        <div className="absolute bottom-4 right-5 text-text-tertiary/0 group-hover:text-text-tertiary transition-all duration-300 translate-x-1 group-hover:translate-x-0">
          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
          </svg>
        </div>
      )}
    </div>
  )
  return href ? <Link to={href}>{inner}</Link> : inner
}

function StatusBadge({ phase }: { phase?: string }) {
  const p = phase || 'Pending'
  const styles =
    p === 'Running'
      ? 'bg-status-running-bg text-status-running border border-status-running/10'
      : p === 'Failed'
      ? 'bg-status-failed-bg text-status-failed border border-status-failed/10'
      : 'bg-status-pending-bg text-status-pending border border-status-pending/10'

  return (
    <span className={`status-badge ${styles}`}>
      {p === 'Running' && <span className="inline-block w-1.5 h-1.5 rounded-full bg-current animate-glow-pulse" />}
      {p}
    </span>
  )
}
