import { useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

export default function ProjectDetailPage() {
  const { projectId } = useParams<{ projectId: string }>()

  const { data: project, isLoading } = useQuery({
    queryKey: ['project', projectId],
    queryFn: () => api.getProject(projectId!),
    enabled: !!projectId,
  })

  const { data: environments } = useQuery({
    queryKey: ['environments', projectId],
    queryFn: () => api.listEnvironments(projectId!),
    enabled: !!projectId,
  })

  const { data: apps } = useQuery({
    queryKey: ['projectApps', projectId],
    queryFn: () => api.listProjectApps(projectId!),
    enabled: !!projectId,
  })

  const [showCreateEnv, setShowCreateEnv] = useState(false)
  const [showCreateApp, setShowCreateApp] = useState(false)

  const [showEditProject, setShowEditProject] = useState(false)

  if (isLoading) return <Spinner />

  if (!project) {
    return (
      <div className="card px-6 py-12 text-center">
        <p className="text-sm text-text-secondary">Project not found</p>
        <Link to="/projects" className="text-xs text-accent mt-2 inline-block">&larr; Back to projects</Link>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-4">
          <Link to="/projects" className="text-text-tertiary hover:text-text-secondary transition-colors">
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M15 19l-7-7 7-7" />
            </svg>
          </Link>
          <div>
            <h2 className="text-xl font-display text-text-primary">{project.displayName || project.name}</h2>
            <div className="flex items-center gap-2 mt-0.5">
              <span className="text-xs text-text-tertiary font-mono">{project.name}</span>
              {project.teamName && (
                <span className="text-[11px] font-mono bg-surface-3 text-text-tertiary px-2 py-0.5 rounded">
                  {project.teamName}
                </span>
              )}
            </div>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <div className="lg:col-span-2 space-y-6">
          <section>
            <div className="flex items-center justify-between mb-3">
              <h3 className="section-title">Environments</h3>
              <button onClick={() => setShowCreateEnv(!showCreateEnv)} className="btn-ghost text-xs">
                <span className="flex items-center gap-1.5">
                  <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
                  </svg>
                  Add Environment
                </span>
              </button>
            </div>

            {showCreateEnv && (
              <CreateEnvironmentForm
                projectId={projectId!}
                onClose={() => setShowCreateEnv(false)}
              />
            )}

            {(!environments?.items || environments.items.length === 0) ? (
              <div className="card px-5 py-8 text-center">
                <p className="text-sm text-text-tertiary">No environments yet</p>
              </div>
            ) : (
              <div className="space-y-1.5">
                {environments.items.map((env: any) => (
                  <EnvironmentRow
                    key={env.name}
                    env={env}
                    projectId={projectId!}
                  />
                ))}
              </div>
            )}
          </section>

          <section>
            <div className="flex items-center justify-between mb-3">
              <h3 className="section-title">Apps</h3>
              {environments?.items && environments.items.length > 0 ? (
                <button onClick={() => setShowCreateApp(!showCreateApp)} className="btn-ghost text-xs">
                  <span className="flex items-center gap-1.5">
                    <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
                    </svg>
                    Create App
                  </span>
                </button>
              ) : (
                <span className="text-[11px] text-text-tertiary">Add an environment first</span>
              )}
            </div>

            {showCreateApp && (
              <CreateAppForm
                projectId={projectId!}
                environments={environments?.items || []}
                onClose={() => setShowCreateApp(false)}
              />
            )}

            {(!apps?.items || apps.items.length === 0) ? (
              <div className="card px-5 py-8 text-center">
                <p className="text-sm text-text-tertiary">No apps in this project</p>
              </div>
            ) : (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                {apps.items.map((app: any) => (
                  <Link
                    key={app.id}
                    to={`/apps/${app.id}`}
                    className="card-hover p-5 group"
                  >
                    <div className="flex items-start justify-between mb-2">
                      <div className="flex items-center gap-3">
                        <div className="w-8 h-8 rounded-lg bg-surface-3 flex items-center justify-center text-xs font-mono text-text-secondary group-hover:text-accent group-hover:bg-accent/10 transition-colors">
                          {app.name?.charAt(0)?.toUpperCase() || 'A'}
                        </div>
                        <h4 className="text-sm font-medium text-text-primary group-hover:text-accent transition-colors">
                          {app.name}
                        </h4>
                      </div>
                      <StatusBadge phase={app.status?.phase} />
                    </div>
                    {app.environments && (
                      <div className="flex flex-wrap gap-1.5 mt-2">
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
                    {app.status?.currentImage && (
                      <p className="text-[11px] text-text-tertiary mt-2 truncate font-mono">{app.status.currentImage}</p>
                    )}
                  </Link>
                ))}
              </div>
            )}
          </section>
        </div>

        <div className="space-y-4">
          <section className="card p-5">
            <div className="flex items-center justify-between mb-4">
              <h3 className="section-title">Project Info</h3>
              <button onClick={() => setShowEditProject(!showEditProject)} className="text-xs text-accent hover:text-accent-glow transition-colors">
                {showEditProject ? 'Cancel' : 'Edit'}
              </button>
            </div>
            {showEditProject ? (
              <EditProjectForm project={project} projectId={projectId!} onClose={() => setShowEditProject(false)} />
            ) : (
              <div className="space-y-3">
                <InfoRow label="Name" value={project.name} mono />
                <InfoRow label="Display Name" value={project.displayName || '—'} />
                <InfoRow label="Team" value={project.teamName || '—'} />
                <InfoRow label="Environments" value={environments?.total ?? 0} />
                <InfoRow label="Apps" value={apps?.total ?? 0} />
                {project.createdAt && (
                  <InfoRow label="Created" value={new Date(project.createdAt).toLocaleDateString()} />
                )}
                {project.spec?.labels && Object.keys(project.spec.labels).length > 0 && (
                  <div className="pt-2 border-t border-border-subtle">
                    <span className="text-xs text-text-tertiary">Labels</span>
                    <div className="flex flex-wrap gap-1.5 mt-1">
                      {Object.entries(project.spec.labels).map(([k, v]) => (
                        <span key={k} className="text-[11px] font-mono bg-surface-3 text-text-secondary px-2 py-0.5 rounded">{k}={v as string}</span>
                      ))}
                    </div>
                  </div>
                )}
                {project.spec?.annotations && Object.keys(project.spec.annotations).length > 0 && (
                  <div className="pt-2 border-t border-border-subtle">
                    <span className="text-xs text-text-tertiary">Annotations</span>
                    <div className="flex flex-wrap gap-1.5 mt-1">
                      {Object.entries(project.spec.annotations).map(([k, v]) => (
                        <span key={k} className="text-[11px] font-mono bg-surface-3 text-text-secondary px-2 py-0.5 rounded truncate max-w-full">{k}={v as string}</span>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )}
          </section>
        </div>
      </div>
    </div>
  )
}

function EditProjectForm({ project, projectId, onClose }: { project: any; projectId: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [displayName, setDisplayName] = useState(project.spec?.displayName || project.displayName || '')
  const [labels, setLabels] = useState<{ key: string; value: string }[]>(() => {
    const l = project.spec?.labels || {}
    const entries = Object.entries(l)
    return entries.length > 0 ? entries.map(([key, value]) => ({ key, value: value as string })) : []
  })
  const [annotations, setAnnotations] = useState<{ key: string; value: string }[]>(() => {
    const a = project.spec?.annotations || {}
    const entries = Object.entries(a)
    return entries.length > 0 ? entries.map(([key, value]) => ({ key, value: value as string })) : []
  })

  const mutation = useMutation({
    mutationFn: (data: any) => api.updateProject(projectId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['project', projectId] })
      onClose()
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    const patch: any = {}
    if (displayName) patch.displayName = displayName

    const customLabels: Record<string, string> = {}
    labels.forEach(l => { if (l.key) customLabels[l.key] = l.value })
    patch.labels = Object.keys(customLabels).length > 0 ? customLabels : null

    const customAnnotations: Record<string, string> = {}
    annotations.forEach(a => { if (a.key) customAnnotations[a.key] = a.value })
    patch.annotations = Object.keys(customAnnotations).length > 0 ? customAnnotations : null

    mutation.mutate(patch)
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <div>
        <label className="label">Display Name</label>
        <input value={displayName} onChange={e => setDisplayName(e.target.value)} className="input-field" placeholder="My Project" />
      </div>

      <div>
        <div className="flex items-center justify-between mb-2">
          <label className="label">Labels</label>
          <button type="button" onClick={() => setLabels(prev => [...prev, { key: '', value: '' }])} className="text-xs text-accent hover:text-accent-glow">+ Add</button>
        </div>
        {labels.map((l, i) => (
          <div key={i} className="flex gap-2 mb-2">
            <input value={l.key} onChange={e => { const u = [...labels]; u[i].key = e.target.value; setLabels(u) }} placeholder="key" className="input-field flex-1 font-mono text-xs" />
            <input value={l.value} onChange={e => { const u = [...labels]; u[i].value = e.target.value; setLabels(u) }} placeholder="value" className="input-field flex-1 text-xs" />
            <button type="button" onClick={() => setLabels(prev => prev.filter((_, j) => j !== i))} className="text-text-tertiary hover:text-status-failed text-xs px-1">&times;</button>
          </div>
        ))}
      </div>

      <div>
        <div className="flex items-center justify-between mb-2">
          <label className="label">Annotations</label>
          <button type="button" onClick={() => setAnnotations(prev => [...prev, { key: '', value: '' }])} className="text-xs text-accent hover:text-accent-glow">+ Add</button>
        </div>
        {annotations.map((a, i) => (
          <div key={i} className="flex gap-2 mb-2">
            <input value={a.key} onChange={e => { const u = [...annotations]; u[i].key = e.target.value; setAnnotations(u) }} placeholder="key" className="input-field flex-1 font-mono text-xs" />
            <input value={a.value} onChange={e => { const u = [...annotations]; u[i].value = e.target.value; setAnnotations(u) }} placeholder="value" className="input-field flex-1 text-xs" />
            <button type="button" onClick={() => setAnnotations(prev => prev.filter((_, j) => j !== i))} className="text-text-tertiary hover:text-status-failed text-xs px-1">&times;</button>
          </div>
        ))}
      </div>

      <div className="flex gap-3">
        <button type="submit" disabled={mutation.isPending} className="btn-primary text-xs">
          {mutation.isPending ? 'Saving...' : 'Save'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost text-xs">Cancel</button>
      </div>
      {mutation.isError && (
        <p className="text-status-failed text-xs">{(mutation.error as Error).message}</p>
      )}
    </form>
  )
}

function EnvironmentRow({ env, projectId }: { env: any; projectId: string }) {
  const queryClient = useQueryClient()

  const deleteMutation = useMutation({
    mutationFn: () => api.deleteEnvironment(projectId, env.name),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['environments', projectId] }),
  })

  return (
    <div className="card-hover flex items-center justify-between px-5 py-3.5 group">
      <div className="flex items-center gap-4">
        <div className="w-8 h-8 rounded-lg bg-surface-3 flex items-center justify-center">
          <svg className="w-4 h-4 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
          </svg>
        </div>
        <div>
          <p className="text-sm font-medium text-text-primary">{env.name}</p>
          <div className="flex items-center gap-3 mt-0.5">
            {env.branch && (
              <span className="text-xs text-text-tertiary font-mono">{env.branch}</span>
            )}
            {env.autoDeploy && (
              <span className="text-[11px] text-accent font-mono">auto-deploy</span>
            )}
            {env.requireApproval && (
              <span className="text-[11px] text-status-pending font-mono">approval required</span>
            )}
          </div>
        </div>
      </div>
      <button
        onClick={() => {
          if (confirm(`Delete environment "${env.name}"?`))
            deleteMutation.mutate()
        }}
        className="text-xs text-text-tertiary hover:text-status-failed transition-colors opacity-0 group-hover:opacity-100"
      >
        Delete
      </button>
    </div>
  )
}

function CreateEnvironmentForm({ projectId, onClose }: { projectId: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [branch, setBranch] = useState('')
  const [autoDeploy, setAutoDeploy] = useState(false)
  const [requireApproval, setRequireApproval] = useState(false)

  const mutation = useMutation({
    mutationFn: (data: any) => api.createEnvironment(projectId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['environments', projectId] })
      onClose()
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    mutation.mutate({
      name,
      branch: branch || undefined,
      autoDeploy,
      requireApproval,
    })
  }

  return (
    <form onSubmit={handleSubmit} className="card p-5 space-y-4 animate-slide-up mb-4">
      <h4 className="text-sm font-semibold text-text-secondary">New Environment</h4>
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="label">Name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="input-field"
            required
            placeholder="staging"
          />
        </div>
        <div>
          <label className="label">Branch</label>
          <input
            value={branch}
            onChange={(e) => setBranch(e.target.value)}
            className="input-field"
            placeholder="develop"
          />
        </div>
      </div>
      <div className="flex items-center gap-6">
        <label className="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={autoDeploy}
            onChange={(e) => setAutoDeploy(e.target.checked)}
            className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
          />
          <span className="text-xs text-text-secondary">Auto-deploy</span>
        </label>
        <label className="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={requireApproval}
            onChange={(e) => setRequireApproval(e.target.checked)}
            className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
          />
          <span className="text-xs text-text-secondary">Require approval</span>
        </label>
      </div>
      <div className="flex gap-3 pt-1">
        <button type="submit" disabled={mutation.isPending} className="btn-primary">
          {mutation.isPending ? 'Creating...' : 'Create Environment'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
      </div>
      {mutation.isError && (
        <p className="text-status-failed text-xs">{(mutation.error as Error).message}</p>
      )}
    </form>
  )
}

interface EnvConfig {
  name: string
  replicas: number
  autoscale?: {
    enabled: boolean
    minReplicas: number
    maxReplicas: number
    targetCPU: number
  }
}

function CreateAppForm({ projectId, environments, onClose }: { projectId: string; environments: any[]; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [envConfigs, setEnvConfigs] = useState<Record<string, EnvConfig>>({})
  const [imageRepo, setImageRepo] = useState('')
  const [imageTag, setImageTag] = useState('')
  const [port, setPort] = useState('3000')

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
      return { ...prev, [envName]: { name: envName, replicas: 1 } }
    })
  }

  const updateEnvReplicas = (envName: string, replicas: number) => {
    setEnvConfigs((prev) => ({
      ...prev,
      [envName]: { ...prev[envName], replicas },
    }))
  }

  const toggleAutoscale = (envName: string) => {
    setEnvConfigs((prev) => {
      const current = prev[envName]
      if (current.autoscale?.enabled) {
        return { ...prev, [envName]: { ...current, autoscale: undefined } }
      }
      return {
        ...prev,
        [envName]: {
          ...current,
          autoscale: { enabled: true, minReplicas: 1, maxReplicas: 5, targetCPU: 80 },
        },
      }
    })
  }

  const updateAutoscale = (envName: string, field: string, value: number) => {
    setEnvConfigs((prev) => ({
      ...prev,
      [envName]: {
        ...prev[envName],
        autoscale: { ...prev[envName].autoscale!, [field]: value },
      },
    }))
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    mutation.mutate({
      name,
      environments: Object.values(envConfigs),
      image: {
        repository: imageRepo || undefined,
        tag: imageTag || undefined,
      },
      runtime: {
        port: parseInt(port) || 3000,
      },
    })
  }

  return (
    <form onSubmit={handleSubmit} className="card p-5 space-y-4 animate-slide-up mb-4">
      <h4 className="text-sm font-semibold text-text-secondary">New App</h4>
      <div>
        <label className="label">App Name</label>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="input-field"
          required
          placeholder="my-app"
        />
      </div>

      {environments.length > 0 && (
        <div>
          <label className="label">Environments</label>
          <div className="space-y-3">
            {environments.map((env: any) => {
              const isSelected = !!envConfigs[env.name]
              const config = envConfigs[env.name]
              return (
                <div key={env.name} className={`rounded-lg border p-3 transition-all ${
                  isSelected
                    ? 'border-accent bg-accent/5'
                    : 'border-border bg-surface-1'
                }`}>
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={isSelected}
                      onChange={() => toggleEnv(env.name)}
                      className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
                    />
                    <span className={`text-sm font-mono ${isSelected ? 'text-accent' : 'text-text-secondary'}`}>
                      {env.name}
                    </span>
                  </label>
                  {isSelected && config && (
                    <div className="mt-3 pl-6 space-y-3">
                      <div className="flex items-center gap-4">
                        <div>
                          <label className="text-xs text-text-tertiary">Replicas</label>
                          <input
                            type="number"
                            min="0"
                            value={config.replicas}
                            onChange={(e) => updateEnvReplicas(env.name, parseInt(e.target.value) || 0)}
                            className="input-field w-20 mt-1"
                          />
                        </div>
                        <label className="flex items-center gap-2 cursor-pointer mt-5">
                          <input
                            type="checkbox"
                            checked={config.autoscale?.enabled || false}
                            onChange={() => toggleAutoscale(env.name)}
                            className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
                          />
                          <span className="text-xs text-text-secondary">Autoscale</span>
                        </label>
                      </div>
                      {config.autoscale?.enabled && (
                        <div className="flex items-center gap-4 flex-wrap">
                          <div>
                            <label className="text-xs text-text-tertiary">Min</label>
                            <input
                              type="number"
                              min="1"
                              value={config.autoscale.minReplicas}
                              onChange={(e) => updateAutoscale(env.name, 'minReplicas', parseInt(e.target.value) || 1)}
                              className="input-field w-16 mt-1"
                            />
                          </div>
                          <div>
                            <label className="text-xs text-text-tertiary">Max</label>
                            <input
                              type="number"
                              min="1"
                              value={config.autoscale.maxReplicas}
                              onChange={(e) => updateAutoscale(env.name, 'maxReplicas', parseInt(e.target.value) || 1)}
                              className="input-field w-16 mt-1"
                            />
                          </div>
                          <div>
                            <label className="text-xs text-text-tertiary">Target CPU %</label>
                            <input
                              type="number"
                              min="1"
                              max="100"
                              value={config.autoscale.targetCPU}
                              onChange={(e) => updateAutoscale(env.name, 'targetCPU', parseInt(e.target.value) || 80)}
                              className="input-field w-20 mt-1"
                            />
                          </div>
                        </div>
                      )}
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
          <input
            value={imageRepo}
            onChange={(e) => setImageRepo(e.target.value)}
            className="input-field"
            placeholder="registry.example.com/org/app"
          />
        </div>
        <div>
          <label className="label">Tag</label>
          <input
            value={imageTag}
            onChange={(e) => setImageTag(e.target.value)}
            className="input-field"
            placeholder="latest"
          />
        </div>
      </div>

      <div>
        <label className="label">Port</label>
        <input
          type="number"
          value={port}
          onChange={(e) => setPort(e.target.value)}
          className="input-field w-32"
        />
      </div>

      <div className="flex gap-3 pt-1">
        <button type="submit" disabled={mutation.isPending} className="btn-primary">
          {mutation.isPending ? 'Creating...' : 'Create App'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
      </div>
      {mutation.isError && (
        <p className="text-status-failed text-xs">{(mutation.error as Error).message}</p>
      )}
    </form>
  )
}

function InfoRow({ label, value, mono }: { label: string; value: any; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between py-1.5">
      <span className="text-xs text-text-tertiary">{label}</span>
      <span className={`text-xs text-text-secondary ${mono ? 'font-mono' : ''}`}>{value ?? '—'}</span>
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
    <div className="flex items-center justify-center py-20">
      <svg className="w-5 h-5 animate-spin text-accent" fill="none" viewBox="0 0 24 24">
        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
      </svg>
    </div>
  )
}
