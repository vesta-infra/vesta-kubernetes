import { useState, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import { CreateAppForm } from '../components/CreateAppForm'
import { useUserRole } from '../lib/useRole'

export default function AppsPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const projectFilter = searchParams.get('project') || ''
  const envFilter = searchParams.get('environment') || ''
  const role = useUserRole()

  const [search, setSearch] = useState('')
  const [showCreateApp, setShowCreateApp] = useState(false)

  const params: Record<string, string> = {}
  if (projectFilter) params.project = projectFilter
  if (envFilter) params.environment = envFilter

  const { data, isLoading } = useQuery({
    queryKey: ['apps', projectFilter, envFilter],
    queryFn: () => api.listApps(Object.keys(params).length > 0 ? params : undefined),
  })

  const { data: projects } = useQuery({
    queryKey: ['projects'],
    queryFn: () => api.listProjects(),
  })

  // Collect unique environment names from all apps
  const allEnvironments = useMemo(() => {
    if (!data?.items) return []
    const envSet = new Set<string>()
    data.items.forEach((app: any) => {
      (app.environments || []).forEach((e: any) => {
        envSet.add(typeof e === 'string' ? e : e.name)
      })
    })
    return Array.from(envSet).sort((a, b) => a.localeCompare(b))
  }, [data?.items])

  // Client-side search filter
  const filteredApps = useMemo(() => {
    if (!data?.items) return []
    if (!search.trim()) return data.items
    const q = search.toLowerCase()
    return data.items.filter((app: any) =>
      app.name?.toLowerCase().includes(q) ||
      (app.projectName || app.project || '').toLowerCase().includes(q)
    )
  }, [data?.items, search])

  const updateFilter = (key: string, value: string) => {
    const next = new URLSearchParams(searchParams)
    if (value) next.set(key, value)
    else next.delete(key)
    setSearchParams(next)
  }

  const hasActiveFilters = projectFilter || envFilter

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <p className="text-sm text-text-secondary">
          {filteredApps.length}{filteredApps.length !== (data?.total ?? 0) ? ` / ${data?.total ?? 0}` : ''} application{(data?.total ?? 0) !== 1 ? 's' : ''}
        </p>
        {role !== 'viewer' && (
          <button onClick={() => setShowCreateApp(!showCreateApp)} className="btn-primary text-xs">
            <span className="flex items-center gap-1.5">
              <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
              </svg>
              Create App
            </span>
          </button>
        )}
      </div>

      {showCreateApp && (
        <CreateAppInline onClose={() => setShowCreateApp(false)} />
      )}

      {/* Filters & Search Bar */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[200px] max-w-sm">
          <svg className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-tertiary pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search apps..."
            className="input-field pl-9 w-full"
          />
        </div>
        <select
          value={projectFilter}
          onChange={(e) => updateFilter('project', e.target.value)}
          className="input-field text-xs w-44"
        >
          <option value="">All Projects</option>
          {projects?.items?.map((p: any) => (
            <option key={p.id || p.name} value={p.name}>{p.displayName || p.name}</option>
          ))}
        </select>
        <select
          value={envFilter}
          onChange={(e) => updateFilter('environment', e.target.value)}
          className="input-field text-xs w-44"
        >
          <option value="">All Environments</option>
          {allEnvironments.map((env) => (
            <option key={env} value={env}>{env}</option>
          ))}
        </select>
        {hasActiveFilters && (
          <button
            onClick={() => setSearchParams({})}
            className="text-[11px] text-accent hover:text-accent-glow transition-colors font-mono"
          >
            Clear filters
          </button>
        )}
      </div>

      {isLoading && <Spinner />}

      {!isLoading && filteredApps.length === 0 && (
        <div className="card px-6 py-16 text-center gradient-border">
          <div className="w-12 h-12 rounded-xl bg-surface-3 border border-border flex items-center justify-center mx-auto mb-4">
            <svg className="w-5 h-5 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
            </svg>
          </div>
          <p className="text-sm text-text-secondary font-medium">{search || hasActiveFilters ? 'No matching apps' : 'No apps yet'}</p>
          <p className="text-xs text-text-tertiary mt-1.5">{search || hasActiveFilters ? 'Try adjusting your filters.' : 'Create one from a project.'}</p>
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {filteredApps.map((app: any, i: number) => {
          const envs = (app.environments || []).map((e: any) => (typeof e === 'string' ? e : e.name))
          const subtitle = app.status?.url || app.status?.currentImage || app.spec?.image
          return (
            <Link
              key={app.id}
              to={{ pathname: `/apps/${app.id}`, search: searchParams.toString() }}
              className="card-hover top-sheen p-5 h-full flex flex-col group relative overflow-hidden animate-slide-up"
              style={{ animationDelay: `${i * 0.04}s` }}
            >
              <div className="flex items-start justify-between gap-3">
                <div className="flex items-center gap-3 min-w-0">
                  <div className="w-9 h-9 rounded-lg bg-surface-3 border border-border/80 flex items-center justify-center text-xs font-mono font-semibold text-text-secondary shrink-0 group-hover:text-accent group-hover:bg-accent/10 group-hover:border-accent/25 transition-all duration-300">
                    {app.name?.charAt(0)?.toUpperCase() || 'A'}
                  </div>
                  <div className="min-w-0">
                    <h3 className="text-sm font-semibold text-text-primary truncate group-hover:text-accent transition-colors duration-200">
                      {app.name}
                    </h3>
                    <p className="text-[11px] text-text-tertiary font-mono truncate mt-0.5">
                      {app.projectName || app.project || '—'}
                    </p>
                  </div>
                </div>
                <StatusBadge phase={app.status?.phase} />
              </div>

              {envs.length > 0 && (
                <div className="flex flex-wrap gap-1.5 mt-4">
                  {envs.map((name: string) => (
                    <span key={name} className="chip">{name}</span>
                  ))}
                </div>
              )}

              {/* Footer is bottom-anchored so cards in a row line up whatever they carry.
                  When an app is unhealthy the cause matters more than its image. */}
              <div className="mt-auto flex items-center justify-between gap-3 pt-4 mt-5 border-t border-border/50">
                {app.status?.reason ? (
                  <span
                    className="text-[11px] font-mono text-status-failed truncate"
                    title={app.status.message || app.status.reason}
                  >
                    {app.status.reason}
                  </span>
                ) : (
                  <span className="text-[11px] font-mono text-text-tertiary truncate" title={subtitle || undefined}>
                    {subtitle || 'not deployed'}
                  </span>
                )}
                <svg className="w-3.5 h-3.5 shrink-0 text-text-quaternary group-hover:text-accent group-hover:translate-x-0.5 transition-all duration-300" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
                </svg>
              </div>
            </Link>
          )
        })}
      </div>
    </div>
  )
}

/* ── Create App (inline on Apps page) ── */

function CreateAppInline({ onClose }: { onClose: () => void }) {
  const [selectedProject, setSelectedProject] = useState('')

  const { data: projects } = useQuery({
    queryKey: ['projects'],
    queryFn: () => api.listProjects(),
  })

  const { data: environments } = useQuery({
    queryKey: ['environments', selectedProject],
    queryFn: () => api.listEnvironments(selectedProject),
    enabled: !!selectedProject,
  })

  const hasEnvs = (environments?.items?.length ?? 0) > 0

  return (
    <div className="card p-5 space-y-4 animate-slide-up">
      <h4 className="text-sm font-semibold text-text-secondary">Create App</h4>

      <div>
        <label className="label">Project</label>
        <select
          value={selectedProject}
          onChange={(e) => setSelectedProject(e.target.value)}
          className="input-field w-full"
        >
          <option value="">Select a project…</option>
          {projects?.items?.map((p: any) => (
            <option key={p.id || p.name} value={p.id || p.name}>{p.displayName || p.name}</option>
          ))}
        </select>
      </div>

      {selectedProject && !hasEnvs && (
        <div className="rounded-lg border border-status-pending/30 bg-status-pending-bg px-4 py-3">
          <p className="text-xs text-status-pending">This project has no environments. Add an environment to the project before creating an app.</p>
          <Link to={`/projects/${selectedProject}`} className="text-xs text-accent hover:text-accent-glow mt-1 inline-block font-mono">
            Go to project &rarr;
          </Link>
        </div>
      )}

      {selectedProject && hasEnvs && (
        <CreateAppForm
          projectId={selectedProject}
          environments={environments?.items || []}
          onClose={onClose}
        />
      )}

      {!selectedProject && (
        <div className="flex gap-3 pt-1">
          <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
        </div>
      )}
    </div>
  )
}

function StatusBadge({ phase }: { phase?: string }) {
  const p = phase || 'Pending'
  const styles =
    p === 'Running'
      ? 'bg-status-running-bg text-status-running border border-status-running/10'
      : p === 'Failed' || p === 'CrashLoopBackOff'
      ? 'bg-status-failed-bg text-status-failed border border-status-failed/10'
      : p === 'Degraded'
      ? 'bg-status-degraded-bg text-status-degraded border border-status-degraded/10'
      : p === 'Sleeping' || p === 'Stopped'
      ? 'bg-status-sleeping-bg text-status-sleeping border border-status-sleeping/10'
      : p === 'Deploying'
      ? 'bg-status-pending-bg text-status-pending border border-status-pending/10'
      : 'bg-status-pending-bg text-status-pending border border-status-pending/10'

  return (
    <span className={`status-badge ${styles}`}>
      {p === 'Running' && <span className="inline-block w-1.5 h-1.5 rounded-full bg-current animate-glow-pulse" />}
      {p === 'Deploying' && <span className="inline-block w-1.5 h-1.5 rounded-full bg-current animate-glow-pulse" />}
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
