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
            <div className="flex items-center gap-2 mt-1">
              <span className="text-xs text-text-tertiary font-mono">project: {projectFilter}</span>
              <Link to="/apps" className="text-xs text-accent hover:text-accent-glow transition-colors">
                Clear filter
              </Link>
            </div>
          )}
        </div>
      </div>

      {isLoading && <Spinner />}

      {!isLoading && data?.items?.length === 0 && (
        <div className="card px-6 py-12 text-center">
          <div className="w-10 h-10 rounded-xl bg-surface-3 flex items-center justify-center mx-auto mb-3">
            <svg className="w-5 h-5 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
            </svg>
          </div>
          <p className="text-sm text-text-secondary">No apps yet</p>
          <p className="text-xs text-text-tertiary mt-1">Create one from a project.</p>
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
        {data?.items?.map((app: any) => (
          <Link
            key={app.id}
            to={`/apps/${app.id}`}
            className="card-hover p-5 group"
          >
            <div className="flex items-start justify-between mb-3">
              <div className="flex items-center gap-3">
                <div className="w-8 h-8 rounded-lg bg-surface-3 flex items-center justify-center text-xs font-mono text-text-secondary group-hover:text-accent group-hover:bg-accent/10 transition-colors">
                  {app.name?.charAt(0)?.toUpperCase() || 'A'}
                </div>
                <h3 className="text-sm font-medium text-text-primary group-hover:text-accent transition-colors">
                  {app.name}
                </h3>
              </div>
              <StatusBadge phase={app.status?.phase} />
            </div>
            <p className="text-xs text-text-tertiary font-mono mb-2">
              {app.projectName || app.project || '—'}
            </p>
            {app.environments && app.environments.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mb-2">
                {app.environments.map((e: any) => {
                  const name = typeof e === 'string' ? e : e.name
                  return (
                    <span key={name} className="text-[11px] font-mono bg-surface-3 text-text-tertiary px-2 py-0.5 rounded">
                      {name}
                    </span>
                  )
                })}
              </div>
            )}
            {app.status?.url && (
              <p className="text-xs text-accent/60 truncate">{app.status.url}</p>
            )}
            {app.status?.currentImage && (
              <p className="text-[11px] text-text-tertiary mt-1 truncate font-mono">{app.status.currentImage}</p>
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

function Spinner() {
  return (
    <div className="flex items-center justify-center py-12">
      <svg className="w-5 h-5 animate-spin text-accent" fill="none" viewBox="0 0 24 24">
        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
      </svg>
    </div>
  )
}
