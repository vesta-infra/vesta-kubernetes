import { useState, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
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
        {filteredApps.map((app: any, i: number) => (
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

/* ── Create App (inline on Apps page) ── */

interface EnvConfig {
  name: string
  replicas: number
  podSize?: string
  autoscale?: { enabled: boolean; minReplicas: number; maxReplicas: number; targetCPU: number }
}

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

function CreateAppForm({ projectId, environments, onClose }: { projectId: string; environments: any[]; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [envConfigs, setEnvConfigs] = useState<Record<string, EnvConfig>>({})
  const [imageRepo, setImageRepo] = useState('')
  const [imageTag, setImageTag] = useState('')
  const [pullPolicy, setPullPolicy] = useState('IfNotPresent')
  const [port, setPort] = useState('3000')
  const [pullSecrets, setPullSecrets] = useState<string[]>([])

  const { data: registrySecrets } = useQuery({
    queryKey: ['registrySecrets'],
    queryFn: () => api.listRegistrySecrets(),
  })

  const { data: podSizes } = useQuery({
    queryKey: ['podSizes'],
    queryFn: () => api.listPodSizes(),
  })

  const mutation = useMutation({
    mutationFn: (data: any) => api.createApp(projectId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projectApps', projectId] })
      queryClient.invalidateQueries({ queryKey: ['apps'] })
      onClose()
    },
  })

  const toggleEnv = (envName: string) => {
    setEnvConfigs((prev) => {
      if (prev[envName]) {
        const { [envName]: _, ...rest } = prev
        return rest
      }
      return { ...prev, [envName]: { name: envName, replicas: 1, podSize: '' } }
    })
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    mutation.mutate({
      name,
      environments: Object.values(envConfigs).map(cfg => ({
        name: cfg.name,
        replicas: cfg.replicas,
        ...(cfg.autoscale && { autoscale: cfg.autoscale }),
        ...(cfg.podSize && { resources: { size: cfg.podSize } }),
      })),
      image: {
        repository: imageRepo || undefined,
        tag: imageTag || undefined,
        pullPolicy,
        ...(pullSecrets.length > 0 && { imagePullSecrets: pullSecrets.map(n => ({ name: n })) }),
      },
      runtime: { port: Number.parseInt(port) || 3000 },
    })
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <div>
        <label className="label">App Name</label>
        <input value={name} onChange={(e) => setName(e.target.value)} className="input-field" required placeholder="my-app" />
      </div>

      {environments.length > 0 && (
        <div>
          <label className="label">Environments</label>
          <div className="space-y-2">
            {environments.map((env: any) => {
              const isSelected = !!envConfigs[env.name]
              const config = envConfigs[env.name]
              return (
                <div key={env.name} className={`rounded-lg border p-3 transition-all ${isSelected ? 'border-accent bg-accent/5' : 'border-border bg-surface-1'}`}>
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input type="checkbox" checked={isSelected} onChange={() => toggleEnv(env.name)} className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20" />
                    <span className={`text-sm font-mono ${isSelected ? 'text-accent' : 'text-text-secondary'}`}>{env.name}</span>
                  </label>
                  {isSelected && config && (
                    <div className="mt-3 pl-6 flex items-center gap-4 flex-wrap">
                      <div>
                        <label className="text-xs text-text-tertiary">Pod Size</label>
                        <select value={config.podSize || ''} onChange={(e) => setEnvConfigs(prev => ({ ...prev, [env.name]: { ...prev[env.name], podSize: e.target.value } }))} className="input-field w-28 mt-1">
                          <option value="">Default</option>
                          {podSizes?.items?.map((s: any) => (<option key={s.name} value={s.name}>{s.name}</option>))}
                        </select>
                      </div>
                      <div>
                        <label className="text-xs text-text-tertiary">Replicas</label>
                        <input type="number" min="0" value={config.replicas} onChange={(e) => setEnvConfigs(prev => ({ ...prev, [env.name]: { ...prev[env.name], replicas: Number.parseInt(e.target.value) || 0 } }))} className="input-field w-20 mt-1" />
                      </div>
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}

      <div className="grid grid-cols-3 gap-4">
        <div className="col-span-2">
          <label className="label">Image Repository</label>
          <input value={imageRepo} onChange={(e) => setImageRepo(e.target.value)} className="input-field" placeholder="registry.example.com/org/app" />
        </div>
        <div>
          <label className="label">Tag</label>
          <input value={imageTag} onChange={(e) => setImageTag(e.target.value)} className="input-field" placeholder="latest" />
        </div>
      </div>

      <div className="flex items-center gap-4">
        <div>
          <label className="label">Pull Policy</label>
          <select value={pullPolicy} onChange={(e) => setPullPolicy(e.target.value)} className="input-field w-40">
            <option value="IfNotPresent">IfNotPresent</option>
            <option value="Always">Always</option>
            <option value="Never">Never</option>
          </select>
        </div>
        <div>
          <label className="label">Port</label>
          <input type="number" value={port} onChange={(e) => setPort(e.target.value)} className="input-field w-32" />
        </div>
      </div>

      <div>
        <label className="label mb-2">Image Pull Secrets</label>
        <div className="flex flex-wrap gap-2 mb-2">
          {pullSecrets.map(n => (
            <span key={n} className="inline-flex items-center gap-1.5 px-2.5 py-1 bg-surface-2 border border-border rounded-lg text-xs font-mono text-text-secondary">
              {n}
              <button type="button" onClick={() => setPullSecrets(prev => prev.filter(x => x !== n))} className="text-text-tertiary hover:text-status-failed">&times;</button>
            </span>
          ))}
        </div>
        {(registrySecrets?.items?.filter((s: any) => !pullSecrets.includes(s.name))?.length ?? 0) > 0 && (
          <select value="" onChange={(e) => { if (e.target.value) setPullSecrets(prev => [...prev, e.target.value]) }} className="input-field w-48">
            <option value="">+ Add pull secret</option>
            {registrySecrets?.items?.filter((s: any) => !pullSecrets.includes(s.name)).map((s: any) => (<option key={s.name} value={s.name}>{s.name}</option>))}
          </select>
        )}
      </div>

      <div className="flex gap-3 pt-1">
        <button type="submit" disabled={mutation.isPending} className="btn-primary">
          {mutation.isPending ? 'Creating...' : 'Create App'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
      </div>
      {mutation.isError && (
        <p className="text-status-failed text-xs">{mutation.error?.message}</p>
      )}
    </form>
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
