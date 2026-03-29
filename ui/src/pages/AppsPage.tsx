import { useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'

export default function AppsPage() {
  const [searchParams] = useSearchParams()
  const projectFilter = searchParams.get('project') || undefined

  const { data, isLoading } = useQuery({
    queryKey: ['apps', projectFilter],
    queryFn: () => api.listApps(projectFilter ? { project: projectFilter } : undefined),
  })

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm text-text-secondary">
            {data?.total ?? 0} application{(data?.total ?? 0) !== 1 ? 's' : ''}
          </p>
          {projectFilter && (
            <div className="flex items-center gap-2 mt-1.5">
              <span className="text-[11px] text-text-tertiary font-mono bg-surface-3 px-2 py-0.5 rounded">project: {projectFilter}</span>
              <Link to="/apps" className="text-[11px] text-accent hover:text-accent-glow transition-colors font-mono">
                Clear
              </Link>
            </div>
          )}
        </div>
      </div>

      {isLoading && <Spinner />}

      {!isLoading && data?.items?.length === 0 && (
        <div className="card px-6 py-16 text-center gradient-border">
          <div className="w-12 h-12 rounded-xl bg-surface-3 border border-border flex items-center justify-center mx-auto mb-4">
            <svg className="w-5 h-5 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
            </svg>
          </div>
          <p className="text-sm text-text-secondary font-medium">No apps yet</p>
          <p className="text-xs text-text-tertiary mt-1.5">Create one from a project.</p>
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {data?.items?.map((app: any, i: number) => (
          <Link
            key={app.id}
            to={`/apps/${app.id}`}
            className="card-hover p-5 group relative overflow-hidden"
            style={{ animationDelay: `${i * 0.03}s` }}
          >
            {/* Top accent line on hover */}
            <div className="absolute top-0 left-0 right-0 h-px bg-gradient-to-r from-transparent via-accent/0 to-transparent group-hover:via-accent/30 transition-all duration-500" />

            <div className="flex items-start justify-between mb-3">
              <div className="flex items-center gap-3">
                <div className="w-9 h-9 rounded-lg bg-surface-3 border border-border flex items-center justify-center text-xs font-mono font-semibold text-text-tertiary group-hover:text-accent group-hover:bg-accent/10 group-hover:border-accent/20 transition-all duration-300">
                  {app.name?.charAt(0)?.toUpperCase() || 'A'}
                </div>
                <h3 className="text-sm font-semibold text-text-primary group-hover:text-accent transition-colors duration-200">
                  {app.name}
                </h3>
              </div>
              <StatusBadge phase={app.status?.phase} />
            </div>
            <p className="text-[11px] text-text-tertiary font-mono mb-2.5">
              {app.projectName || app.project || '—'}
            </p>
            {app.environments && app.environments.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mb-2.5">
                {app.environments.map((e: any) => {
                  const name = typeof e === 'string' ? e : e.name
                  return (
                    <span key={name} className="text-[10px] font-mono bg-surface-3/80 text-text-tertiary px-2 py-0.5 rounded border border-border/50">
                      {name}
                    </span>
                  )
                })}
              </div>
            )}
            {app.status?.url && (
              <p className="text-[11px] text-accent/50 truncate">{app.status.url}</p>
            )}
            {app.status?.currentImage && (
              <p className="text-[10px] text-text-tertiary mt-1.5 truncate font-mono">{app.status.currentImage}</p>
            )}
          </Link>
        ))}
      </div>
    </div>
  )
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

function Spinner() {
  return (
    <div className="flex items-center justify-center py-16">
      <div className="relative">
        <div className="w-8 h-8 rounded-lg bg-accent/10 border border-accent/20 flex items-center justify-center animate-glow-pulse">
          <div className="w-2.5 h-2.5 rounded bg-accent" />
        </div>
      </div>
    </div>
  )
}
