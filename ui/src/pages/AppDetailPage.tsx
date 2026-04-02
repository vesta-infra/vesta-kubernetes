import { useState, useRef, useEffect, useMemo } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { XAxis, YAxis, Tooltip, ResponsiveContainer, Area, AreaChart } from 'recharts'
import { api } from '../lib/api'
import { useUserRole } from '../lib/useRole'
import RevealableInput from '../components/RevealableInput'

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
  const [activeTab, setActiveTab] = useState<'overview' | 'builds' | 'secrets' | 'logs' | 'terminal' | 'metrics'>('overview')
  const [showCloneModal, setShowCloneModal] = useState(false)
  const role = useUserRole()

  const projectId = app?.project || app?.spec?.project
  const rawEnvs = app?.environments || app?.spec?.environments || []
  const appEnvironments: string[] = rawEnvs.map((e: any) => typeof e === 'string' ? e : e.name)

  // Default selectors to first environment
  useEffect(() => {
    if (appEnvironments.length > 0) {
      if (!secretEnv) setSecretEnv(appEnvironments[0])
      if (!deployEnv) setDeployEnv(appEnvironments[0])
    }
  }, [appEnvironments.join(',')])

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

  const cloneMutation = useMutation({
    mutationFn: (data: { name: string; project?: string }) => api.cloneApp(appId!, data),
    onSuccess: (res) => {
      setShowCloneModal(false)
      queryClient.invalidateQueries({ queryKey: ['apps'] })
      if (res?.id) navigate(`/apps/${res.id}`)
    },
  })

  const wakeMutation = useMutation({
    mutationFn: () => api.wakeApp(appId!),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['app', appId] }),
  })

  const sleepMutation = useMutation({
    mutationFn: () => api.sleepApp(appId!),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['app', appId] }),
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
            <h2 className="text-xl font-display italic text-text-primary">{app.name}</h2>
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
          {role !== 'viewer' && (
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
          )}
          {role !== 'viewer' && phase === 'Sleeping' && (
          <button
            onClick={() => wakeMutation.mutate()}
            disabled={wakeMutation.isPending}
            className="btn-primary text-xs"
          >
            {wakeMutation.isPending ? 'Waking...' : 'Wake Up'}
          </button>
          )}
          {role !== 'viewer' && phase === 'Running' && app.spec?.sleep?.enabled && (
          <button
            onClick={() => sleepMutation.mutate()}
            disabled={sleepMutation.isPending}
            className="btn-ghost text-xs text-status-pending"
          >
            {sleepMutation.isPending ? 'Sleeping...' : 'Sleep'}
          </button>
          )}
          {role !== 'viewer' && (
          <button
            onClick={() => setShowCloneModal(true)}
            className="btn-ghost text-xs"
          >
            <span className="flex items-center gap-1.5">
              <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
              </svg>
              Clone
            </span>
          </button>
          )}
          {role !== 'viewer' && (
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
          )}
        </div>
      </div>

      {showCloneModal && (
        <CloneAppModal
          appName={app.name}
          isPending={cloneMutation.isPending}
          error={cloneMutation.error}
          onClone={(data) => cloneMutation.mutate(data)}
          onClose={() => setShowCloneModal(false)}
        />
      )}

      {editing && (
        <EditAppForm appId={appId!} app={app} onClose={() => setEditing(false)} />
      )}

      <div className="flex border-b border-border">
        {(['overview', 'builds', ...(role !== 'viewer' ? ['secrets' as const, 'logs' as const, 'terminal' as const] : ['logs' as const]), 'metrics'] as const).map((tab) => (
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
                {app.spec?.sleep?.enabled && (
                  <InfoItem label="Sleep Mode">
                    <span className="text-xs font-mono text-status-pending">
                      {phase === 'Sleeping' ? 'Sleeping' : `Active (timeout: ${app.spec.sleep.inactivityTimeout || '30m'})`}
                    </span>
                  </InfoItem>
                )}
              </div>
            </section>

            <section className="card p-5">
              <div className="flex items-center justify-between mb-4">
                <h3 className="section-title">Environments</h3>
                {availableEnvs.length > 0 && role !== 'viewer' && (
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
                  {appEnvironments.map((env: string) => {
                    const envConfig = (projectEnvs?.items || []).find((e: any) => e.name === env)
                    return (
                      <EnvironmentRow
                        key={env}
                        env={env}
                        envConfig={envConfig}
                        projectId={projectId}
                        appName={app.name}
                        appGitRepo={app.spec?.git?.repository || ''}
                        canRemove={appEnvironments.length > 1}
                        role={role}
                        onRestart={() => restartMutation.mutate(env)}
                        onRemove={() => removeEnvMutation.mutate(env)}
                        restartPending={restartMutation.isPending}
                        removePending={removeEnvMutation.isPending}
                      />
                    )
                  })}
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
                        {d.version && role !== 'viewer' && (
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
            {role !== 'viewer' && (
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
            )}

            <section className="card p-5">
              <h3 className="section-title mb-4">Configuration</h3>
              <div className="space-y-3">
                {app.spec?.git?.repository && (
                  <>
                    <div className="flex items-center gap-2 mb-1">
                      <svg className="w-4 h-4 text-text-tertiary" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
                      <span className="text-[10px] font-mono uppercase tracking-wider text-text-tertiary">Git Source</span>
                    </div>
                    <ConfigItem label="Repository" value={app.spec.git.repository} mono />
                    <ConfigItem label="Branch" value={app.spec.git.branch || 'main'} mono />
                    <ConfigItem label="Auto-deploy" value={app.spec.git.autoDeployOnPush ? 'Enabled' : 'Disabled'} accent={app.spec.git.autoDeployOnPush} />
                    {app.spec?.build?.strategy && (
                      <ConfigItem label="Build Strategy" value={app.spec.build.strategy} mono />
                    )}
                    {app.status?.lastCommitSHA && (
                      <ConfigItem label="Last Commit" value={app.status.lastCommitSHA.slice(0, 8)} mono />
                    )}
                    <div className="border-t border-border-subtle my-3" />
                  </>
                )}
                <ConfigItem label="Image Repository" value={app.spec?.image?.repository} mono />
                {app.spec?.service?.ports?.length > 0 ? (
                  <>
                    <ConfigItem label="Service Type" value={app.spec.service.type || 'ClusterIP'} />
                    <div>
                      <dt className="text-[10px] font-mono uppercase tracking-wider text-text-tertiary mb-1">Ports</dt>
                      <dd className="flex flex-wrap gap-1.5">
                        {app.spec.service.ports.map((p: any) => (
                          <span key={p.name} className="px-2 py-0.5 bg-surface-1 border border-border rounded text-xs font-mono">
                            {p.name}: {p.port}→{p.targetPort || p.port}{p.nodePort ? ` (node:${p.nodePort})` : ''} {p.protocol !== 'TCP' ? p.protocol : ''}
                          </span>
                        ))}
                      </dd>
                    </div>
                  </>
                ) : (
                  <ConfigItem label="Port" value={app.spec?.runtime?.port} />
                )}
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
              {app.spec?.cronjobs?.length > 0 && (
                <div className="mt-5 pt-4 border-t border-border-subtle">
                  <h4 className="text-[11px] font-mono uppercase tracking-wider text-text-tertiary mb-3">Cron Jobs</h4>
                  <div className="space-y-2">
                    {app.spec.cronjobs.map((cj: any) => (
                      <div key={cj.name} className="px-3 py-2 bg-surface-1 border border-border rounded-lg">
                        <div className="flex items-center justify-between">
                          <div className="flex items-center gap-3">
                            <span className="text-xs font-mono text-accent">{cj.name}</span>
                            <span className="text-[11px] font-mono text-text-tertiary bg-surface-3 px-2 py-0.5 rounded">{cj.schedule}</span>
                          </div>
                          <div className="flex items-center gap-3">
                            <span className="text-xs text-text-secondary font-mono truncate max-w-[200px]">{cj.command}</span>
                            {cj.resources?.size && (
                              <span className="text-[10px] text-text-tertiary bg-surface-3 px-1.5 py-0.5 rounded">{cj.resources.size}</span>
                            )}
                          </div>
                        </div>
                        {cj.environments?.length > 0 && (
                          <div className="mt-2 pt-1.5 border-t border-border-subtle flex flex-wrap gap-2">
                            {cj.environments.map((envOvr: any) => (
                              <span key={envOvr.name} className={`text-[10px] font-mono px-2 py-0.5 rounded border ${envOvr.enabled === false ? 'bg-status-failed/5 text-status-failed border-status-failed/20 line-through' : 'bg-surface-3 text-text-tertiary border-border/50'}`}>
                                {envOvr.name}{envOvr.enabled === false ? ' (disabled)' : ''}{envOvr.schedule ? `: ${envOvr.schedule}` : ''}
                              </span>
                            ))}
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              )}
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

          {projectId && (
            <SharedSecretBindings appId={appId!} projectId={projectId} environments={appEnvironments} />
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

      {activeTab === 'terminal' && (
        <div>
          {appEnvironments.length > 0 ? (
            <AppTerminal appId={appId!} environments={appEnvironments} />
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

      {activeTab === 'builds' && (
        <AppBuilds appId={appId!} environments={appEnvironments} gitRepo={app?.spec?.git?.repository || ''} />
      )}
    </div>
  )
}

function EnvironmentRow({ env, envConfig, projectId, appName, appGitRepo, canRemove, role, onRestart, onRemove, restartPending, removePending }: {
  env: string; envConfig: any; projectId: string; appName: string; appGitRepo: string; canRemove: boolean; role: string
  onRestart: () => void; onRemove: () => void; restartPending: boolean; removePending: boolean
}) {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [branch, setBranch] = useState(envConfig?.branch || '')
  const [autoDeploy, setAutoDeploy] = useState(envConfig?.autoDeploy || false)

  const { data: branchesData } = useQuery({
    queryKey: ['repoBranches', appGitRepo],
    queryFn: () => api.listRepoBranches(appGitRepo),
    enabled: editing && !!appGitRepo && appGitRepo.includes('/'),
    staleTime: 60_000,
  })
  const branches: string[] = branchesData?.branches || []

  const updateMutation = useMutation({
    mutationFn: () => api.updateEnvironment(projectId, env, { branch, autoDeploy }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['environments', projectId] })
      setEditing(false)
    },
  })

  return (
    <div className="bg-surface-1 border border-border rounded-lg group">
      <div className="flex items-center justify-between px-4 py-3">
        <div className="flex items-center gap-3">
          <span className="text-sm font-mono text-text-secondary">{env}</span>
          {envConfig?.branch && (
            <span className="text-[11px] font-mono text-text-tertiary bg-surface-3 px-1.5 py-0.5 rounded">
              {envConfig.branch}
            </span>
          )}
          {envConfig?.autoDeploy && (
            <span className="text-[11px] font-mono text-accent bg-accent/10 px-1.5 py-0.5 rounded">auto-deploy</span>
          )}
        </div>
        <div className="flex items-center gap-3 opacity-0 group-hover:opacity-100 transition-opacity">
          {role !== 'viewer' && (
            <button
              onClick={() => setEditing(!editing)}
              className="text-xs text-text-tertiary hover:text-accent transition-colors"
            >
              {editing ? 'Cancel' : 'Configure'}
            </button>
          )}
          {role !== 'viewer' && (
            <button
              onClick={() => { if (confirm(`Restart "${appName}" in "${env}"?`)) onRestart() }}
              disabled={restartPending}
              className="text-xs text-text-tertiary hover:text-accent transition-colors"
            >
              Restart
            </button>
          )}
          {canRemove && role !== 'viewer' && (
            <button
              onClick={() => { if (confirm(`Remove "${appName}" from "${env}"?`)) onRemove() }}
              disabled={removePending}
              className="text-xs text-text-tertiary hover:text-status-failed transition-colors"
            >
              Remove
            </button>
          )}
        </div>
      </div>
      {editing && (
        <div className="px-4 pb-3 pt-1 border-t border-border-subtle">
          <div className="flex items-end gap-3">
            <div className="flex-1">
              <label className="text-[11px] text-text-tertiary mb-1 block">Branch</label>
              {branches.length > 0 ? (
                <select
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  className="input-field font-mono text-xs w-full"
                >
                  <option value="">No branch</option>
                  {branches.map(b => (
                    <option key={b} value={b}>{b}</option>
                  ))}
                </select>
              ) : (
                <input
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  className="input-field font-mono text-xs w-full"
                  placeholder="main"
                />
              )}
            </div>
            <label className="flex items-center gap-2 cursor-pointer pb-1.5">
              <input
                type="checkbox"
                checked={autoDeploy}
                onChange={(e) => setAutoDeploy(e.target.checked)}
                className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
              />
              <span className="text-xs text-text-secondary whitespace-nowrap">Auto-deploy on push</span>
            </label>
            <button
              onClick={() => updateMutation.mutate()}
              disabled={updateMutation.isPending}
              className="btn-primary text-xs whitespace-nowrap"
            >
              {updateMutation.isPending ? 'Saving...' : 'Save'}
            </button>
          </div>
          {updateMutation.isError && (
            <p className="text-xs text-status-failed mt-2">{(updateMutation.error as Error).message}</p>
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
  const [pullPolicy, setPullPolicy] = useState(app.spec?.image?.pullPolicy || 'IfNotPresent')
  const [port, setPort] = useState(String(app.spec?.runtime?.port || 3000))
  const [domain, setDomain] = useState(app.spec?.ingress?.domain || '')
  const [tls, setTls] = useState(app.spec?.ingress?.tls || false)

  // Service config (multi-port + service type)
  const [serviceType, setServiceType] = useState<string>(app.spec?.service?.type || 'ClusterIP')
  const [servicePorts, setServicePorts] = useState<{ name: string; port: string; targetPort: string; protocol: string; nodePort: string }[]>(() => {
    const ports = app.spec?.service?.ports
    if (ports && ports.length > 0) {
      return ports.map((p: any) => ({
        name: p.name || '',
        port: String(p.port || ''),
        targetPort: String(p.targetPort || ''),
        protocol: p.protocol || 'TCP',
        nodePort: String(p.nodePort || ''),
      }))
    }
    return []
  })
  const useServiceConfig = servicePorts.length > 0

  // Health check config
  const [hcEnabled, setHcEnabled] = useState(!!app.spec?.healthCheck)
  const [hcType, setHcType] = useState(app.spec?.healthCheck?.type || 'http')
  const [hcPath, setHcPath] = useState(app.spec?.healthCheck?.path || '/')
  const [hcPort, setHcPort] = useState(String(app.spec?.healthCheck?.port || ''))
  const [hcCommand, setHcCommand] = useState(app.spec?.healthCheck?.command || '')
  const [hcInitialDelay, setHcInitialDelay] = useState(String(app.spec?.healthCheck?.initialDelaySeconds || 0))
  const [hcPeriod, setHcPeriod] = useState(String(app.spec?.healthCheck?.periodSeconds || 10))
  const [hcTimeout, setHcTimeout] = useState(String(app.spec?.healthCheck?.timeoutSeconds || 1))
  const [hcFailureThreshold, setHcFailureThreshold] = useState(String(app.spec?.healthCheck?.failureThreshold || 3))

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
  const appEnvironments: string[] = rawEnvs.map((e: any) => typeof e === 'string' ? e : e.name)
  const [envConfigs, setEnvConfigs] = useState<Record<string, { replicas: number; podSize: string; autoscaleEnabled: boolean; minReplicas: number; maxReplicas: number; targetCPU: number }>>(() => {
    const configs: Record<string, any> = {}
    for (const e of rawEnvs) {
      const env = typeof e === 'string' ? { name: e } : e
      configs[env.name] = {
        replicas: env.replicas ?? 1,
        podSize: env.resources?.size || '',
        autoscaleEnabled: env.autoscale?.enabled || false,
        minReplicas: env.autoscale?.minReplicas || 1,
        maxReplicas: env.autoscale?.maxReplicas || 5,
        targetCPU: env.autoscale?.metrics?.[0]?.targetAverageUtilization || env.autoscale?.targetCPU || 80,
      }
    }
    return configs
  })

  const { data: podSizes } = useQuery({
    queryKey: ['podSizes'],
    queryFn: () => api.listPodSizes(),
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

  // Cron jobs
  const [cronjobs, setCronjobs] = useState<{ name: string; schedule: string; command: string; size: string; environments: { name: string; enabled: boolean; schedule: string }[] }[]>(() => {
    return (app.spec?.cronjobs || []).map((cj: any) => ({
      name: cj.name || '',
      schedule: cj.schedule || '',
      command: cj.command || '',
      size: cj.resources?.size || '',
      environments: (cj.environments || []).map((e: any) => ({
        name: e.name || '',
        enabled: e.enabled !== false,
        schedule: e.schedule || '',
      })),
    }))
  })

  // Volumes (PVC mounts)
  const [volumes, setVolumes] = useState<{ name: string; mountPath: string; claimName: string }[]>(() => {
    return (app.spec?.runtime?.volumes || []).map((v: any) => ({
      name: v.name || '',
      mountPath: v.mountPath || '',
      claimName: v.persistentVolumeClaim?.claimName || '',
    }))
  })

  // Git source
  const [gitProvider, setGitProvider] = useState(app.spec?.git?.provider || 'github')
  const [gitRepo, setGitRepo] = useState(app.spec?.git?.repository || '')
  const [gitBranch, setGitBranch] = useState(app.spec?.git?.branch || '')
  const [gitAutoDeploy, setGitAutoDeploy] = useState(app.spec?.git?.autoDeployOnPush || false)
  const [gitTokenSecret, setGitTokenSecret] = useState(app.spec?.git?.tokenSecret || '')

  const { data: branchesData } = useQuery({
    queryKey: ['repoBranches', gitRepo],
    queryFn: () => api.listRepoBranches(gitRepo),
    enabled: !!gitRepo && gitRepo.includes('/'),
    staleTime: 60_000,
  })
  const branches: string[] = branchesData?.branches || []

  const { data: accessibleRepos } = useQuery({
    queryKey: ['accessibleRepos'],
    queryFn: () => api.listAccessibleRepos(),
    staleTime: 60_000,
  })
  const repos = accessibleRepos?.repos || []

  const { data: ghStatus } = useQuery({
    queryKey: ['github-app-status'],
    queryFn: () => api.getGitHubAppStatus(),
    staleTime: 60_000,
  })

  // Build config
  const [buildStrategy, setBuildStrategy] = useState(app.spec?.build?.strategy || '')
  const [buildDockerfile, setBuildDockerfile] = useState(app.spec?.build?.dockerfile || 'Dockerfile')

  // Sleep / Scale-to-Zero
  const [sleepEnabled, setSleepEnabled] = useState(app.spec?.sleep?.enabled || false)
  const [sleepTimeout, setSleepTimeout] = useState(app.spec?.sleep?.inactivityTimeout || '30m')

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
        pullPolicy,
        ...(pullSecrets.length > 0 && { imagePullSecrets: pullSecrets.map(n => ({ name: n })) }),
      }
    }

    // Volumes
    const validVolumes = volumes.filter(v => v.name && v.mountPath && v.claimName).map(v => ({
      name: v.name,
      mountPath: v.mountPath,
      persistentVolumeClaim: { claimName: v.claimName },
    }))

    // Runtime & Service
    if (useServiceConfig) {
      patch.runtime = { port: 0, ...(validVolumes.length > 0 && { volumes: validVolumes }) }
      patch.service = {
        type: serviceType,
        ports: servicePorts.filter(p => p.name && p.port).map(p => ({
          name: p.name,
          port: parseInt(p.port),
          ...(p.targetPort && { targetPort: parseInt(p.targetPort) }),
          protocol: p.protocol || 'TCP',
          ...(serviceType === 'NodePort' && p.nodePort && { nodePort: parseInt(p.nodePort) }),
        })),
      }
    } else {
      patch.runtime = { port: Number.parseInt(port) || 3000, ...(validVolumes.length > 0 && { volumes: validVolumes }) }
      patch.service = null
    }

    // Ingress
    if (domain) {
      patch.ingress = { domain, tls }
    }

    // Health check
    if (hcEnabled) {
      patch.healthCheck = {
        type: hcType,
        ...(hcType === 'http' && { path: hcPath }),
        ...(hcType !== 'exec' && hcPort && { port: parseInt(hcPort) }),
        ...(hcType === 'exec' && { command: hcCommand }),
        initialDelaySeconds: parseInt(hcInitialDelay) || 0,
        periodSeconds: parseInt(hcPeriod) || 10,
        timeoutSeconds: parseInt(hcTimeout) || 1,
        failureThreshold: parseInt(hcFailureThreshold) || 3,
      }
    } else {
      patch.healthCheck = null
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
      if (cfg.podSize) {
        env.resources = { size: cfg.podSize }
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

    // Cron jobs
    const validCronjobs = cronjobs.filter(cj => cj.name && cj.schedule && cj.command)
    patch.cronjobs = validCronjobs.map(cj => ({
      name: cj.name,
      schedule: cj.schedule,
      command: cj.command,
      ...(cj.size && { resources: { size: cj.size } }),
      ...(cj.environments.length > 0 && {
        environments: cj.environments.filter(e => e.name).map(e => ({
          name: e.name,
          ...((!e.enabled) && { enabled: false }),
          ...(e.schedule && { schedule: e.schedule }),
        })),
      }),
    }))

    // Git source
    if (gitRepo) {
      patch.git = {
        provider: gitProvider,
        repository: gitRepo,
        branch: gitBranch || 'main',
        autoDeployOnPush: gitAutoDeploy,
        ...(gitTokenSecret && { tokenSecret: gitTokenSecret }),
      }
    } else {
      patch.git = null
    }

    // Build config
    if (buildStrategy && buildStrategy !== 'image') {
      patch.build = {
        strategy: buildStrategy,
        ...(buildStrategy === 'dockerfile' && buildDockerfile !== 'Dockerfile' && { dockerfile: buildDockerfile }),
      }
    } else if (buildStrategy === 'image' || !buildStrategy) {
      patch.build = null
    }

    // Sleep / Scale-to-Zero
    if (sleepEnabled) {
      patch.sleep = { enabled: true, inactivityTimeout: sleepTimeout }
    } else {
      patch.sleep = { enabled: false }
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
        <div>
          <label className="label">Pull Policy</label>
          <select value={pullPolicy} onChange={e => setPullPolicy(e.target.value)} className="input-field">
            <option value="IfNotPresent">IfNotPresent</option>
            <option value="Always">Always</option>
            <option value="Never">Never</option>
          </select>
        </div>
      </div>

      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <label className="label mb-0">Networking</label>
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={useServiceConfig}
              onChange={(e) => {
                if (e.target.checked) {
                  setServicePorts([{ name: 'http', port: port || '80', targetPort: port || '3000', protocol: 'TCP', nodePort: '' }])
                } else {
                  setServicePorts([])
                }
              }}
              className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
            />
            <span className="text-xs text-text-secondary">Multi-port / Service config</span>
          </label>
        </div>

        {!useServiceConfig ? (
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
        ) : (
          <>
            <div className="grid grid-cols-3 gap-4">
              <div>
                <label className="label">Service Type</label>
                <select value={serviceType} onChange={e => setServiceType(e.target.value)} className="input-field">
                  <option value="ClusterIP">ClusterIP</option>
                  <option value="NodePort">NodePort</option>
                  <option value="LoadBalancer">LoadBalancer</option>
                </select>
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

            <div className="border border-border rounded-lg overflow-hidden">
              <table className="w-full text-xs">
                <thead>
                  <tr className="bg-surface-1 text-text-tertiary">
                    <th className="text-left px-3 py-2 font-medium">Name</th>
                    <th className="text-left px-3 py-2 font-medium">Port</th>
                    <th className="text-left px-3 py-2 font-medium">Target Port</th>
                    <th className="text-left px-3 py-2 font-medium">Protocol</th>
                    {serviceType === 'NodePort' && <th className="text-left px-3 py-2 font-medium">Node Port</th>}
                    <th className="w-10"></th>
                  </tr>
                </thead>
                <tbody>
                  {servicePorts.map((sp, i) => (
                    <tr key={i} className="border-t border-border">
                      <td className="px-2 py-1"><input value={sp.name} onChange={e => { const u = [...servicePorts]; u[i] = { ...sp, name: e.target.value }; setServicePorts(u) }} className="input-field text-xs" placeholder="http" /></td>
                      <td className="px-2 py-1"><input type="number" value={sp.port} onChange={e => { const u = [...servicePorts]; u[i] = { ...sp, port: e.target.value }; setServicePorts(u) }} className="input-field text-xs" placeholder="80" /></td>
                      <td className="px-2 py-1"><input type="number" value={sp.targetPort} onChange={e => { const u = [...servicePorts]; u[i] = { ...sp, targetPort: e.target.value }; setServicePorts(u) }} className="input-field text-xs" placeholder="3000" /></td>
                      <td className="px-2 py-1">
                        <select value={sp.protocol} onChange={e => { const u = [...servicePorts]; u[i] = { ...sp, protocol: e.target.value }; setServicePorts(u) }} className="input-field text-xs">
                          <option value="TCP">TCP</option>
                          <option value="UDP">UDP</option>
                        </select>
                      </td>
                      {serviceType === 'NodePort' && (
                        <td className="px-2 py-1"><input type="number" value={sp.nodePort} onChange={e => { const u = [...servicePorts]; u[i] = { ...sp, nodePort: e.target.value }; setServicePorts(u) }} className="input-field text-xs" placeholder="30000-32767" /></td>
                      )}
                      <td className="px-2 py-1">
                        {servicePorts.length > 1 && (
                          <button type="button" onClick={() => setServicePorts(servicePorts.filter((_, j) => j !== i))} className="text-text-tertiary hover:text-status-failed transition-colors">×</button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <button type="button" onClick={() => setServicePorts([...servicePorts, { name: '', port: '', targetPort: '', protocol: 'TCP', nodePort: '' }])} className="btn-ghost text-xs">+ Add Port</button>
          </>
        )}
      </div>

      <div>
        <label className="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={hcEnabled}
            onChange={(e) => setHcEnabled(e.target.checked)}
            className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
          />
          <span className="label mb-0">Health Check</span>
        </label>
        {hcEnabled && (
          <div className="mt-3 pl-6 space-y-3">
            <div className="flex items-center gap-4 flex-wrap">
              <div>
                <label className="text-xs text-text-tertiary">Type</label>
                <select value={hcType} onChange={(e) => setHcType(e.target.value)} className="input-field w-28 mt-1">
                  <option value="http">HTTP</option>
                  <option value="tcp">TCP</option>
                  <option value="exec">Exec</option>
                </select>
              </div>
              {hcType === 'http' && (
                <div>
                  <label className="text-xs text-text-tertiary">Path</label>
                  <input value={hcPath} onChange={(e) => setHcPath(e.target.value)} className="input-field w-32 mt-1" placeholder="/healthz" />
                </div>
              )}
              {hcType !== 'exec' && (
                <div>
                  <label className="text-xs text-text-tertiary">Port</label>
                  <input type="number" value={hcPort} onChange={(e) => setHcPort(e.target.value)} className="input-field w-20 mt-1" placeholder={port} />
                </div>
              )}
              {hcType === 'exec' && (
                <div className="flex-1">
                  <label className="text-xs text-text-tertiary">Command</label>
                  <input value={hcCommand} onChange={(e) => setHcCommand(e.target.value)} className="input-field mt-1 font-mono text-xs" placeholder="cat /tmp/healthy" />
                </div>
              )}
            </div>
            <div className="flex items-center gap-4 flex-wrap">
              <div>
                <label className="text-xs text-text-tertiary">Initial Delay (s)</label>
                <input type="number" min="0" value={hcInitialDelay} onChange={(e) => setHcInitialDelay(e.target.value)} className="input-field w-20 mt-1" />
              </div>
              <div>
                <label className="text-xs text-text-tertiary">Period (s)</label>
                <input type="number" min="1" value={hcPeriod} onChange={(e) => setHcPeriod(e.target.value)} className="input-field w-20 mt-1" />
              </div>
              <div>
                <label className="text-xs text-text-tertiary">Timeout (s)</label>
                <input type="number" min="1" value={hcTimeout} onChange={(e) => setHcTimeout(e.target.value)} className="input-field w-20 mt-1" />
              </div>
              <div>
                <label className="text-xs text-text-tertiary">Failure Threshold</label>
                <input type="number" min="1" value={hcFailureThreshold} onChange={(e) => setHcFailureThreshold(e.target.value)} className="input-field w-20 mt-1" />
              </div>
            </div>
          </div>
        )}
      </div>

      <div>
        <label className="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={sleepEnabled}
            onChange={(e) => setSleepEnabled(e.target.checked)}
            className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
          />
          <span className="label mb-0">Scale-to-Zero (Sleep Mode)</span>
        </label>
        <p className="text-[11px] text-text-tertiary mt-1 ml-6">
          Automatically scale down to zero replicas after inactivity. Traffic will wake the app.
        </p>
        {sleepEnabled && (
          <div className="mt-3 ml-6">
            <label className="text-xs text-text-tertiary">Inactivity Timeout</label>
            <select value={sleepTimeout} onChange={(e) => setSleepTimeout(e.target.value)} className="input-field w-36 mt-1">
              <option value="5m">5 minutes</option>
              <option value="15m">15 minutes</option>
              <option value="30m">30 minutes</option>
              <option value="1h">1 hour</option>
              <option value="2h">2 hours</option>
              <option value="6h">6 hours</option>
              <option value="12h">12 hours</option>
              <option value="24h">24 hours</option>
            </select>
          </div>
        )}
      </div>

      <div>
        <div className="flex items-center justify-between mb-2">
          <label className="label">Volumes</label>
          <button type="button" onClick={() => setVolumes(prev => [...prev, { name: '', mountPath: '', claimName: '' }])} className="text-xs text-accent hover:text-accent-glow">+ Add</button>
        </div>
        <p className="text-[11px] text-text-tertiary mb-2">
          Mount existing PersistentVolumeClaims into the container.
        </p>
        {volumes.map((v, i) => (
          <div key={i} className="flex gap-2 mb-2">
            <input value={v.name} onChange={e => { const u = [...volumes]; u[i] = { ...v, name: e.target.value }; setVolumes(u) }} placeholder="volume name" className="input-field flex-1 font-mono text-xs" />
            <input value={v.mountPath} onChange={e => { const u = [...volumes]; u[i] = { ...v, mountPath: e.target.value }; setVolumes(u) }} placeholder="/data" className="input-field flex-1 font-mono text-xs" />
            <input value={v.claimName} onChange={e => { const u = [...volumes]; u[i] = { ...v, claimName: e.target.value }; setVolumes(u) }} placeholder="pvc-name" className="input-field flex-1 font-mono text-xs" />
            <button type="button" onClick={() => setVolumes(prev => prev.filter((_, j) => j !== i))} className="text-text-tertiary hover:text-status-failed text-xs px-2">&times;</button>
          </div>
        ))}
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
                    <label className="text-xs text-text-tertiary">Pod Size</label>
                    <select
                      value={cfg.podSize}
                      onChange={e => setEnvConfigs(prev => ({ ...prev, [envName]: { ...prev[envName], podSize: e.target.value } }))}
                      className="input-field w-64 mt-1"
                    >
                      <option value="">Default</option>
                      {podSizes?.items?.map((s: any) => (
                        <option key={s.name} value={s.name}>{s.name} ({s.cpu}/{s.memory} → {s.cpuLimit}/{s.memoryLimit})</option>
                      ))}
                    </select>
                  </div>
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

      {/* Git Source */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <label className="label mb-0">Git Source</label>
          {!gitRepo && (
            <button type="button" onClick={() => setGitRepo('org/repo')} className="text-xs text-accent hover:text-accent-glow">+ Link Repository</button>
          )}
        </div>
        {gitRepo && (
          <div className="rounded-lg border border-border bg-surface-1 p-4 space-y-3">
            <div className="grid grid-cols-3 gap-3">
              <div>
                <label className="text-xs text-text-tertiary mb-1 block">Provider</label>
                <select value={gitProvider} onChange={e => setGitProvider(e.target.value)} className="input-field text-xs w-full">
                  <option value="github">GitHub</option>
                  <option value="gitlab">GitLab</option>
                  <option value="bitbucket">Bitbucket</option>
                </select>
              </div>
              <div className="col-span-2">
                <label className="text-xs text-text-tertiary mb-1 block">Repository</label>
                {repos.length > 0 ? (
                  <div className="space-y-1.5">
                    <select
                      value={gitRepo}
                      onChange={e => { setGitRepo(e.target.value); setGitBranch('') }}
                      className="input-field font-mono text-xs w-full"
                    >
                      <option value="">Select repository</option>
                      {repos.map(r => (
                        <option key={r.full_name} value={r.full_name}>
                          {r.full_name}{r.private ? ' 🔒' : ''}
                        </option>
                      ))}
                      {gitRepo && !repos.find(r => r.full_name === gitRepo) && (
                        <option value={gitRepo}>{gitRepo}</option>
                      )}
                    </select>
                    {ghStatus?.configured && ghStatus.appSlug && (
                      <a
                        href={ghStatus.ownerType === 'Organization'
                          ? `https://github.com/organizations/${ghStatus.ownerLogin}/settings/installations`
                          : `https://github.com/settings/installations`
                        }
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-1 text-[10px] text-accent hover:underline"
                      >
                        <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                          <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
                        </svg>
                        Add more repositories
                      </a>
                    )}
                  </div>
                ) : (
                  <input value={gitRepo} onChange={e => setGitRepo(e.target.value)} className="input-field font-mono text-xs w-full" placeholder="org/repo-name" />
                )}
              </div>
            </div>
            <div className={`grid ${ghStatus?.configured ? 'grid-cols-2' : 'grid-cols-3'} gap-3`}>
              <div>
                <label className="text-xs text-text-tertiary mb-1 block">Branch</label>
                {branches.length > 0 ? (
                  <select value={gitBranch} onChange={e => setGitBranch(e.target.value)} className="input-field font-mono text-xs w-full">
                    <option value="">Select branch</option>
                    {branches.map(b => (
                      <option key={b} value={b}>{b}</option>
                    ))}
                  </select>
                ) : (
                  <input value={gitBranch} onChange={e => setGitBranch(e.target.value)} className="input-field font-mono text-xs w-full" placeholder="main" />
                )}
              </div>
              {!ghStatus?.configured && (
                <div>
                  <label className="text-xs text-text-tertiary mb-1 block">Token Secret</label>
                  <input value={gitTokenSecret} onChange={e => setGitTokenSecret(e.target.value)} className="input-field font-mono text-xs w-full" placeholder="github-token" />
                  <p className="text-[10px] text-text-tertiary mt-0.5">K8s secret name with key &quot;token&quot;</p>
                </div>
              )}
              <div className="flex items-end pb-1">
                <label className="flex items-center gap-2 cursor-pointer">
                  <input type="checkbox" checked={gitAutoDeploy} onChange={e => setGitAutoDeploy(e.target.checked)} className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20" />
                  <span className="text-xs text-text-secondary">Auto-deploy on push</span>
                </label>
              </div>
            </div>
            <div className="flex items-center justify-between pt-1">
              <button type="button" onClick={() => { setGitRepo(''); setGitBranch(''); setGitAutoDeploy(false); setGitTokenSecret('') }} className="text-xs text-status-failed hover:text-red-300">Unlink Repository</button>
            </div>
          </div>
        )}
      </div>

      {/* Build Strategy */}
      <div className="space-y-3">
        <label className="label">Build Strategy</label>
        <div className="grid grid-cols-5 gap-2">
          {[
            { value: '', label: 'None', desc: 'Pre-built image' },
            { value: 'dockerfile', label: 'Dockerfile', desc: 'Kaniko build' },
            { value: 'nixpacks', label: 'Nixpacks', desc: 'Auto-detect' },
            { value: 'buildpacks', label: 'Buildpacks', desc: 'Cloud Native' },
            { value: 'image', label: 'Image Only', desc: 'No build' },
          ].map(opt => (
            <button
              key={opt.value}
              type="button"
              onClick={() => setBuildStrategy(opt.value)}
              className={`p-2.5 rounded-lg border text-center transition-all ${
                buildStrategy === opt.value
                  ? 'border-accent bg-accent/10 text-accent'
                  : 'border-border bg-surface-1 text-text-secondary hover:border-text-tertiary'
              }`}
            >
              <div className="text-xs font-mono font-medium">{opt.label}</div>
              <div className="text-[10px] text-text-tertiary mt-0.5">{opt.desc}</div>
            </button>
          ))}
        </div>
        {buildStrategy === 'dockerfile' && (
          <div>
            <label className="text-xs text-text-tertiary mb-1 block">Dockerfile Path</label>
            <input value={buildDockerfile} onChange={e => setBuildDockerfile(e.target.value)} className="input-field font-mono text-xs w-48" placeholder="Dockerfile" />
          </div>
        )}
      </div>

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

      <div>
        <div className="flex items-center justify-between mb-2">
          <label className="label">Cron Jobs</label>
          <button type="button" onClick={() => setCronjobs(prev => [...prev, { name: '', schedule: '', command: '', size: '', environments: [] }])} className="text-xs text-accent hover:text-accent-glow">+ Add</button>
        </div>
        {cronjobs.map((cj, i) => (
          <div key={i} className="rounded-lg border border-border bg-surface-1 p-3 mb-2">
            <div className="flex gap-2 mb-2">
              <div className="flex-1">
                <label className="text-xs text-text-tertiary">Name</label>
                <input value={cj.name} onChange={e => { const u = [...cronjobs]; u[i].name = e.target.value; setCronjobs(u) }} placeholder="cleanup" className="input-field font-mono text-xs mt-1" />
              </div>
              <div className="flex-1">
                <label className="text-xs text-text-tertiary">Schedule (cron)</label>
                <input value={cj.schedule} onChange={e => { const u = [...cronjobs]; u[i].schedule = e.target.value; setCronjobs(u) }} placeholder="0 2 * * *" className="input-field font-mono text-xs mt-1" />
              </div>
              <div className="w-64">
                <label className="text-xs text-text-tertiary">Size</label>
                <select value={cj.size} onChange={e => { const u = [...cronjobs]; u[i].size = e.target.value; setCronjobs(u) }} className="input-field text-xs mt-1">
                  <option value="">Default</option>
                  {podSizes?.items?.map((s: any) => (
                    <option key={s.name} value={s.name}>{s.name} ({s.cpu}/{s.memory} → {s.cpuLimit}/{s.memoryLimit})</option>
                  ))}
                </select>
              </div>
              <div className="flex items-end pb-1">
                <button type="button" onClick={() => setCronjobs(prev => prev.filter((_, j) => j !== i))} className="text-text-tertiary hover:text-status-failed text-xs px-2">&times;</button>
              </div>
            </div>
            <div className="mb-2">
              <label className="text-xs text-text-tertiary">Command</label>
              <input value={cj.command} onChange={e => { const u = [...cronjobs]; u[i].command = e.target.value; setCronjobs(u) }} placeholder="npm run cleanup" className="input-field font-mono text-xs mt-1" />
            </div>
            {appEnvironments.length > 0 && (
              <div className="mt-2 pt-2 border-t border-border-subtle">
                <div className="flex items-center justify-between mb-2">
                  <span className="text-[11px] text-text-tertiary font-mono uppercase tracking-wider">Per-Environment Overrides</span>
                  {appEnvironments.filter(env => !cj.environments.some(e => e.name === env)).length > 0 && (
                    <select
                      value=""
                      onChange={e => {
                        if (!e.target.value) return
                        const u = [...cronjobs]
                        u[i].environments = [...u[i].environments, { name: e.target.value, enabled: true, schedule: '' }]
                        setCronjobs(u)
                      }}
                      className="input-field text-xs w-auto"
                    >
                      <option value="">+ Add override</option>
                      {appEnvironments.filter(env => !cj.environments.some(e => e.name === env)).map(env => (
                        <option key={env} value={env}>{env}</option>
                      ))}
                    </select>
                  )}
                </div>
                {cj.environments.map((envOvr, ei) => (
                  <div key={envOvr.name} className="flex items-center gap-3 mb-1.5 pl-2">
                    <span className="text-xs font-mono text-accent w-24">{envOvr.name}</span>
                    <label className="flex items-center gap-1.5 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={envOvr.enabled}
                        onChange={e => {
                          const u = [...cronjobs]
                          u[i].environments[ei].enabled = e.target.checked
                          setCronjobs(u)
                        }}
                        className="w-3.5 h-3.5 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
                      />
                      <span className="text-[11px] text-text-tertiary">Enabled</span>
                    </label>
                    <div className="flex-1">
                      <input
                        value={envOvr.schedule}
                        onChange={e => {
                          const u = [...cronjobs]
                          u[i].environments[ei].schedule = e.target.value
                          setCronjobs(u)
                        }}
                        placeholder={`Override schedule (default: ${cj.schedule || '...'})`}
                        className="input-field font-mono text-xs w-full"
                        disabled={!envOvr.enabled}
                      />
                    </div>
                    <button
                      type="button"
                      onClick={() => {
                        const u = [...cronjobs]
                        u[i].environments = u[i].environments.filter((_, j) => j !== ei)
                        setCronjobs(u)
                      }}
                      className="text-text-tertiary hover:text-status-failed text-xs px-1"
                    >&times;</button>
                  </div>
                ))}
              </div>
            )}
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

function CloneAppModal({ appName, isPending, error, onClone, onClose }: {
  appName: string
  isPending: boolean
  error: Error | null
  onClone: (data: { name: string; project?: string }) => void
  onClose: () => void
}) {
  const [name, setName] = useState(`${appName}-clone`)

  return (
    <div className="card p-5 animate-slide-up">
      <h3 className="section-title mb-4">Clone App</h3>
      <p className="text-xs text-text-tertiary mb-4">
        Create a copy of <span className="font-mono text-text-secondary">{appName}</span> with the same configuration.
      </p>
      <form onSubmit={(e) => { e.preventDefault(); onClone({ name }) }}>
        <div className="mb-4">
          <label className="label">New App Name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="input-field"
            placeholder="my-app-clone"
            required
          />
        </div>
        <div className="flex gap-3">
          <button type="submit" disabled={isPending || !name} className="btn-primary">
            {isPending ? 'Cloning...' : 'Clone'}
          </button>
          <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
        </div>
        {error && <p className="text-status-failed text-xs mt-2">{error.message}</p>}
      </form>
    </div>
  )
}

const POD_LOG_COLORS = ['#56d4dd', '#e5c07b', '#98c379', '#c678dd', '#d19a66', '#61afef']

function AppLogs({ appId, environments }: { appId: string; environments: string[] }) {
  const [env, setEnv] = useState(environments[0] || '')
  const [tail, setTail] = useState(100)
  const [previous, setPrevious] = useState(false)
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [unified, setUnified] = useState(false)
  const [liveMode, setLiveMode] = useState(false)
  const [liveLines, setLiveLines] = useState<{ pod: string; line: string }[]>([])
  const [wsConnected, setWsConnected] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  const logEndRef = useRef<HTMLDivElement>(null)

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['appLogs', appId, env, tail, previous],
    queryFn: () => api.getAppLogs(appId, env, { tail, previous }),
    enabled: !!env && !liveMode,
    refetchInterval: autoRefresh ? 5000 : false,
  })

  // WebSocket streaming
  useEffect(() => {
    if (!liveMode || !env) {
      if (wsRef.current) {
        wsRef.current.close()
        wsRef.current = null
        setWsConnected(false)
      }
      return
    }

    const token = localStorage.getItem('vesta-token')
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${proto}//${window.location.host}/api/v1/apps/${appId}/logs/ws?environment=${encodeURIComponent(env)}&tail=${tail}&token=${encodeURIComponent(token || '')}`
    const ws = new WebSocket(wsUrl)
    wsRef.current = ws
    setLiveLines([])

    ws.onopen = () => setWsConnected(true)
    ws.onclose = () => setWsConnected(false)
    ws.onerror = () => setWsConnected(false)
    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data)
        if (msg.type === 'log') {
          setLiveLines(prev => {
            const next = [...prev, { pod: msg.pod, line: msg.line }]
            return next.length > 2000 ? next.slice(-1500) : next
          })
        }
      } catch { /* ignore */ }
    }

    return () => {
      ws.close()
      wsRef.current = null
      setWsConnected(false)
    }
  }, [liveMode, env, appId, tail])

  useEffect(() => {
    if ((data || liveLines.length > 0) && logEndRef.current) {
      logEndRef.current.scrollIntoView({ behavior: 'smooth' })
    }
  }, [data, liveLines])

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
                  disabled={liveMode}
                />
                <span className="text-[11px] text-text-tertiary">Auto-refresh</span>
              </label>
              <label className="flex items-center gap-1.5 cursor-pointer">
                <input
                  type="checkbox"
                  checked={liveMode}
                  onChange={(e) => { setLiveMode(e.target.checked); if (e.target.checked) setAutoRefresh(false) }}
                  className="w-3.5 h-3.5 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
                />
                <span className="text-[11px] text-text-tertiary flex items-center gap-1">
                  Live
                  {liveMode && wsConnected && <span className="inline-block w-1.5 h-1.5 rounded-full bg-status-running animate-glow-pulse" />}
                  {liveMode && !wsConnected && <span className="inline-block w-1.5 h-1.5 rounded-full bg-status-failed" />}
                </span>
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
        <label className="flex items-center gap-1.5 cursor-pointer">
          <input
            type="checkbox"
            checked={unified}
            onChange={(e) => setUnified(e.target.checked)}
            className="w-3.5 h-3.5 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
          />
          <span className="text-[11px] text-text-tertiary">Unified</span>
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
          {unified && data.pods?.length > 0 ? (
            <div className="border border-border rounded-lg overflow-hidden">
              <div className="bg-surface-1 px-4 py-2.5 flex items-center gap-4 border-b border-border flex-wrap">
                {data.pods.map((pod: any, i: number) => (
                  <div key={pod.pod} className="flex items-center gap-1.5">
                    <span className="inline-block w-2 h-2 rounded-full" style={{ backgroundColor: POD_LOG_COLORS[i % POD_LOG_COLORS.length] }} />
                    <span className="text-[11px] font-mono text-text-secondary">{pod.pod.split('-').slice(-2).join('-')}</span>
                    <span className="text-[10px] text-text-tertiary">({pod.status})</span>
                  </div>
                ))}
              </div>
              <pre className="bg-[#0d1117] text-[#c9d1d9] text-[11px] leading-relaxed p-4 overflow-x-auto max-h-[600px] overflow-y-auto font-mono whitespace-pre-wrap break-all">
                {data.pods.flatMap((pod: any, i: number) => {
                  const color = POD_LOG_COLORS[i % POD_LOG_COLORS.length]
                  const short = pod.pod.split('-').slice(-2).join('-')
                  const lines = (pod.logs || '').split('\n')
                  return lines.map((line: string, li: number) => (
                    <span key={`${pod.pod}-${li}`}>
                      <span style={{ color }}>[{short}]</span> {line}{'\n'}
                    </span>
                  ))
                })}
                <div ref={logEndRef} />
              </pre>
            </div>
          ) : (
            data.pods?.map((pod: any) => (
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
          ))
          )}
        </div>
      )}

      {env && liveMode && (
        <div className="border border-border rounded-lg overflow-hidden">
          <div className="bg-surface-1 px-4 py-2.5 flex items-center justify-between border-b border-border">
            <div className="flex items-center gap-2">
              <span className={`inline-block w-2 h-2 rounded-full ${wsConnected ? 'bg-status-running animate-glow-pulse' : 'bg-status-failed'}`} />
              <span className="text-xs font-mono text-text-primary">Live Stream</span>
            </div>
            <span className="text-[11px] text-text-tertiary">{liveLines.length} lines</span>
          </div>
          <pre className="bg-[#0d1117] text-[#c9d1d9] text-[11px] leading-relaxed p-4 overflow-x-auto max-h-[600px] overflow-y-auto font-mono whitespace-pre-wrap break-all">
            {liveLines.length === 0 && <span className="text-text-tertiary">Waiting for logs...</span>}
            {liveLines.map((entry, i) => {
              const pods = [...new Set(liveLines.map(l => l.pod))]
              const podIdx = pods.indexOf(entry.pod)
              const color = POD_LOG_COLORS[podIdx % POD_LOG_COLORS.length]
              const short = entry.pod.split('-').slice(-2).join('-')
              return (
                <span key={i}>
                  <span style={{ color }}>[{short}]</span> {entry.line}{'\n'}
                </span>
              )
            })}
            <div ref={logEndRef} />
          </pre>
        </div>
      )}
    </div>
  )
}

function AppTerminal({ appId, environments }: { appId: string; environments: string[] }) {
  const [env, setEnv] = useState(environments[0] || '')
  const [connected, setConnected] = useState(false)
  const termRef = useRef<HTMLDivElement>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const xtermRef = useRef<any>(null)

  const connect = () => {
    if (!env || !termRef.current) return

    // Dynamically import xterm
    Promise.all([
      import('@xterm/xterm'),
      import('@xterm/addon-fit'),
    ]).then(([{ Terminal }, { FitAddon }]) => {
      // Clean up previous
      if (xtermRef.current) {
        xtermRef.current.dispose()
      }
      if (wsRef.current) {
        wsRef.current.close()
      }

      const term = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
        theme: {
          background: '#0d1117',
          foreground: '#c9d1d9',
          cursor: '#58a6ff',
          selectionBackground: '#264f78',
        },
      })
      const fitAddon = new FitAddon()
      term.loadAddon(fitAddon)
      term.open(termRef.current!)
      fitAddon.fit()
      xtermRef.current = term

      const token = localStorage.getItem('vesta-token')
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const wsUrl = `${proto}//${window.location.host}/api/v1/apps/${appId}/exec?environment=${encodeURIComponent(env)}&token=${encodeURIComponent(token || '')}`
      const ws = new WebSocket(wsUrl)
      wsRef.current = ws

      ws.onopen = () => {
        setConnected(true)
        term.focus()
      }
      ws.onclose = () => {
        setConnected(false)
        term.write('\r\n\x1b[31m--- Connection closed ---\x1b[0m\r\n')
      }
      ws.onerror = () => {
        setConnected(false)
      }
      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data)
          if (msg.type === 'output') {
            term.write(msg.data)
          } else if (msg.type === 'error') {
            term.write(`\r\n\x1b[31m${msg.message}\x1b[0m\r\n`)
          }
        } catch {
          term.write(event.data)
        }
      }

      term.onData((data: string) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'input', data }))
        }
      })

      // Handle resize
      const resizeObserver = new ResizeObserver(() => fitAddon.fit())
      resizeObserver.observe(termRef.current!)

      return () => {
        resizeObserver.disconnect()
      }
    })
  }

  useEffect(() => {
    return () => {
      if (wsRef.current) wsRef.current.close()
      if (xtermRef.current) xtermRef.current.dispose()
    }
  }, [])

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h3 className="section-title">Web Terminal</h3>
        <div className="flex items-center gap-2">
          {connected && <span className="inline-block w-2 h-2 rounded-full bg-status-running animate-glow-pulse" />}
          <span className="text-[11px] text-text-tertiary">{connected ? 'Connected' : 'Disconnected'}</span>
        </div>
      </div>

      <div className="flex items-center gap-3 mb-4">
        <select value={env} onChange={(e) => setEnv(e.target.value)} className="input-field text-xs">
          <option value="">Select environment...</option>
          {environments.map((e) => (
            <option key={e} value={e}>{e}</option>
          ))}
        </select>
        <button
          onClick={connect}
          disabled={!env}
          className="btn-primary text-xs"
        >
          {connected ? 'Reconnect' : 'Connect'}
        </button>
        {connected && (
          <button
            onClick={() => {
              if (wsRef.current) wsRef.current.close()
              setConnected(false)
            }}
            className="btn-ghost text-xs text-status-failed"
          >
            Disconnect
          </button>
        )}
      </div>

      <div
        ref={termRef}
        className="border border-border rounded-lg overflow-hidden bg-[#0d1117]"
        style={{ minHeight: '400px' }}
      />
    </div>
  )
}

function AppBuilds({ appId, environments, gitRepo }: { appId: string; environments: string[]; gitRepo: string }) {
  const queryClient = useQueryClient()
  const [buildEnv, setBuildEnv] = useState(environments[0] || '')
  const [selectedBuild, setSelectedBuild] = useState<string | null>(null)
  const [showTrigger, setShowTrigger] = useState(false)
  const [commitSha, setCommitSha] = useState('')
  const [branch, setBranch] = useState('')
  const role = useUserRole()

  const { data: branchesData } = useQuery({
    queryKey: ['repoBranches', gitRepo],
    queryFn: () => api.listRepoBranches(gitRepo),
    enabled: !!gitRepo && gitRepo.includes('/'),
    staleTime: 60_000,
  })
  const buildBranches: string[] = branchesData?.branches || []

  const { data: builds, isLoading } = useQuery({
    queryKey: ['builds', appId],
    queryFn: () => api.listBuilds(appId, { limit: 30 }),
    enabled: !!appId,
    refetchInterval: 5000,
  })

  const triggerMutation = useMutation({
    mutationFn: () => api.triggerBuild(appId, {
      environment: buildEnv,
      commitSha: commitSha || undefined,
      branch: branch || undefined,
    }),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['builds', appId] })
      setShowTrigger(false)
      setCommitSha('')
      setBranch('')
      if (res?.id) setSelectedBuild(res.id)
    },
  })

  const cancelMutation = useMutation({
    mutationFn: (buildId: string) => api.cancelBuild(appId, buildId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['builds', appId] }),
  })

  const statusIcon = (status: string) => {
    switch (status) {
      case 'success': return '✅'
      case 'failed': return '❌'
      case 'running': return '🔨'
      case 'cancelled': return '🚫'
      default: return '⏳'
    }
  }

  const statusColor = (status: string) => {
    switch (status) {
      case 'success': return 'text-green-400'
      case 'failed': return 'text-red-400'
      case 'running': return 'text-yellow-400'
      case 'cancelled': return 'text-text-tertiary'
      default: return 'text-text-secondary'
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-mono tracking-wider uppercase text-text-secondary">Builds</h3>
        {role !== 'viewer' && (
          <button
            onClick={() => setShowTrigger(!showTrigger)}
            className="px-3 py-1.5 text-xs font-mono bg-accent text-white rounded hover:bg-accent/80 transition-colors"
          >
            Trigger Build
          </button>
        )}
      </div>

      {showTrigger && (
        <div className="card p-4 space-y-3">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
            <div>
              <label className="block text-xs text-text-tertiary mb-1">Environment</label>
              <select
                value={buildEnv}
                onChange={(e) => setBuildEnv(e.target.value)}
                className="w-full bg-surface-secondary border border-border rounded px-3 py-1.5 text-sm"
              >
                {environments.map((env) => (
                  <option key={env} value={env}>{env}</option>
                ))}
              </select>
            </div>
            <div>
              <label className="block text-xs text-text-tertiary mb-1">Branch (optional)</label>
              {buildBranches.length > 0 ? (
                <select
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  className="w-full bg-surface-secondary border border-border rounded px-3 py-1.5 text-sm"
                >
                  <option value="">Default branch</option>
                  {buildBranches.map(b => (
                    <option key={b} value={b}>{b}</option>
                  ))}
                </select>
              ) : (
                <input
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  placeholder="main"
                  className="w-full bg-surface-secondary border border-border rounded px-3 py-1.5 text-sm"
                />
              )}
            </div>
            <div>
              <label className="block text-xs text-text-tertiary mb-1">Commit SHA (optional)</label>
              <input
                value={commitSha}
                onChange={(e) => setCommitSha(e.target.value)}
                placeholder="a1b2c3d4..."
                className="w-full bg-surface-secondary border border-border rounded px-3 py-1.5 text-sm font-mono"
              />
            </div>
          </div>
          <div className="flex gap-2">
            <button
              onClick={() => triggerMutation.mutate()}
              disabled={triggerMutation.isPending || !buildEnv}
              className="px-4 py-1.5 text-xs font-mono bg-accent text-white rounded hover:bg-accent/80 transition-colors disabled:opacity-50"
            >
              {triggerMutation.isPending ? 'Starting...' : 'Start Build'}
            </button>
            <button
              onClick={() => setShowTrigger(false)}
              className="px-4 py-1.5 text-xs font-mono bg-surface-secondary text-text-secondary rounded hover:bg-surface-tertiary transition-colors"
            >
              Cancel
            </button>
          </div>
          {triggerMutation.error && (
            <p className="text-xs text-red-400">{(triggerMutation.error as Error).message}</p>
          )}
        </div>
      )}

      {selectedBuild && (
        <BuildLogViewer appId={appId} buildId={selectedBuild} onClose={() => setSelectedBuild(null)} />
      )}

      {isLoading ? (
        <div className="card p-5 text-center text-text-tertiary text-sm">Loading builds...</div>
      ) : !builds?.items?.length ? (
        <div className="card p-5 text-center text-text-tertiary text-sm">
          No builds yet. Trigger a build or push to a linked repository.
        </div>
      ) : (
        <div className="space-y-1">
          {builds.items.map((b: any) => (
            <div
              key={b.id}
              onClick={() => setSelectedBuild(selectedBuild === b.id ? null : b.id)}
              className={`card p-3 cursor-pointer hover:bg-surface-secondary/50 transition-colors ${
                selectedBuild === b.id ? 'ring-1 ring-accent' : ''
              }`}
            >
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-3">
                  <span title={b.status}>{statusIcon(b.status)}</span>
                  <div>
                    <div className="text-sm font-mono">
                      <span className={statusColor(b.status)}>{b.status}</span>
                      <span className="text-text-tertiary mx-2">·</span>
                      <span className="text-text-secondary">{b.strategy}</span>
                      {b.commitSha && (
                        <>
                          <span className="text-text-tertiary mx-2">·</span>
                          <span className="text-text-tertiary">{b.commitSha.slice(0, 8)}</span>
                        </>
                      )}
                    </div>
                    <div className="text-xs text-text-tertiary mt-0.5">
                      {b.branch && <span>{b.branch}</span>}
                      <span className="mx-1">→</span>
                      <span>{b.environment}</span>
                      <span className="mx-2">·</span>
                      <span>{b.triggeredBy}</span>
                      <span className="mx-2">·</span>
                      <span>{new Date(b.createdAt).toLocaleString()}</span>
                      {b.durationMs > 0 && (
                        <span className="ml-2">({Math.round(b.durationMs / 1000)}s)</span>
                      )}
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  {(b.status === 'pending' || b.status === 'running') && role !== 'viewer' && (
                    <button
                      onClick={(e) => { e.stopPropagation(); cancelMutation.mutate(b.id) }}
                      className="px-2 py-1 text-xs text-red-400 hover:text-red-300 font-mono"
                    >
                      Cancel
                    </button>
                  )}
                  <span className="text-xs text-text-tertiary">
                    {b.image?.split(':').pop()}
                  </span>
                </div>
              </div>
              {b.error && (
                <div className="mt-2 text-xs text-red-400 font-mono bg-red-400/10 rounded px-2 py-1">
                  {b.error}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function BuildLogViewer({ appId, buildId, onClose }: { appId: string; buildId: string; onClose: () => void }) {
  const logRef = useRef<HTMLPreElement>(null)
  const [logs, setLogs] = useState<string>('')
  const [streaming, setStreaming] = useState(false)

  useEffect(() => {
    let cancelled = false
    const abortController = new AbortController()

    const fetchLogs = async () => {
      try {
        // Try streaming first
        const token = localStorage.getItem('vesta-token')
        const res = await fetch(`/api/v1/apps/${appId}/builds/${buildId}/logs?follow=true`, {
          headers: token ? { Authorization: `Bearer ${token}` } : {},
          signal: abortController.signal,
        })

        if (!res.ok) {
          // Fall back to non-streaming
          const data = await api.getBuildLogs(appId, buildId)
          if (!cancelled) {
            setLogs(data.logs || '(no logs available)')
          }
          return
        }

        setStreaming(true)
        const reader = res.body?.getReader()
        const decoder = new TextDecoder()
        let buffer = ''

        while (reader) {
          const { done, value } = await reader.read()
          if (done || cancelled) break

          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() || ''

          for (const line of lines) {
            if (line.startsWith('data: ')) {
              const data = line.slice(6)
              setLogs((prev) => prev + data + '\n')
            } else if (line.startsWith('event: done')) {
              setStreaming(false)
            }
          }

          if (logRef.current) {
            logRef.current.scrollTop = logRef.current.scrollHeight
          }
        }
      } catch (err: any) {
        if (err.name === 'AbortError') return
        // Fallback
        try {
          const data = await api.getBuildLogs(appId, buildId)
          if (!cancelled) {
            setLogs(data.logs || '(no logs available)')
          }
        } catch { /* ignore */ }
      }
    }

    fetchLogs()
    return () => {
      cancelled = true
      abortController.abort()
    }
  }, [appId, buildId])

  return (
    <div className="card overflow-hidden">
      <div className="flex items-center justify-between px-4 py-2 bg-surface-secondary border-b border-border">
        <div className="flex items-center gap-3">
          <span className="text-xs font-mono text-text-secondary">Build Logs</span>
          <span className="text-xs font-mono text-text-tertiary">{buildId.slice(0, 8)}...</span>
          {streaming && (
            <span className="flex items-center gap-1 text-xs text-yellow-400">
              <span className="w-1.5 h-1.5 bg-yellow-400 rounded-full animate-pulse" />
              streaming
            </span>
          )}
        </div>
        <button
          onClick={onClose}
          className="text-text-tertiary hover:text-text-primary text-sm"
        >
          ✕
        </button>
      </div>
      <pre
        ref={logRef}
        className="p-4 text-xs font-mono text-text-secondary bg-black/30 overflow-auto max-h-96 whitespace-pre-wrap"
      >
        {logs || 'Waiting for logs...'}
      </pre>
    </div>
  )
}

function AppMetrics({ appId, environments }: { appId: string; environments: string[] }) {
  const [env, setEnv] = useState(environments[0] || '')
  const [expandedPod, setExpandedPod] = useState<string | null>(null)
  const [promRange, setPromRange] = useState<string>('1h')

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['appMetrics', appId, env],
    queryFn: () => api.getAppMetrics(appId, env),
    enabled: !!env,
    refetchInterval: 15000,
  })

  const { data: promStatus } = useQuery({
    queryKey: ['prometheusStatus'],
    queryFn: () => api.getPrometheusStatus(),
    staleTime: 60000,
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

          {/* Prometheus Time-Series Charts */}
          {promStatus?.available && env && (
            <div className="bg-surface-1 border border-border rounded-lg p-4">
              <div className="flex items-center justify-between mb-3">
                <h4 className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider">Historical Metrics</h4>
                <div className="flex gap-1">
                  {(['1h', '6h', '24h', '7d'] as const).map((r) => (
                    <button
                      key={r}
                      onClick={() => setPromRange(r)}
                      className={`px-2 py-0.5 text-[10px] font-mono rounded ${promRange === r ? 'bg-accent text-white' : 'bg-surface-2 text-text-tertiary hover:text-text-secondary'}`}
                    >
                      {r}
                    </button>
                  ))}
                </div>
              </div>
              <div className="grid grid-cols-2 gap-4">
                <PrometheusChart appId={appId} env={env} metric="cpu" range={promRange} label="CPU Usage" unit="cores" color="#6366f1" />
                <PrometheusChart appId={appId} env={env} metric="memory" range={promRange} label="Memory Usage" unit="bytes" color="#06b6d4" />
                <PrometheusChart appId={appId} env={env} metric="network_rx" range={promRange} label="Network Receive" unit="bytes/s" color="#10b981" />
                <PrometheusChart appId={appId} env={env} metric="network_tx" range={promRange} label="Network Transmit" unit="bytes/s" color="#f59e0b" />
              </div>
              {promStatus?.httpAvailable ? (
                <div className="grid grid-cols-3 gap-4 mt-4">
                  <PrometheusChart appId={appId} env={env} metric="http_rate" range={promRange} label="Request Rate" unit="req/s" color="#8b5cf6" />
                  <PrometheusChart appId={appId} env={env} metric="http_errors" range={promRange} label="Error Rate" unit="%" color="#ef4444" />
                  <PrometheusChart appId={appId} env={env} metric="http_latency_p95" range={promRange} label="Latency (p95)" unit="s" color="#f97316" />
                </div>
              ) : (
                <div className="mt-4 px-3 py-2 bg-surface-2/50 rounded-lg">
                  <p className="text-[10px] font-mono text-text-tertiary">
                    HTTP metrics (request rate, errors, latency) require an ingress controller with Prometheus metrics enabled.
                    <br />Traefik: <span className="text-text-secondary">--set metrics.prometheus.enabled=true --set metrics.prometheus.serviceMonitor.enabled=true</span>
                    <br />nginx: <span className="text-text-secondary">--set controller.metrics.enabled=true --set controller.metrics.serviceMonitor.enabled=true</span>
                  </p>
                </div>
              )}
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

function formatMetricValue(value: number, unit: string): string {
  if (isNaN(value)) return '—'
  switch (unit) {
    case 'cores':
      return value < 0.01 ? `${(value * 1000).toFixed(0)}m` : value.toFixed(3)
    case 'bytes':
      if (value >= 1024 * 1024 * 1024) return `${(value / (1024 * 1024 * 1024)).toFixed(1)} Gi`
      if (value >= 1024 * 1024) return `${(value / (1024 * 1024)).toFixed(0)} Mi`
      if (value >= 1024) return `${(value / 1024).toFixed(0)} Ki`
      return `${value.toFixed(0)} B`
    case 'bytes/s':
      if (value >= 1024 * 1024) return `${(value / (1024 * 1024)).toFixed(1)} MB/s`
      if (value >= 1024) return `${(value / 1024).toFixed(1)} KB/s`
      return `${value.toFixed(0)} B/s`
    case 'req/s':
      return value < 1 ? value.toFixed(2) : value.toFixed(0)
    case '%':
      return `${value.toFixed(1)}%`
    case 's':
      if (value < 0.001) return `${(value * 1000000).toFixed(0)}µs`
      if (value < 1) return `${(value * 1000).toFixed(0)}ms`
      return `${value.toFixed(2)}s`
    default:
      return value.toFixed(2)
  }
}

function PrometheusChart({ appId, env, metric, range: timeRange, label, unit, color }: {
  appId: string; env: string; metric: string; range: string; label: string; unit: string; color: string
}) {
  const { data, isLoading } = useQuery({
    queryKey: ['prometheusMetrics', appId, env, metric, timeRange],
    queryFn: () => api.getPrometheusMetrics(appId, env, metric, timeRange),
    enabled: !!env,
    refetchInterval: 30000,
    staleTime: 15000,
  })

  const chartData = useMemo(() => {
    if (!data?.series?.length) return []
    // Aggregate all series by timestamp (sum across pods)
    const byTime: Record<number, number> = {}
    for (const series of data.series) {
      for (const pt of series.values) {
        const ts = Math.floor(pt.timestamp)
        byTime[ts] = (byTime[ts] || 0) + parseFloat(pt.value || '0')
      }
    }
    return Object.entries(byTime)
      .map(([ts, val]) => ({ time: parseInt(ts), value: val }))
      .sort((a, b) => a.time - b.time)
  }, [data])

  const isEmpty = !isLoading && chartData.length === 0
  const noData = data && data.available === false

  return (
    <div className="bg-surface-2/50 rounded-lg p-3">
      <p className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-2">{label}</p>
      {isLoading && (
        <div className="h-[120px] flex items-center justify-center">
          <svg className="w-4 h-4 animate-spin text-text-tertiary" fill="none" viewBox="0 0 24 24">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
        </div>
      )}
      {noData && (
        <div className="h-[120px] flex items-center justify-center">
          <p className="text-[10px] text-text-tertiary">Not available</p>
        </div>
      )}
      {isEmpty && !noData && !isLoading && (
        <div className="h-[120px] flex items-center justify-center">
          <p className="text-[10px] text-text-tertiary">No data</p>
        </div>
      )}
      {!isLoading && chartData.length > 0 && (
        <ResponsiveContainer width="100%" height={120}>
          <AreaChart data={chartData}>
            <defs>
              <linearGradient id={`grad-${metric}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor={color} stopOpacity={0.3} />
                <stop offset="95%" stopColor={color} stopOpacity={0} />
              </linearGradient>
            </defs>
            <XAxis
              dataKey="time"
              type="number"
              domain={['dataMin', 'dataMax']}
              tickFormatter={(ts: number) => {
                const d = new Date(ts * 1000)
                return timeRange === '7d' ? `${d.getMonth()+1}/${d.getDate()}` : `${d.getHours()}:${String(d.getMinutes()).padStart(2, '0')}`
              }}
              tick={{ fontSize: 9, fill: '#6b7280' }}
              axisLine={false}
              tickLine={false}
              minTickGap={40}
            />
            <YAxis
              tickFormatter={(v: number) => formatMetricValue(v, unit)}
              tick={{ fontSize: 9, fill: '#6b7280' }}
              axisLine={false}
              tickLine={false}
              width={50}
            />
            <Tooltip
              contentStyle={{ backgroundColor: '#1a1a2e', border: '1px solid #2a2a4a', borderRadius: '6px', fontSize: '11px' }}
              labelFormatter={(ts: number) => new Date(ts * 1000).toLocaleString()}
              formatter={(value: number) => [formatMetricValue(value, unit), label]}
            />
            <Area
              type="monotone"
              dataKey="value"
              stroke={color}
              strokeWidth={1.5}
              fill={`url(#grad-${metric})`}
              dot={false}
              isAnimationActive={false}
            />
          </AreaChart>
        </ResponsiveContainer>
      )}
    </div>
  )
}

function SharedSecretBindings({ appId, projectId, environments }: { appId: string; projectId: string; environments: string[] }) {
  const queryClient = useQueryClient()
  const [bindEnv, setBindEnv] = useState<Record<string, string>>({})

  const { data: bound } = useQuery({
    queryKey: ['appSharedSecrets', appId],
    queryFn: () => api.listAppSharedSecrets(appId),
  })

  const { data: available } = useQuery({
    queryKey: ['sharedSecrets', projectId],
    queryFn: () => api.listSharedSecrets(projectId),
    enabled: !!projectId,
  })

  const bindMutation = useMutation({
    mutationFn: ({ name, environment }: { name: string; environment: string }) => api.bindSharedSecret(appId, name, environment),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['appSharedSecrets', appId] })
      queryClient.invalidateQueries({ queryKey: ['app', appId] })
    },
  })

  const unbindMutation = useMutation({
    mutationFn: ({ name, environment }: { name: string; environment: string }) => api.unbindSharedSecret(appId, name, environment),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['appSharedSecrets', appId] })
      queryClient.invalidateQueries({ queryKey: ['app', appId] })
    },
  })

  const boundItems: { name: string; environments: string[] }[] = bound?.items || []
  const boundNames = boundItems.map((b) => b.name)
  const unbound = (available?.items || []).filter((s: any) => !boundNames.includes(s.name))
  // Secrets that are bound but not to all environments yet
  const partiallyBound = (available?.items || []).filter((s: any) => {
    const b = boundItems.find((bi) => bi.name === s.name)
    return b && b.environments.length < environments.length
  })

  return (
    <section className="card p-5">
      <h3 className="section-title mb-4">Shared Secrets</h3>
      <p className="text-xs text-text-tertiary mb-4">
        Bind project-level shared secrets to specific environments of this app.
      </p>

      {boundItems.length > 0 && (
        <div className="space-y-2 mb-4">
          <label className="text-[11px] text-text-tertiary font-mono uppercase tracking-wider">Bound</label>
          {boundItems.map((binding) => {
            const secret = available?.items?.find((s: any) => s.name === binding.name)
            return (
              <div key={binding.name} className="bg-surface-2 rounded-lg px-4 py-3">
                <div className="flex items-center gap-3 mb-2">
                  <div className="w-7 h-7 rounded bg-status-healthy/10 flex items-center justify-center shrink-0">
                    <svg className="w-3.5 h-3.5 text-status-healthy" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
                    </svg>
                  </div>
                  <div className="flex-1 min-w-0">
                    <span className="text-sm font-mono text-text-primary">{binding.name}</span>
                    {secret?.keys && (
                      <div className="flex gap-1.5 mt-0.5">
                        {secret.keys.map((k: string) => (
                          <span key={k} className="px-1.5 py-0.5 bg-surface-3 rounded text-[10px] font-mono text-text-tertiary">{k}</span>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
                <div className="flex flex-wrap gap-1.5 ml-10">
                  {binding.environments.map((env) => (
                    <span key={env} className="inline-flex items-center gap-1 px-2 py-0.5 bg-accent/10 text-accent rounded text-[11px] font-mono">
                      {env}
                      <button
                        onClick={() => unbindMutation.mutate({ name: binding.name, environment: env })}
                        disabled={unbindMutation.isPending}
                        className="hover:text-status-failed transition-colors ml-0.5"
                        title={`Unbind from ${env}`}
                      >
                        ×
                      </button>
                    </span>
                  ))}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {(unbound.length > 0 || partiallyBound.length > 0) && (
        <div className="space-y-2">
          <label className="text-[11px] text-text-tertiary font-mono uppercase tracking-wider">Available</label>
          {[...unbound, ...partiallyBound]
            .filter((s: any, i: number, arr: any[]) => arr.findIndex((x: any) => x.name === s.name) === i)
            .map((s: any) => {
              const existingBinding = boundItems.find((b) => b.name === s.name)
              const availableEnvs = environments.filter((e) => !existingBinding?.environments.includes(e))
              const selectedEnv = bindEnv[s.name] || availableEnvs[0] || ''
              return (
                <div key={s.name} className="flex items-center justify-between bg-surface-1 border border-border-subtle rounded-lg px-4 py-3">
                  <div className="flex items-center gap-3">
                    <div className="w-7 h-7 rounded bg-surface-3 flex items-center justify-center shrink-0">
                      <svg className="w-3.5 h-3.5 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
                      </svg>
                    </div>
                    <div>
                      <span className="text-sm font-mono text-text-primary">{s.name}</span>
                      {s.keys && (
                        <div className="flex gap-1.5 mt-0.5">
                          {s.keys.map((k: string) => (
                            <span key={k} className="px-1.5 py-0.5 bg-surface-3 rounded text-[10px] font-mono text-text-tertiary">{k}</span>
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <select
                      value={selectedEnv}
                      onChange={(e) => setBindEnv({ ...bindEnv, [s.name]: e.target.value })}
                      className="text-xs bg-surface-2 border border-border-subtle rounded px-2 py-1 text-text-primary"
                    >
                      {availableEnvs.map((env) => (
                        <option key={env} value={env}>{env}</option>
                      ))}
                    </select>
                    <button
                      onClick={() => selectedEnv && bindMutation.mutate({ name: s.name, environment: selectedEnv })}
                      disabled={bindMutation.isPending || !selectedEnv}
                      className="text-xs text-accent hover:text-accent-glow transition-colors"
                    >
                      Bind
                    </button>
                  </div>
                </div>
              )
            })}
        </div>
      )}

      {boundItems.length === 0 && unbound.length === 0 && (
        <p className="text-xs text-text-tertiary">No shared secrets in this project. Create them in Secrets → Shared Secrets.</p>
      )}

      {(bindMutation.isError || unbindMutation.isError) && (
        <p className="text-status-failed text-xs mt-2">{((bindMutation.error || unbindMutation.error) as Error)?.message}</p>
      )}
    </section>
  )
}

function EnvSecrets({ appId, env }: { appId: string; env: string }) {
  const queryClient = useQueryClient()
  const { data, isLoading } = useQuery({
    queryKey: ['appEnvSecrets', appId, env],
    queryFn: () => api.listAppEnvSecrets(appId, env),
  })

  const role = useUserRole()
  const isAdmin = role === 'admin'
  const [showAdd, setShowAdd] = useState(false)
  const [newKeys, setNewKeys] = useState([{ key: '', value: '' }])
  const [editMode, setEditMode] = useState(false)
  const [editKeys, setEditKeys] = useState<{ key: string; value: string }[]>([])
  const [envInput, setEnvInput] = useState('')
  const [showEnvImport, setShowEnvImport] = useState(false)
  const [revealed, setRevealed] = useState<Record<string, string> | null>(null)
  const [revealLoading, setRevealLoading] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const addMutation = useMutation({
    mutationFn: (secretData: Record<string, string>) => {
      return api.createAppEnvSecret(appId, env, { data: secretData })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['appEnvSecrets', appId, env] })
      setShowAdd(false)
      setNewKeys([{ key: '', value: '' }])
      setEditMode(false)
      setEditKeys([])
      setShowEnvImport(false)
      setEnvInput('')
      setRevealed(null)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (key: string) => api.deleteAppEnvSecretKey(appId, env, key),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['appEnvSecrets', appId, env] }),
  })

  const parseEnvContent = (content: string): Record<string, string> => {
    const result: Record<string, string> = {}
    for (const line of content.split('\n')) {
      const trimmed = line.trim()
      if (!trimmed || trimmed.startsWith('#')) continue
      const eqIdx = trimmed.indexOf('=')
      if (eqIdx === -1) continue
      const key = trimmed.slice(0, eqIdx).trim()
      let value = trimmed.slice(eqIdx + 1).trim()
      // Strip surrounding quotes
      if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
        value = value.slice(1, -1)
      }
      if (key) result[key] = value
    }
    return result
  }

  const handleEnvImport = () => {
    const parsed = parseEnvContent(envInput)
    if (Object.keys(parsed).length === 0) return
    addMutation.mutate(parsed)
  }

  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = (ev) => {
      const content = ev.target?.result as string
      setEnvInput(content)
    }
    reader.readAsText(file)
    e.target.value = ''
  }

  const handleEditSubmit = () => {
    const secretData: Record<string, string> = {}
    editKeys.forEach((kv) => { if (kv.key && kv.value) secretData[kv.key] = kv.value })
    if (Object.keys(secretData).length === 0) return
    addMutation.mutate(secretData)
  }

  useEffect(() => {
    if (!revealed) return
    const timer = setTimeout(() => setRevealed(null), 30000)
    return () => clearTimeout(timer)
  }, [revealed])

  const handleReveal = async () => {
    setRevealLoading(true)
    try {
      const res = await api.revealAppEnvSecretValues(appId, env)
      setRevealed(res.values)
      setEditMode(false)
      setShowAdd(false)
      setShowEnvImport(false)
    } catch {
      setRevealed(null)
    } finally {
      setRevealLoading(false)
    }
  }

  if (isLoading) return <p className="text-xs text-text-tertiary">Loading secrets...</p>

  const existingKeys: string[] = data?.items?.[0]?.keys || []

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-xs text-text-tertiary">{existingKeys.length} key{existingKeys.length !== 1 ? 's' : ''} in {env}</p>
        <div className="flex items-center gap-3">
          {existingKeys.length > 0 && (
            <button
              onClick={() => {
                setEditMode(!editMode)
                setEditKeys(existingKeys.map(k => ({ key: k, value: '' })))
                setShowAdd(false)
                setShowEnvImport(false)
                setRevealed(null)
              }}
              className="text-xs text-accent hover:text-accent-glow transition-colors font-mono"
            >
              {editMode ? 'Cancel Edit' : 'Edit Values'}
            </button>
          )}
          <button
            onClick={() => {
              setShowEnvImport(!showEnvImport)
              setShowAdd(false)
              setEditMode(false)
              setRevealed(null)
            }}
            className="text-xs text-accent hover:text-accent-glow transition-colors font-mono"
          >
            {showEnvImport ? 'Cancel' : 'Import .env'}
          </button>
          <button
            onClick={() => {
              setShowAdd(!showAdd)
              setEditMode(false)
              setShowEnvImport(false)
              setRevealed(null)
            }}
            className="text-xs text-accent hover:text-accent-glow transition-colors font-mono"
          >
            + Add Keys
          </button>
          {isAdmin && existingKeys.length > 0 && !revealed && (
            <button
              onClick={handleReveal}
              disabled={revealLoading}
              className="text-xs text-accent hover:text-accent-glow transition-colors font-mono disabled:opacity-60"
            >
              {revealLoading ? 'Loading...' : 'Reveal'}
            </button>
          )}
          {revealed && (
            <button
              onClick={() => setRevealed(null)}
              className="text-xs text-accent hover:text-accent-glow transition-colors font-mono"
            >
              Hide
            </button>
          )}
        </div>
      </div>

      {revealed && (
        <div className="bg-surface-1 border border-border rounded-lg p-3">
          <div className="flex items-center justify-between mb-2">
            <span className="text-[11px] font-mono text-yellow-500">Values visible (auto-hides in 30s)</span>
          </div>
          <div className="space-y-1">
            {Object.entries(revealed).map(([k, v]) => (
              <div key={k} className="flex items-center gap-2 font-mono text-xs">
                <span className="text-text-tertiary min-w-[140px]">{k}</span>
                <span className="text-text-primary bg-surface-3 px-2 py-0.5 rounded select-all break-all">{v}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {existingKeys.length > 0 && !editMode && (
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

      {editMode && (
        <div className="bg-surface-1 border border-border rounded-lg p-4 space-y-3 animate-slide-up">
          <div>
            <label className="label">Update Values</label>
            <p className="text-[11px] text-text-tertiary mb-2">Enter new values for existing keys. Only keys with values will be updated.</p>
            <div className="space-y-2">
              {editKeys.map((kv, i) => (
                <div key={kv.key} className="flex gap-2 items-center">
                  <span className="font-mono text-xs text-text-secondary w-40 truncate flex-shrink-0">{kv.key}</span>
                  <RevealableInput
                    value={kv.value}
                    onChange={(e) => { const u = [...editKeys]; u[i].value = e.target.value; setEditKeys(u) }}
                    placeholder="new value"
                    type="password"
                    className="input-field flex-1"
                  />
                </div>
              ))}
            </div>
          </div>
          <div className="flex gap-3">
            <button
              type="button"
              onClick={handleEditSubmit}
              disabled={addMutation.isPending || !editKeys.some(kv => kv.value)}
              className="btn-primary text-xs"
            >
              {addMutation.isPending ? 'Saving...' : 'Update'}
            </button>
            <button type="button" onClick={() => setEditMode(false)} className="btn-ghost text-xs">Cancel</button>
          </div>
          {addMutation.isError && (
            <p className="text-status-failed text-xs">{(addMutation.error as Error).message}</p>
          )}
        </div>
      )}

      {showEnvImport && (
        <div className="bg-surface-1 border border-border rounded-lg p-4 space-y-3 animate-slide-up">
          <div>
            <div className="flex items-center justify-between mb-2">
              <label className="label mb-0">Import .env File</label>
              <button
                type="button"
                onClick={() => fileInputRef.current?.click()}
                className="text-xs text-accent hover:text-accent-glow transition-colors font-mono"
              >
                Upload file
              </button>
              <input ref={fileInputRef} type="file" accept=".env,.env.*,text/plain" onChange={handleFileUpload} className="hidden" />
            </div>
            <p className="text-[11px] text-text-tertiary mb-2">Paste .env content below or upload a file. Format: KEY=value (one per line, # comments ignored).</p>
            <textarea
              value={envInput}
              onChange={(e) => setEnvInput(e.target.value)}
              placeholder={"DATABASE_URL=postgres://...\nAPI_KEY=sk-...\n# Comments are ignored"}
              rows={6}
              className="input-field font-mono text-xs w-full"
              spellCheck={false}
            />
            {envInput && (
              <p className="text-[11px] text-text-tertiary mt-1">
                {Object.keys(parseEnvContent(envInput)).length} key{Object.keys(parseEnvContent(envInput)).length !== 1 ? 's' : ''} detected
              </p>
            )}
          </div>
          <div className="flex gap-3">
            <button
              type="button"
              onClick={handleEnvImport}
              disabled={addMutation.isPending || Object.keys(parseEnvContent(envInput)).length === 0}
              className="btn-primary text-xs"
            >
              {addMutation.isPending ? 'Importing...' : 'Import'}
            </button>
            <button type="button" onClick={() => { setShowEnvImport(false); setEnvInput('') }} className="btn-ghost text-xs">Cancel</button>
          </div>
          {addMutation.isError && (
            <p className="text-status-failed text-xs">{(addMutation.error as Error).message}</p>
          )}
        </div>
      )}

      {showAdd && (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            const secretData: Record<string, string> = {}
            newKeys.forEach((kv) => { if (kv.key) secretData[kv.key] = kv.value })
            addMutation.mutate(secretData)
          }}
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
                  <RevealableInput
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
    <div className="flex items-center justify-center py-20">
      <div className="relative">
        <div className="w-8 h-8 rounded-lg bg-accent/10 border border-accent/20 flex items-center justify-center animate-glow-pulse">
          <div className="w-2.5 h-2.5 rounded bg-accent" />
        </div>
      </div>
    </div>
  )
}
