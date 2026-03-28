import { useState, useRef, useEffect } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

export default function AppDetailPage() {
  const { appId } = useParams<{ appId: string }>()
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const { data: app, isLoading } = useQuery({
    queryKey: ['app', appId],
    queryFn: () => api.getApp(appId!),
    enabled: !!appId,
  })

  const { data: deployments } = useQuery({
    queryKey: ['deployments', appId],
    queryFn: () => api.listDeployments(appId!),
    enabled: !!appId,
  })

  const [tag, setTag] = useState('')
  const [reason, setReason] = useState('')
  const [deployEnv, setDeployEnv] = useState('')
  const [secretEnv, setSecretEnv] = useState('')
  const [editing, setEditing] = useState(false)
  const [activeTab, setActiveTab] = useState<'overview' | 'secrets' | 'logs' | 'metrics'>('overview')

  const projectId = app?.project || app?.spec?.project
  const rawEnvs = app?.environments || app?.spec?.environments || []
  const appEnvironments: string[] = rawEnvs.map((e: any) => typeof e === 'string' ? e : e.name)

  const { data: projectEnvs } = useQuery({
    queryKey: ['environments', projectId],
    queryFn: () => api.listEnvironments(projectId),
    enabled: !!projectId,
  })

  const availableEnvs = (projectEnvs?.items || [])
    .map((e: any) => e.name)
    .filter((name: string) => !appEnvironments.includes(name))

  const deployMutation = useMutation({
    mutationFn: () => api.deploy(appId!, { tag, environment: deployEnv, reason: reason || undefined }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['app', appId] })
      queryClient.invalidateQueries({ queryKey: ['deployments', appId] })
      setTag('')
      setReason('')
    },
  })

  const restartMutation = useMutation({
    mutationFn: (env: string) => api.restart(appId!, env),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['app', appId] }),
  })

  const rollbackMutation = useMutation({
    mutationFn: ({ version, environment }: { version: number; environment: string }) => api.rollback(appId!, version, environment),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['app', appId] })
      queryClient.invalidateQueries({ queryKey: ['deployments', appId] })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: () => api.deleteApp(appId!),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['apps'] })
      navigate('/apps')
    },
  })

  const addEnvMutation = useMutation({
    mutationFn: (envName: string) => {
      const currentEnvs = rawEnvs.map((e: any) => typeof e === 'string' ? { name: e } : e)
      return api.updateApp(appId!, { environments: [...currentEnvs, { name: envName }] })
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['app', appId] }),
  })

  const removeEnvMutation = useMutation({
    mutationFn: (envName: string) => {
      const currentEnvs = rawEnvs
        .map((e: any) => typeof e === 'string' ? { name: e } : e)
        .filter((e: any) => e.name !== envName)
      return api.updateApp(appId!, { environments: currentEnvs })
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['app', appId] }),
  })

  if (isLoading) return <Spinner />

  if (!app) {
    return (
      <div className="card px-6 py-12 text-center">
        <p className="text-sm text-text-secondary">App not found</p>
        <Link to="/apps" className="text-xs text-accent mt-2 inline-block">&larr; Back to apps</Link>
      </div>
    )
  }

  const phase = app.status?.phase || 'Pending'

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-4">
          <Link to="/apps" className="text-text-tertiary hover:text-text-secondary transition-colors">
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M15 19l-7-7 7-7" />
            </svg>
          </Link>
          <div>
            <h2 className="text-xl font-display text-text-primary">{app.name}</h2>
            <div className="flex items-center gap-2 mt-0.5">
              {app.projectId && (
                <Link
                  to={`/projects/${app.projectId}`}
                  className="text-xs text-accent hover:text-accent-glow transition-colors font-mono"
                >
                  {app.projectName || app.project || app.projectId}
                </Link>
              )}
              {!app.projectId && app.project && (
                <span className="text-xs text-text-tertiary font-mono">{app.project}</span>
              )}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setEditing(!editing)}
            className="btn-ghost text-xs"
          >
            <span className="flex items-center gap-1.5">
              <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
              </svg>
              {editing ? 'Cancel Edit' : 'Edit'}
            </span>
          </button>
          <button
            onClick={() => {
              if (confirm(`Delete app "${app.name}"? This cannot be undone.`))
                deleteMutation.mutate()
            }}
            disabled={deleteMutation.isPending}
            className="btn-ghost text-xs text-status-failed hover:bg-status-failed/10"
          >
            <span className="flex items-center gap-1.5">
              <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
              </svg>
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </span>
          </button>
        </div>
      </div>

      {editing && (
        <EditAppForm appId={appId!} app={app} onClose={() => setEditing(false)} />
      )}

      <div className="flex border-b border-border">
        {(['overview', 'secrets', 'logs', 'metrics'] as const).map((tab) => (
          <button
            key={tab}
            onClick={() => setActiveTab(tab)}
            className={`px-5 py-3 text-xs font-mono tracking-wider uppercase transition-colors ${
              activeTab === tab
                ? 'text-accent border-b-2 border-accent'
                : 'text-text-tertiary hover:text-text-secondary'
            }`}
          >
            {tab}
          </button>
        ))}
      </div>

      {activeTab === 'overview' && (
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <div className="lg:col-span-2 space-y-4">
            <section className="card p-5">
              <h3 className="section-title mb-4">Status</h3>
              <div className="grid grid-cols-2 gap-x-6 gap-y-4">
                <InfoItem label="Phase">
                  <StatusBadge phase={phase} />
                </InfoItem>
                <InfoItem label="URL">
                  {app.status?.url ? (
                    <a href={app.status.url} target="_blank" rel="noopener noreferrer" className="text-accent text-sm hover:underline">
                      {app.status.url}
                    </a>
                  ) : (
                    <span className="text-text-tertiary text-sm">—</span>
                  )}
                </InfoItem>
                <InfoItem label="Current Image">
                  <span className="font-mono text-xs text-text-secondary">{app.status?.currentImage || '—'}</span>
                </InfoItem>
                <InfoItem label="Last Deployed">
                  <span className="text-sm text-text-secondary">
                    {app.status?.lastDeployedAt
                      ? new Date(app.status.lastDeployedAt).toLocaleString()
                      : '—'}
                  </span>
                </InfoItem>
              </div>
            </section>

            <section className="card p-5">
              <div className="flex items-center justify-between mb-4">
                <h3 className="section-title">Environments</h3>
                {availableEnvs.length > 0 && (
                  <select
                    value=""
                    onChange={(e) => {
                      if (e.target.value) addEnvMutation.mutate(e.target.value)
                    }}
                    className="input-field text-xs w-auto"
                    disabled={addEnvMutation.isPending}
                  >
                    <option value="">+ Add environment</option>
                    {availableEnvs.map((name: string) => (
                      <option key={name} value={name}>{name}</option>
                    ))}
                  </select>
                )}
              </div>
              {appEnvironments.length === 0 ? (
                <p className="text-sm text-text-tertiary">No environments assigned</p>
              ) : (
                <div className="space-y-2">
                  {appEnvironments.map((env: string) => (
                    <div
                      key={env}
                      className="flex items-center justify-between px-4 py-3 bg-surface-1 border border-border rounded-lg group"
                    >
                      <span className="text-sm font-mono text-text-secondary">{env}</span>
                      <div className="flex items-center gap-3 opacity-0 group-hover:opacity-100 transition-opacity">
                        <button
                          onClick={() => {
                            if (confirm(`Restart "${app.name}" in "${env}"?`))
                              restartMutation.mutate(env)
                          }}
                          disabled={restartMutation.isPending}
                          className="text-xs text-text-tertiary hover:text-accent transition-colors flex items-center gap-1"
                        >
                          <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                            <path strokeLinecap="round" strokeLinejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                          </svg>
                          Restart
                        </button>
                        {appEnvironments.length > 1 && (
                          <button
                            onClick={() => {
                              if (confirm(`Remove "${app.name}" from "${env}"?`))
                                removeEnvMutation.mutate(env)
                            }}
                            disabled={removeEnvMutation.isPending}
                            className="text-xs text-text-tertiary hover:text-status-failed transition-colors flex items-center gap-1"
                          >
                            <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                            </svg>
                            Remove
                          </button>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </section>

            <section className="card p-5">
              <h3 className="section-title mb-4">Deployment History</h3>
              {(!deployments?.items || deployments.items.length === 0) ? (
                <p className="text-sm text-text-tertiary">No deployments yet</p>
              ) : (
                <div className="space-y-0">
                  {deployments.items.map((d: any, i: number) => (
                    <div key={i} className="flex items-center justify-between py-3 border-b border-border-subtle last:border-0 group">
                      <div className="flex items-center gap-3">
                        <div className="w-6 h-6 rounded bg-surface-3 flex items-center justify-center text-[10px] font-mono text-text-tertiary">
                          v{d.version}
                        </div>
                        <div>
                          <span className="font-mono text-xs text-text-secondary">{d.image || '—'}</span>
                          {d.commitSHA && (
                            <span className="ml-2 font-mono text-[11px] text-text-tertiary">
                              {d.commitSHA.slice(0, 7)}
                            </span>
                          )}
                        </div>
                      </div>
                      <div className="flex items-center gap-3">
                        <span className="text-xs text-text-tertiary font-mono">{d.deployedBy}</span>
                        {d.version && (
                          <button
                            onClick={() => {
                              if (confirm(`Rollback to version ${d.version}?`)) {
                                const env = prompt('Which environment?', appEnvironments[0] || '')
                                if (env) rollbackMutation.mutate({ version: d.version, environment: env })
                              }
                            }}
                            className="text-xs text-text-tertiary hover:text-accent transition-colors opacity-0 group-hover:opacity-100"
                          >
                            Rollback
                          </button>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </section>
          </div>

          <div className="space-y-4">
            <section className="card p-5">
              <h3 className="section-title mb-4">Deploy</h3>
              <form
                onSubmit={(e) => {
                  e.preventDefault()
                  deployMutation.mutate()
                }}
                className="space-y-3"
              >
                <div>
                  <label className="label">Environment</label>
                  <select
                    value={deployEnv}
                    onChange={(e) => setDeployEnv(e.target.value)}
                    className="input-field"
                    required
                  >
                    <option value="">Select environment...</option>
                    {appEnvironments.map((env: string) => (
                      <option key={env} value={env}>{env}</option>
                    ))}
                  </select>
                </div>
                <div>
                  <label className="label">Image Tag</label>
                  <input
                    value={tag}
                    onChange={(e) => setTag(e.target.value)}
                    placeholder="v1.2.3"
                    className="input-field"
                    required
                  />
                </div>
                <div>
                  <label className="label">Reason</label>
                  <input
                    value={reason}
                    onChange={(e) => setReason(e.target.value)}
                    placeholder="CI build #123"
                    className="input-field"
                  />
                </div>
                <button type="submit" disabled={deployMutation.isPending || !tag || !deployEnv} className="btn-primary w-full">
                  {deployMutation.isPending ? 'Deploying...' : `Deploy to ${deployEnv || '...'}`}
                </button>
                {deployMutation.isError && (
                  <p className="text-status-failed text-xs">{(deployMutation.error as Error).message}</p>
                )}
                {deployMutation.isSuccess && (
                  <p className="text-status-running text-xs">Deployment triggered</p>
                )}
              </form>
            </section>

            <section className="card p-5">
              <h3 className="section-title mb-4">Configuration</h3>
              <div className="space-y-3">
                <ConfigItem label="Image Repository" value={app.spec?.image?.repository} mono />
                <ConfigItem label="Port" value={app.spec?.runtime?.port} />
                <ConfigItem label="Replicas" value={app.spec?.scaling?.replicas || app.status?.scaling?.currentReplicas || 1} />
                <ConfigItem
                  label="Autoscale"
                  value={app.spec?.scaling?.autoscale?.enabled ? 'Enabled' : 'Disabled'}
                  accent={app.spec?.scaling?.autoscale?.enabled}
                />
                {app.spec?.ingress?.domain && (
                  <ConfigItem label="Domain" value={app.spec.ingress.domain} mono />
                )}
                {app.spec?.ingress?.tls !== undefined && (
                  <ConfigItem label="TLS" value={app.spec.ingress.tls ? 'Enabled' : 'Disabled'} accent={app.spec.ingress.tls} />
                )}
              </div>
            </section>
          </div>
        </div>
      )}

      {activeTab === 'secrets' && (
        <div className="space-y-4">
          {appEnvironments.length > 0 ? (
            <section className="card p-5">
              <h3 className="section-title mb-4">Per-Environment Secrets</h3>
              <div className="mb-3">
                <label className="label">Select Environment</label>
                <select
                  value={secretEnv}
                  onChange={(e) => setSecretEnv(e.target.value)}
                  className="input-field w-48"
                >
                  <option value="">Choose...</option>
                  {appEnvironments.map((env: string) => (
                    <option key={env} value={env}>{env}</option>
                  ))}
                </select>
              </div>
              {secretEnv && (
                <EnvSecrets appId={appId!} env={secretEnv} />
              )}
            </section>
          ) : (
            <div className="card p-5">
              <p className="text-sm text-text-tertiary">No environments configured</p>
            </div>
          )}
        </div>
      )}

      {activeTab === 'logs' && (
        <div>
          {appEnvironments.length > 0 ? (
            <AppLogs appId={appId!} environments={appEnvironments} />
          ) : (
            <div className="card p-5">
              <p className="text-sm text-text-tertiary">No environments configured</p>
            </div>
          )}
        </div>
      )}

      {activeTab === 'metrics' && (
        <div>
          {appEnvironments.length > 0 ? (
            <AppMetrics appId={appId!} environments={appEnvironments} />
          ) : (
            <div className="card p-5">
              <p className="text-sm text-text-tertiary">No environments configured</p>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function EditAppForm({ appId, app, onClose }: { appId: string; app: any; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [imageRepo, setImageRepo] = useState(app.spec?.image?.repository || '')
  const [imageTag, setImageTag] = useState(app.spec?.image?.tag || '')
  const [port, setPort] = useState(String(app.spec?.runtime?.port || 3000))
  const [domain, setDomain] = useState(app.spec?.ingress?.domain || '')
  const [tls, setTls] = useState(app.spec?.ingress?.tls || false)

  // Image pull secrets (app-level override)
  const { data: registrySecrets } = useQuery({
    queryKey: ['registrySecrets'],
    queryFn: () => api.listRegistrySecrets(),
  })
  const [pullSecrets, setPullSecrets] = useState<string[]>(() => {
    return (app.spec?.image?.imagePullSecrets || []).map((s: any) => s.name)
  })

  // Per-environment config
  const rawEnvs = app.environments || app.spec?.environments || []
  const [envConfigs, setEnvConfigs] = useState<Record<string, { replicas: number; autoscaleEnabled: boolean; minReplicas: number; maxReplicas: number; targetCPU: number }>>(() => {
    const configs: Record<string, any> = {}
    for (const e of rawEnvs) {
      const env = typeof e === 'string' ? { name: e } : e
      configs[env.name] = {
        replicas: env.replicas ?? 1,
        autoscaleEnabled: env.autoscale?.enabled || false,
        minReplicas: env.autoscale?.minReplicas || 1,
        maxReplicas: env.autoscale?.maxReplicas || 5,
        targetCPU: env.autoscale?.metrics?.[0]?.targetAverageUtilization || env.autoscale?.targetCPU || 80,
      }
    }
    return configs
  })

  // Custom labels and annotations
  const [labels, setLabels] = useState<{ key: string; value: string }[]>(() => {
    const l = app.spec?.customConfig?.labels || {}
    const entries = Object.entries(l)
    return entries.length > 0 ? entries.map(([key, value]) => ({ key, value: value as string })) : []
  })
  const [annotations, setAnnotations] = useState<{ key: string; value: string }[]>(() => {
    const a = app.spec?.customConfig?.annotations || {}
    const entries = Object.entries(a)
    return entries.length > 0 ? entries.map(([key, value]) => ({ key, value: value as string })) : []
  })

  const mutation = useMutation({
    mutationFn: (data: any) => api.updateApp(appId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['app', appId] })
      onClose()
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    const patch: any = {}

    // Image
    if (imageRepo) {
      patch.image = {
        repository: imageRepo,
        tag: imageTag || 'latest',
        ...(pullSecrets.length > 0 && { imagePullSecrets: pullSecrets.map(n => ({ name: n })) }),
      }
    }

    // Runtime
    patch.runtime = { port: Number.parseInt(port) || 3000 }

    // Ingress
    if (domain) {
      patch.ingress = { domain, tls }
    }

    // Environments
    const envArray = Object.entries(envConfigs).map(([name, cfg]) => {
      const env: any = { name, replicas: cfg.replicas }
      if (cfg.autoscaleEnabled) {
        env.autoscale = {
          enabled: true,
          minReplicas: cfg.minReplicas,
          maxReplicas: cfg.maxReplicas,
          metrics: [{ type: 'cpu', targetAverageUtilization: cfg.targetCPU }],
        }
      }
      return env
    })
    if (envArray.length > 0) {
      patch.environments = envArray
    }

    // Custom labels/annotations
    const customLabels: Record<string, string> = {}
    labels.forEach(l => { if (l.key) customLabels[l.key] = l.value })
    const customAnnotations: Record<string, string> = {}
    annotations.forEach(a => { if (a.key) customAnnotations[a.key] = a.value })
    if (Object.keys(customLabels).length > 0 || Object.keys(customAnnotations).length > 0) {
      patch.customConfig = {
        ...(Object.keys(customLabels).length > 0 && { labels: customLabels }),
        ...(Object.keys(customAnnotations).length > 0 && { annotations: customAnnotations }),
      }
    }

    mutation.mutate(patch)
  }

  return (
    <form onSubmit={handleSubmit} className="card p-5 space-y-5 animate-slide-up">
      <h3 className="section-title">Edit App</h3>

      <div className="grid grid-cols-3 gap-4">
        <div className="col-span-2">
          <label className="label">Image Repository</label>
          <input value={imageRepo} onChange={e => setImageRepo(e.target.value)} className="input-field" placeholder="registry/org/app" />
        </div>
        <div>
          <label className="label">Tag</label>
          <input value={imageTag} onChange={e => setImageTag(e.target.value)} className="input-field" placeholder="latest" />
        </div>
      </div>

      <div className="grid grid-cols-3 gap-4">
        <div>
          <label className="label">Port</label>
          <input type="number" value={port} onChange={e => setPort(e.target.value)} className="input-field" />
        </div>
        <div>
          <label className="label">Domain</label>
          <input value={domain} onChange={e => setDomain(e.target.value)} className="input-field" placeholder="app.example.com" />
        </div>
        <div className="flex items-end pb-1">
          <label className="flex items-center gap-2 cursor-pointer">
            <input type="checkbox" checked={tls} onChange={e => setTls(e.target.checked)} className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20" />
            <span className="text-xs text-text-secondary">TLS</span>
          </label>
        </div>
      </div>

      {Object.keys(envConfigs).length > 0 && (
        <div>
          <label className="label">Per-Environment Config</label>
          <div className="space-y-3">
            {Object.entries(envConfigs).map(([envName, cfg]) => (
              <div key={envName} className="rounded-lg border border-border bg-surface-1 p-3">
                <span className="text-sm font-mono text-accent">{envName}</span>
                <div className="mt-2 flex items-center gap-4 flex-wrap">
                  <div>
                    <label className="text-xs text-text-tertiary">Replicas</label>
                    <input
                      type="number" min="0" value={cfg.replicas}
                      onChange={e => setEnvConfigs(prev => ({ ...prev, [envName]: { ...prev[envName], replicas: Number.parseInt(e.target.value) || 0 } }))}
                      className="input-field w-20 mt-1"
                    />
                  </div>
                  <label className="flex items-center gap-2 cursor-pointer mt-5">
                    <input
                      type="checkbox" checked={cfg.autoscaleEnabled}
                      onChange={e => setEnvConfigs(prev => ({ ...prev, [envName]: { ...prev[envName], autoscaleEnabled: e.target.checked } }))}
                      className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
                    />
                    <span className="text-xs text-text-secondary">Autoscale</span>
                  </label>
                  {cfg.autoscaleEnabled && (
                    <>
                      <div>
                        <label className="text-xs text-text-tertiary">Min</label>
                        <input
                          type="number" min="1" value={cfg.minReplicas}
                          onChange={e => setEnvConfigs(prev => ({ ...prev, [envName]: { ...prev[envName], minReplicas: Number.parseInt(e.target.value) || 1 } }))}
                          className="input-field w-16 mt-1"
                        />
                      </div>
                      <div>
                        <label className="text-xs text-text-tertiary">Max</label>
                        <input
                          type="number" min="1" value={cfg.maxReplicas}
                          onChange={e => setEnvConfigs(prev => ({ ...prev, [envName]: { ...prev[envName], maxReplicas: Number.parseInt(e.target.value) || 1 } }))}
                          className="input-field w-16 mt-1"
                        />
                      </div>
                      <div>
                        <label className="text-xs text-text-tertiary">CPU %</label>
                        <input
                          type="number" min="1" max="100" value={cfg.targetCPU}
                          onChange={e => setEnvConfigs(prev => ({ ...prev, [envName]: { ...prev[envName], targetCPU: Number.parseInt(e.target.value) || 80 } }))}
                          className="input-field w-20 mt-1"
                        />
                      </div>
                    </>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {(registrySecrets?.items?.length ?? 0) > 0 && (
        <div>
          <label className="label mb-2">Image Pull Secrets (app-level override)</label>
          <p className="text-[11px] text-text-tertiary mb-2">
            Project-level credentials are inherited automatically. Only add here to override.
          </p>
          <div className="flex flex-wrap gap-1.5 mb-2">
            {pullSecrets.map(name => (
              <span key={name} className="inline-flex items-center gap-1.5 text-[11px] font-mono bg-surface-3 text-text-secondary px-2 py-1 rounded">
                {name}
                <button type="button" onClick={() => setPullSecrets(prev => prev.filter(n => n !== name))} className="text-text-tertiary hover:text-status-failed">&times;</button>
              </span>
            ))}
          </div>
          {(registrySecrets?.items?.filter((s: any) => !pullSecrets.includes(s.name))?.length ?? 0) > 0 && (
            <select
              value=""
              onChange={(e) => { if (e.target.value) setPullSecrets(prev => [...prev, e.target.value]) }}
              className="input-field text-xs w-48"
            >
              <option value="">+ Add pull secret</option>
              {registrySecrets?.items?.filter((s: any) => !pullSecrets.includes(s.name)).map((s: any) => (
                <option key={s.name} value={s.name}>{s.name} ({s.registry})</option>
              ))}
            </select>
          )}
        </div>
      )}

      <div>
        <div className="flex items-center justify-between mb-2">
          <label className="label">Custom Labels</label>
          <button type="button" onClick={() => setLabels(prev => [...prev, { key: '', value: '' }])} className="text-xs text-accent hover:text-accent-glow">+ Add</button>
        </div>
        {labels.map((l, i) => (
          <div key={i} className="flex gap-2 mb-2">
            <input value={l.key} onChange={e => { const u = [...labels]; u[i].key = e.target.value; setLabels(u) }} placeholder="key" className="input-field flex-1 font-mono text-xs" />
            <input value={l.value} onChange={e => { const u = [...labels]; u[i].value = e.target.value; setLabels(u) }} placeholder="value" className="input-field flex-1 text-xs" />
            <button type="button" onClick={() => setLabels(prev => prev.filter((_, j) => j !== i))} className="text-text-tertiary hover:text-status-failed text-xs px-2">&times;</button>
          </div>
        ))}
      </div>

      <div>
        <div className="flex items-center justify-between mb-2">
          <label className="label">Custom Annotations</label>
          <button type="button" onClick={() => setAnnotations(prev => [...prev, { key: '', value: '' }])} className="text-xs text-accent hover:text-accent-glow">+ Add</button>
        </div>
        {annotations.map((a, i) => (
          <div key={i} className="flex gap-2 mb-2">
            <input value={a.key} onChange={e => { const u = [...annotations]; u[i].key = e.target.value; setAnnotations(u) }} placeholder="key" className="input-field flex-1 font-mono text-xs" />
            <input value={a.value} onChange={e => { const u = [...annotations]; u[i].value = e.target.value; setAnnotations(u) }} placeholder="value" className="input-field flex-1 text-xs" />
            <button type="button" onClick={() => setAnnotations(prev => prev.filter((_, j) => j !== i))} className="text-text-tertiary hover:text-status-failed text-xs px-2">&times;</button>
          </div>
        ))}
      </div>

      <div className="flex gap-3 pt-1">
        <button type="submit" disabled={mutation.isPending} className="btn-primary">
          {mutation.isPending ? 'Saving...' : 'Save Changes'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
      </div>
      {mutation.isError && (
        <p className="text-status-failed text-xs">{(mutation.error as Error).message}</p>
      )}
    </form>
  )
}

function AppLogs({ appId, environments }: { appId: string; environments: string[] }) {
  const [env, setEnv] = useState('')
  const [tail, setTail] = useState(100)
  const [previous, setPrevious] = useState(false)
  const [autoRefresh, setAutoRefresh] = useState(false)
  const logEndRef = useRef<HTMLDivElement>(null)

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['appLogs', appId, env, tail, previous],
    queryFn: () => api.getAppLogs(appId, env, { tail, previous }),
    enabled: !!env,
    refetchInterval: autoRefresh ? 5000 : false,
  })

  useEffect(() => {
    if (data && logEndRef.current) {
      logEndRef.current.scrollIntoView({ behavior: 'smooth' })
    }
  }, [data])

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h3 className="section-title">Logs</h3>
        <div className="flex items-center gap-2">
          {env && (
            <>
              <label className="flex items-center gap-1.5 cursor-pointer">
                <input
                  type="checkbox"
                  checked={autoRefresh}
                  onChange={(e) => setAutoRefresh(e.target.checked)}
                  className="w-3.5 h-3.5 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
                />
                <span className="text-[11px] text-text-tertiary">Auto-refresh</span>
              </label>
              <button
                onClick={() => refetch()}
                disabled={isFetching}
                className="btn-ghost text-xs"
              >
                {isFetching ? 'Loading...' : 'Refresh'}
              </button>
            </>
          )}
        </div>
      </div>

      <div className="flex items-center gap-3 mb-4">
        <div>
          <select
            value={env}
            onChange={(e) => setEnv(e.target.value)}
            className="input-field text-xs"
          >
            <option value="">Select environment...</option>
            {environments.map((e) => (
              <option key={e} value={e}>{e}</option>
            ))}
          </select>
        </div>
        <div>
          <select
            value={tail}
            onChange={(e) => setTail(Number(e.target.value))}
            className="input-field text-xs w-24"
          >
            <option value={50}>50 lines</option>
            <option value={100}>100 lines</option>
            <option value={500}>500 lines</option>
            <option value={1000}>1000 lines</option>
          </select>
        </div>
        <label className="flex items-center gap-1.5 cursor-pointer">
          <input
            type="checkbox"
            checked={previous}
            onChange={(e) => setPrevious(e.target.checked)}
            className="w-3.5 h-3.5 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
          />
          <span className="text-[11px] text-text-tertiary">Previous</span>
        </label>
      </div>

      {!env && (
        <p className="text-sm text-text-tertiary">Select an environment to view logs</p>
      )}

      {env && isLoading && (
        <div className="flex items-center justify-center py-8">
          <svg className="w-5 h-5 animate-spin text-accent" fill="none" viewBox="0 0 24 24">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
        </div>
      )}

      {env && data && (
        <div className="space-y-3">
          {data.total === 0 && (
            <p className="text-sm text-text-tertiary">No pods found in this environment</p>
          )}
          {data.pods?.map((pod: any) => (
            <div key={pod.pod} className="border border-border rounded-lg overflow-hidden">
              <div className="bg-surface-1 px-4 py-2.5 flex items-center justify-between border-b border-border">
                <div className="flex items-center gap-3">
                  <span className={`inline-block w-2 h-2 rounded-full ${
                    pod.status === 'Running' ? 'bg-status-running' :
                    pod.status === 'Failed' || pod.status === 'Error' ? 'bg-status-failed' :
                    'bg-status-pending'
                  }`} />
                  <span className="text-xs font-mono text-text-primary">{pod.pod}</span>
                </div>
                <div className="flex items-center gap-3">
                  <span className="text-[11px] text-text-tertiary">{pod.status}</span>
                  {pod.restarts > 0 && (
                    <span className="text-[11px] text-status-failed">{pod.restarts} restart{pod.restarts !== 1 ? 's' : ''}</span>
                  )}
                </div>
              </div>
              <pre className="bg-[#0d1117] text-[#c9d1d9] text-[11px] leading-relaxed p-4 overflow-x-auto max-h-80 overflow-y-auto font-mono whitespace-pre-wrap break-all">
                {pod.logs || '(no output)'}
                <div ref={logEndRef} />
              </pre>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function AppMetrics({ appId, environments }: { appId: string; environments: string[] }) {
  const [env, setEnv] = useState('')
  const [expandedPod, setExpandedPod] = useState<string | null>(null)

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['appMetrics', appId, env],
    queryFn: () => api.getAppMetrics(appId, env),
    enabled: !!env,
    refetchInterval: 15000,
  })

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h3 className="section-title">Metrics</h3>
        {env && (
          <button onClick={() => refetch()} disabled={isFetching} className="btn-ghost text-xs">
            {isFetching ? 'Refreshing...' : 'Refresh'}
          </button>
        )}
      </div>

      <div className="mb-4">
        <select
          value={env}
          onChange={(e) => setEnv(e.target.value)}
          className="input-field text-xs w-48"
        >
          <option value="">Select environment...</option>
          {environments.map((e) => (
            <option key={e} value={e}>{e}</option>
          ))}
        </select>
      </div>

      {!env && (
        <p className="text-sm text-text-tertiary">Select an environment to view metrics</p>
      )}

      {env && isLoading && (
        <div className="flex items-center justify-center py-8">
          <svg className="w-5 h-5 animate-spin text-accent" fill="none" viewBox="0 0 24 24">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
        </div>
      )}

      {env && data && (
        <div className="space-y-4">
          {/* Deployment overview */}
          {data.deployment && (
            <div className="grid grid-cols-4 gap-3">
              <MetricCard label="Desired" value={data.deployment.desiredReplicas ?? '—'} />
              <MetricCard label="Ready" value={data.readyPods ?? 0} total={data.totalPods ?? 0} ok={data.readyPods === data.totalPods} />
              <MetricCard label="Available" value={data.deployment.availableReplicas ?? 0} />
              <MetricCard label="Updated" value={data.deployment.updatedReplicas ?? 0} />
            </div>
          )}

          {/* Resource usage summary */}
          {data.summary && (
            <div className="bg-surface-1 border border-border rounded-lg p-4">
              <h4 className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-3">Resource Usage</h4>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <div className="flex items-center justify-between mb-1.5">
                    <span className="text-[11px] text-text-secondary">CPU</span>
                    <span className="text-[11px] font-mono text-text-primary">
                      {data.summary.totalCPUUsage || '0'} / {data.summary.totalCPURequest || '0'}
                      {data.summary.cpuUtilization !== undefined && (
                        <span className={`ml-1.5 ${data.summary.cpuUtilization > 90 ? 'text-status-failed' : data.summary.cpuUtilization > 70 ? 'text-yellow-400' : 'text-status-running'}`}>
                          ({Math.round(data.summary.cpuUtilization)}%)
                        </span>
                      )}
                    </span>
                  </div>
                  <div className="w-full bg-surface-2 rounded-full h-2">
                    <div
                      className={`h-2 rounded-full transition-all ${(data.summary.cpuUtilization ?? 0) > 90 ? 'bg-status-failed' : (data.summary.cpuUtilization ?? 0) > 70 ? 'bg-yellow-400' : 'bg-accent'}`}
                      style={{ width: `${Math.min(data.summary.cpuUtilization ?? 0, 100)}%` }}
                    />
                  </div>
                </div>
                <div>
                  <div className="flex items-center justify-between mb-1.5">
                    <span className="text-[11px] text-text-secondary">Memory</span>
                    <span className="text-[11px] font-mono text-text-primary">
                      {data.summary.totalMemoryUsage || '0'} / {data.summary.totalMemoryRequest || '0'}
                      {data.summary.memoryUtilization !== undefined && (
                        <span className={`ml-1.5 ${data.summary.memoryUtilization > 90 ? 'text-status-failed' : data.summary.memoryUtilization > 70 ? 'text-yellow-400' : 'text-status-running'}`}>
                          ({Math.round(data.summary.memoryUtilization)}%)
                        </span>
                      )}
                    </span>
                  </div>
                  <div className="w-full bg-surface-2 rounded-full h-2">
                    <div
                      className={`h-2 rounded-full transition-all ${(data.summary.memoryUtilization ?? 0) > 90 ? 'bg-status-failed' : (data.summary.memoryUtilization ?? 0) > 70 ? 'bg-yellow-400' : 'bg-accent'}`}
                      style={{ width: `${Math.min(data.summary.memoryUtilization ?? 0, 100)}%` }}
                    />
                  </div>
                </div>
              </div>
            </div>
          )}

          {/* Autoscaling info */}
          {data.autoscaling && data.autoscaling.maxReplicas && (
            <div className="bg-surface-1 border border-border rounded-lg p-4">
              <h4 className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-3">Autoscaling (HPA)</h4>
              <div className="grid grid-cols-4 gap-3">
                <div>
                  <p className="text-[10px] text-text-tertiary mb-0.5">Min Replicas</p>
                  <p className="text-sm font-mono font-medium text-text-primary">{data.autoscaling.minReplicas ?? 1}</p>
                </div>
                <div>
                  <p className="text-[10px] text-text-tertiary mb-0.5">Max Replicas</p>
                  <p className="text-sm font-mono font-medium text-text-primary">{data.autoscaling.maxReplicas}</p>
                </div>
                <div>
                  <p className="text-[10px] text-text-tertiary mb-0.5">Current</p>
                  <p className="text-sm font-mono font-medium text-text-primary">{data.autoscaling.currentReplicas ?? '—'}</p>
                </div>
                <div>
                  <p className="text-[10px] text-text-tertiary mb-0.5">Desired</p>
                  <p className="text-sm font-mono font-medium text-text-primary">{data.autoscaling.desiredReplicas ?? '—'}</p>
                </div>
              </div>
              {data.autoscaling.currentMetrics?.length > 0 && (
                <div className="mt-3 pt-3 border-t border-border-subtle">
                  <p className="text-[10px] text-text-tertiary mb-2">Current Metrics</p>
                  <div className="flex gap-4">
                    {data.autoscaling.currentMetrics.map((m: any, i: number) => (
                      <span key={i} className="text-xs font-mono text-text-secondary">
                        {m.resource?.name || m.type}: {m.resource?.current?.averageUtilization ?? '—'}%
                        {m.resource?.current?.averageValue ? ` (${m.resource.current.averageValue})` : ''}
                      </span>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}

          {/* Pod table */}
          {data.pods?.length > 0 && (
            <div>
              <h4 className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-2">Pods</h4>
              <div className="border border-border rounded-lg overflow-hidden">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="bg-surface-1 border-b border-border">
                      <th className="text-left px-4 py-2.5 text-text-tertiary font-mono font-medium uppercase tracking-wider text-[10px]">Pod</th>
                      <th className="text-left px-4 py-2.5 text-text-tertiary font-mono font-medium uppercase tracking-wider text-[10px]">Status</th>
                      <th className="text-left px-4 py-2.5 text-text-tertiary font-mono font-medium uppercase tracking-wider text-[10px]">Restarts</th>
                      <th className="text-left px-4 py-2.5 text-text-tertiary font-mono font-medium uppercase tracking-wider text-[10px]">CPU</th>
                      <th className="text-left px-4 py-2.5 text-text-tertiary font-mono font-medium uppercase tracking-wider text-[10px]">Memory</th>
                      <th className="text-left px-4 py-2.5 text-text-tertiary font-mono font-medium uppercase tracking-wider text-[10px]">Age</th>
                      <th className="text-left px-4 py-2.5 text-text-tertiary font-mono font-medium uppercase tracking-wider text-[10px]">Node</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.pods.map((pod: any) => (
                      <>
                        <tr
                          key={pod.name}
                          className={`border-b border-border-subtle last:border-0 hover:bg-surface-1/50 cursor-pointer ${expandedPod === pod.name ? 'bg-surface-1/30' : ''}`}
                          onClick={() => setExpandedPod(expandedPod === pod.name ? null : pod.name)}
                        >
                          <td className="px-4 py-2.5 font-mono text-text-secondary">
                            <div className="flex items-center gap-2">
                              <span className={`inline-block w-1.5 h-1.5 rounded-full ${
                                pod.ready ? 'bg-status-running' :
                                pod.status === 'Failed' ? 'bg-status-failed' :
                                'bg-status-pending'
                              }`} />
                              <span className="truncate max-w-[180px]" title={pod.name}>{pod.name}</span>
                              {pod.containers?.length > 1 && (
                                <span className="text-[10px] text-text-tertiary bg-surface-2 px-1.5 py-0.5 rounded">{pod.containers.length}c</span>
                              )}
                            </div>
                          </td>
                          <td className="px-4 py-2.5">
                            <span className={`${
                              pod.status === 'Running' ? 'text-status-running' :
                              pod.status === 'Failed' ? 'text-status-failed' :
                              'text-status-pending'
                            }`}>{pod.status}</span>
                          </td>
                          <td className="px-4 py-2.5">
                            <span className={pod.restarts > 0 ? 'text-status-failed' : 'text-text-tertiary'}>
                              {pod.restarts}
                            </span>
                          </td>
                          <td className="px-4 py-2.5 font-mono">
                            <div className="flex flex-col gap-0.5">
                              <span className="text-text-secondary">{pod.cpuUsage || '—'}</span>
                              {pod.cpuRequest && <span className="text-[10px] text-text-tertiary">req: {pod.cpuRequest}</span>}
                              {pod.cpuLimit && <span className="text-[10px] text-text-tertiary">lim: {pod.cpuLimit}</span>}
                            </div>
                          </td>
                          <td className="px-4 py-2.5 font-mono">
                            <div className="flex flex-col gap-0.5">
                              <span className="text-text-secondary">{pod.memoryUsage || '—'}</span>
                              {pod.memoryRequest && <span className="text-[10px] text-text-tertiary">req: {pod.memoryRequest}</span>}
                              {pod.memoryLimit && <span className="text-[10px] text-text-tertiary">lim: {pod.memoryLimit}</span>}
                            </div>
                          </td>
                          <td className="px-4 py-2.5 text-text-tertiary" title={pod.startedAt ? new Date(pod.startedAt).toLocaleString() : ''}>
                            {pod.age || '—'}
                          </td>
                          <td className="px-4 py-2.5 text-text-tertiary font-mono truncate max-w-[120px]" title={pod.nodeName}>
                            {pod.nodeName || '—'}
                          </td>
                        </tr>
                        {/* Expanded container details */}
                        {expandedPod === pod.name && pod.containers?.length > 0 && (
                          <tr key={`${pod.name}-detail`}>
                            <td colSpan={7} className="px-0 py-0">
                              <div className="bg-surface-1/50 border-t border-border-subtle">
                                <div className="px-6 py-2">
                                  <p className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-1.5">
                                    Containers ({pod.containers.length})
                                    {pod.ip && <span className="ml-3 normal-case tracking-normal">IP: {pod.ip}</span>}
                                  </p>
                                  <div className="space-y-1.5">
                                    {pod.containers.map((ctr: any) => (
                                      <div key={ctr.name} className="flex items-center gap-4 text-[11px]">
                                        <div className="flex items-center gap-1.5 min-w-[120px]">
                                          <span className={`inline-block w-1.5 h-1.5 rounded-full ${
                                            ctr.ready ? 'bg-status-running' :
                                            ctr.state === 'running' ? 'bg-status-pending' :
                                            'bg-status-failed'
                                          }`} />
                                          <span className="font-mono text-text-primary">{ctr.name}</span>
                                        </div>
                                        <span className="text-text-tertiary font-mono truncate max-w-[200px]" title={ctr.image}>{ctr.image}</span>
                                        <span className={`${ctr.state === 'running' ? 'text-status-running' : 'text-status-pending'}`}>{ctr.state || '—'}</span>
                                        <span className="font-mono text-text-secondary">cpu: {ctr.cpuUsage || '—'}{ctr.cpuRequest ? ` / ${ctr.cpuRequest}` : ''}</span>
                                        <span className="font-mono text-text-secondary">mem: {ctr.memoryUsage || '—'}{ctr.memoryRequest ? ` / ${ctr.memoryRequest}` : ''}</span>
                                        {ctr.restarts > 0 && <span className="text-status-failed">{ctr.restarts} restarts</span>}
                                      </div>
                                    ))}
                                  </div>
                                </div>
                              </div>
                            </td>
                          </tr>
                        )}
                      </>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {data.totalPods === 0 && (
            <p className="text-sm text-text-tertiary">No pods found in this environment</p>
          )}
        </div>
      )}
    </div>
  )
}

function MetricCard({ label, value, total, ok }: { label: string; value: any; total?: number; ok?: boolean }) {
  return (
    <div className="bg-surface-1 border border-border rounded-lg px-4 py-3 text-center">
      <p className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-1">{label}</p>
      <p className={`text-lg font-display font-bold ${
        ok === true ? 'text-status-running' :
        ok === false ? 'text-status-failed' :
        'text-text-primary'
      }`}>
        {value}{total !== undefined ? <span className="text-xs text-text-tertiary font-normal">/{total}</span> : null}
      </p>
    </div>
  )
}

function EnvSecrets({ appId, env }: { appId: string; env: string }) {
  const queryClient = useQueryClient()
  const { data, isLoading } = useQuery({
    queryKey: ['appEnvSecrets', appId, env],
    queryFn: () => api.listAppEnvSecrets(appId, env),
  })

  const [showAdd, setShowAdd] = useState(false)
  const [newKeys, setNewKeys] = useState([{ key: '', value: '' }])

  const addMutation = useMutation({
    mutationFn: () => {
      const secretData: Record<string, string> = {}
      newKeys.forEach((kv) => { if (kv.key) secretData[kv.key] = kv.value })
      return api.createAppEnvSecret(appId, env, { data: secretData })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['appEnvSecrets', appId, env] })
      setShowAdd(false)
      setNewKeys([{ key: '', value: '' }])
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (key: string) => api.deleteAppEnvSecretKey(appId, env, key),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['appEnvSecrets', appId, env] }),
  })

  if (isLoading) return <p className="text-xs text-text-tertiary">Loading secrets...</p>

  const existingKeys: string[] = data?.items?.[0]?.keys || []

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-xs text-text-tertiary">{existingKeys.length} key{existingKeys.length !== 1 ? 's' : ''} in {env}</p>
        <button onClick={() => setShowAdd(!showAdd)} className="text-xs text-accent hover:text-accent-glow transition-colors font-mono">
          + Add Keys
        </button>
      </div>

      {existingKeys.length > 0 && (
        <div className="space-y-1">
          {existingKeys.map((k: string) => (
            <div key={k} className="flex items-center justify-between px-3 py-2 bg-surface-1 border border-border rounded-lg group">
              <span className="text-xs font-mono text-text-secondary">{k}</span>
              <button
                onClick={() => {
                  if (confirm(`Delete key "${k}" from ${env} secrets?`))
                    deleteMutation.mutate(k)
                }}
                className="text-xs text-text-tertiary hover:text-status-failed transition-colors opacity-0 group-hover:opacity-100"
              >
                Remove
              </button>
            </div>
          ))}
        </div>
      )}

      {showAdd && (
        <form
          onSubmit={(e) => { e.preventDefault(); addMutation.mutate() }}
          className="bg-surface-1 border border-border rounded-lg p-4 space-y-3 animate-slide-up"
        >
          <div>
            <label className="label">Key-Value Pairs</label>
            <div className="space-y-2">
              {newKeys.map((kv, i) => (
                <div key={i} className="flex gap-2">
                  <input
                    value={kv.key}
                    onChange={(e) => { const u = [...newKeys]; u[i].key = e.target.value; setNewKeys(u) }}
                    placeholder="KEY"
                    className="input-field flex-1 font-mono text-xs"
                  />
                  <input
                    value={kv.value}
                    onChange={(e) => { const u = [...newKeys]; u[i].value = e.target.value; setNewKeys(u) }}
                    placeholder="value"
                    type="password"
                    className="input-field flex-1"
                  />
                  {newKeys.length > 1 && (
                    <button type="button" onClick={() => setNewKeys(newKeys.filter((_, idx) => idx !== i))} className="px-2 text-text-tertiary hover:text-status-failed transition-colors">
                      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  )}
                </div>
              ))}
            </div>
            <button type="button" onClick={() => setNewKeys([...newKeys, { key: '', value: '' }])} className="text-xs text-accent hover:text-accent-glow transition-colors mt-2 font-mono">
              + Add another
            </button>
          </div>
          <div className="flex gap-3">
            <button type="submit" disabled={addMutation.isPending || !newKeys.some(kv => kv.key)} className="btn-primary text-xs">
              {addMutation.isPending ? 'Saving...' : 'Save'}
            </button>
            <button type="button" onClick={() => setShowAdd(false)} className="btn-ghost text-xs">Cancel</button>
          </div>
          {addMutation.isError && (
            <p className="text-status-failed text-xs">{(addMutation.error as Error).message}</p>
          )}
        </form>
      )}
    </div>
  )
}

function InfoItem({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="text-[11px] font-mono text-text-tertiary uppercase tracking-wider mb-1">{label}</p>
      {children}
    </div>
  )
}

function ConfigItem({ label, value, mono, accent }: { label: string; value?: any; mono?: boolean; accent?: boolean }) {
  const display = value ?? '—'
  return (
    <div className="flex items-center justify-between py-1.5">
      <span className="text-xs text-text-tertiary">{label}</span>
      <span className={`text-xs ${accent ? 'text-accent' : 'text-text-secondary'} ${mono ? 'font-mono' : ''}`}>
        {display}
      </span>
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
