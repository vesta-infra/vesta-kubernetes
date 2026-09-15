import { useState, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import { ImageRepositoryInput, ImageTagInput } from '../components/RegistryPicker'
import { BranchPicker } from '../components/BranchPicker'
import { RepositoryPicker } from '../components/RepositoryPicker'
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

type SourceMode = 'image' | 'build' | 'push' | 'template'

const SOURCE_MODES: { value: SourceMode; label: string; blurb: string }[] = [
  { value: 'image', label: 'Deploy an existing image',
    blurb: 'Already published to a registry.' },
  { value: 'build', label: 'Build from source',
    blurb: 'Vesta builds from a repository and pushes the result.' },
  { value: 'push', label: 'Deploy on push, built elsewhere',
    blurb: 'Your CI builds; a push tells Vesta to roll out the new tag.' },
  { value: 'template', label: 'Start from a template',
    blurb: 'Postgres, Redis and other ready-made apps.' },
]

function CreateAppForm({ projectId, environments, onClose }: { projectId: string; environments: any[]; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [envConfigs, setEnvConfigs] = useState<Record<string, EnvConfig>>({})
  const [imageRepo, setImageRepo] = useState('')
  const [imageTag, setImageTag] = useState('')
  const [pullPolicy, setPullPolicy] = useState('IfNotPresent')
  const [port, setPort] = useState('3000')
  const [pullSecrets, setPullSecrets] = useState<string[]>([])
  const [manualRepo, setManualRepo] = useState(false)
  const [gitHost, setGitHost] = useState('')
  const [gitConnectionId, setGitConnectionId] = useState('')

  // Git source
  const [gitRepo, setGitRepo] = useState('')
  const [gitProvider, setGitProvider] = useState('github')
  const [gitBranch, setGitBranch] = useState('')
  const [gitAutoDeploy, setGitAutoDeploy] = useState(false)
  const [gitTokenSecret, setGitTokenSecret] = useState('')

  // Build strategy
  const [buildStrategy, setBuildStrategy] = useState('')
  const [buildDockerfile, setBuildDockerfile] = useState('Dockerfile')

  // How this app gets its image. Asked outright instead of being inferred from which
  // fields happen to be filled in, which is what made "Image Repository" mean two opposite
  // things -- where to pull from with no repository linked, where the build pushes to with
  // one -- and put "no build" inside the build-strategy group.
  const [sourceMode, setSourceMode] = useState<SourceMode>('image')

  // Which stored credential the repository and tag suggestions are listed through. Separate
  // from pullSecrets because browsing and pulling are different questions, though choosing
  // one here adds it below: a registry you browse is almost always one you pull from.
  const [imageCredential, setImageCredential] = useState('')

  const [templateId, setTemplateId] = useState('')
  const [templateStorage, setTemplateStorage] = useState('')

  // Switching modes clears what the new mode cannot show. Without this a repository chosen
  // and then abandoned would still be submitted, giving the app a spec.git block the form
  // no longer displays -- and with autoDeployOnPush set, a push would deploy an app whose
  // owner had decided not to build from source.
  const chooseMode = (next: SourceMode) => {
    setSourceMode(next)
    if (next === 'image' || next === 'template') {
      setGitRepo(''); setGitBranch(''); setGitHost(''); setGitConnectionId('')
      setGitAutoDeploy(false); setGitTokenSecret('')
      setManualRepo(false)
    }
    if (next !== 'build') {
      setBuildStrategy(''); setBuildDockerfile('Dockerfile')
    }
    if (next !== 'template') {
      setTemplateId(''); setTemplateStorage('')
    }
    if (next === 'build' && !buildStrategy) {
      // Every build needs a strategy, and dockerfile is the one that needs no detection.
      setBuildStrategy('dockerfile')
    }
    if (next === 'push') {
      // Deploying on push IS this mode, so it starts on. Left off, the app would be created
      // with a repository, a webhook that matches it, and nothing happening when someone
      // pushes -- which looks like a broken integration rather than an unticked box.
      setGitAutoDeploy(true)
    }
  }

  // Picking a credential to browse also makes it a pull secret, since an image listed
  // through a private registry cannot be pulled without one.
  const chooseCredential = (name: string) => {
    setImageCredential(name)
    if (name && !pullSecrets.includes(name)) setPullSecrets(prev => [...prev, name])
  }

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

  const deployTemplateMutation = useMutation({
    mutationFn: (data: Parameters<typeof api.deployTemplate>[1]) =>
      api.deployTemplate(templateId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projectApps', projectId] })
      queryClient.invalidateQueries({ queryKey: ['apps'] })
      onClose()
    },
  })

  const { data: templates } = useQuery({
    queryKey: ['templates'],
    queryFn: () => api.listTemplates(),
    enabled: sourceMode === 'template',
  })

  const submitting = mutation.isPending || deployTemplateMutation.isPending
  const submitError = (mutation.error ?? deployTemplateMutation.error) as Error | null

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

    // A template is a different endpoint with different inputs, so it gets its own path
    // rather than being folded into the app payload and pulled apart server-side.
    if (sourceMode === 'template') {
      deployTemplateMutation.mutate({
        project: projectId,
        name,
        environments: Object.keys(envConfigs),
        ...(templateStorage && { storageSize: templateStorage }),
      })
      return
    }

    // Git is sent for the two modes that have a repository. Build is sent only for the one
    // that builds -- "deploy on push" is exactly a git source with no build block, which is
    // the shape the old form could only reach by linking a repo and then choosing
    // "Image Only" inside the build strategy.
    const wantsGit = sourceMode === 'build' || sourceMode === 'push'
    const wantsBuild = sourceMode === 'build'

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
      ...(wantsGit && gitRepo && {
        git: {
          provider: gitProvider,
          repository: gitRepo,
          branch: gitBranch || 'main',
          autoDeployOnPush: gitAutoDeploy,
          ...(gitTokenSecret && { tokenSecret: gitTokenSecret }),
          // Identity, not decoration: the webhook matcher compares provider, host and path,
          // so a host recorded here is what lets a self-managed server be told apart from
          // its SaaS namesake.
          ...(gitHost && { host: gitHost }),
          ...(gitConnectionId && { connectionId: gitConnectionId }),
        },
      }),
      ...(wantsBuild && buildStrategy && buildStrategy !== 'image' && {
        build: {
          strategy: buildStrategy,
          ...(buildStrategy === 'dockerfile' && buildDockerfile !== 'Dockerfile' && { dockerfile: buildDockerfile }),
        },
      }),
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
                        <select value={config.podSize || ''} onChange={(e) => setEnvConfigs(prev => ({ ...prev, [env.name]: { ...prev[env.name], podSize: e.target.value } }))} className="input-field w-64 mt-1">
                          <option value="">Default</option>
                          {podSizes?.items?.map((s: any) => (<option key={s.name} value={s.name}>{s.name} ({s.cpu}/{s.memory} → {s.cpuLimit}/{s.memoryLimit})</option>))}
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

      <div>
        <label className="label">How should this app get its image?</label>
        <div className="grid grid-cols-2 gap-2">
          {SOURCE_MODES.map(m => (
            <button
              key={m.value}
              type="button"
              onClick={() => chooseMode(m.value)}
              className={`p-3 rounded-lg border text-left transition-all ${
                sourceMode === m.value
                  ? 'border-accent bg-accent/10'
                  : 'border-border bg-surface-1 hover:border-text-tertiary'
              }`}
            >
              <div className={`text-xs font-medium ${sourceMode === m.value ? 'text-accent' : 'text-text-primary'}`}>
                {m.label}
              </div>
              <div className="text-[10px] text-text-tertiary mt-0.5">{m.blurb}</div>
            </button>
          ))}
        </div>
      </div>

      {sourceMode !== 'template' && (
      <div className="grid grid-cols-3 gap-4">
        <div className="col-span-2">
          <label className="label">
            {sourceMode === 'build' ? 'Push built image to' : 'Image Repository'}
          </label>
          {/* Offers what the selected credential can see; typing something it cannot is
              still valid, since a credential need not see every repository. In build mode
              this is the destination, which is why it is labelled as one -- the same field
              used to say "Image Repository" while meaning the opposite direction. */}
          <ImageRepositoryInput
            secretName={imageCredential || pullSecrets[0]}
            value={imageRepo}
            onChange={setImageRepo}
            className="input-field w-full"
            placeholder="registry.example.com/org/app"
          />
        </div>
        <div>
          <label className="label">{sourceMode === 'build' ? 'Tag prefix' : 'Tag'}</label>
          <ImageTagInput
            secretName={imageCredential || pullSecrets[0]}
            repository={imageRepo}
            value={imageTag}
            onChange={setImageTag}
            className="input-field w-full"
            placeholder={sourceMode === 'build' ? 'from commit' : 'latest'}
          />
        </div>
      </div>
      )}

      {sourceMode !== 'template' && (
      <div>
        <label className="label">Registry credential</label>
        <select
          value={imageCredential}
          onChange={e => chooseCredential(e.target.value)}
          className="input-field w-full max-w-md"
        >
          <option value="">Public registry — no credential</option>
          {registrySecrets?.items?.map((sec: any) => (
            <option key={sec.name} value={sec.name}>{sec.name} ({sec.registry})</option>
          ))}
        </select>
        <p className="text-[11px] text-text-tertiary mt-1">
          {sourceMode === 'build'
            ? 'Where the build pushes, and what it authenticates with.'
            : 'Lists repositories and tags you can choose from. Leave unset for a public image and type the name.'}
        </p>
      </div>
      )}

      {sourceMode !== 'template' && (
      <div>
        <label className="label">Port</label>
        <input type="number" value={port} onChange={(e) => setPort(e.target.value)} className="input-field w-32" />
        <p className="text-[11px] text-text-tertiary mt-1">The port your app listens on.</p>
      </div>
      )}

      {sourceMode !== 'template' && (
      <details className="rounded-lg border border-border bg-surface-1 p-3">
        <summary className="text-xs text-text-secondary cursor-pointer">Advanced</summary>
        <div className="mt-3 space-y-4">
      <div>
        <label className="label">Pull Policy</label>
        <select value={pullPolicy} onChange={(e) => setPullPolicy(e.target.value)} className="input-field w-40">
          <option value="IfNotPresent">IfNotPresent</option>
          <option value="Always">Always</option>
          <option value="Never">Never</option>
        </select>
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
        </div>
      </details>
      )}

      {(sourceMode === 'build' || sourceMode === 'push') && (
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <label className="label mb-0">
            Repository <span className="text-status-failed">*</span>
          </label>
        </div>
        {/* Always open in these modes. The "+ Link Repository" button made sense when git
            was optional on a form that could not tell what you were doing; having chosen
            "build from source", being asked to opt into naming a repository is a step that
            asks a question already answered. */}
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
                {manualRepo ? (
                  <input
                    value={gitRepo}
                    onChange={e => setGitRepo(e.target.value)}
                    className="input-field font-mono text-xs w-full"
                    placeholder="org/repo-name"
                  />
                ) : (
                  <RepositoryPicker
                    value={gitRepo}
                    provider={gitProvider}
                    onSelect={sel => {
                      // Provider, host and connection are recorded together with the name.
                      // Set apart they can disagree, and a repository whose provider says
                      // GitHub while its host says gitlab.internal matches no push at all.
                      setGitRepo(sel.repository)
                      setGitProvider(sel.provider)
                      setGitHost(sel.host)
                      setGitConnectionId(sel.connectionId)
                      if (sel.defaultBranch && !gitBranch) setGitBranch(sel.defaultBranch)
                    }}
                    onClear={() => setManualRepo(true)}
                  />
                )}
              </div>
            </div>
            <div className="grid grid-cols-3 gap-3">
              <div>
                <label className="text-xs text-text-tertiary mb-1 block">Branch</label>
                <BranchPicker
                  repository={gitRepo}
                  provider={gitProvider}
                  host={gitHost}
                  connectionId={gitConnectionId}
                  value={gitBranch}
                  onChange={setGitBranch}
                />
              </div>
              {/* Only when nothing else can authenticate. A connection mints its own token
                  per build, and a token set here takes precedence over it -- so offering
                  the field for a repository a connection already covers invites people to
                  override working credentials with a worse copy. */}
              {!gitConnectionId && (
                <div>
                  <label className="text-xs text-text-tertiary mb-1 block">Token Secret</label>
                  <input value={gitTokenSecret} onChange={e => setGitTokenSecret(e.target.value)} className="input-field font-mono text-xs w-full" placeholder="github-token" />
                  <p className="text-[10px] text-text-tertiary mt-0.5">
                    No connection covers this repository, so cloning needs a secret with key &quot;token&quot;.
                  </p>
                </div>
              )}
              <div className="flex items-end pb-1">
                <label className="flex items-center gap-2 cursor-pointer">
                  <input type="checkbox" checked={gitAutoDeploy} onChange={e => setGitAutoDeploy(e.target.checked)} className="w-4 h-4 rounded border-border bg-surface-1 text-accent focus:ring-accent/20" />
                  <span className="text-xs text-text-secondary">Auto-deploy on push</span>
                </label>
              </div>
            </div>
            <button type="button" onClick={() => { setGitRepo(''); setGitBranch(''); setGitAutoDeploy(false); setGitTokenSecret('') }} className="text-xs text-status-failed hover:text-red-300">Remove</button>
        </div>
      </div>
      )}

      {/* Build strategy: only in the mode that builds. "Image Only" is gone from this group
          -- not building is now a source mode, chosen before any of this is shown, rather
          than the fourth option inside a control called Build Strategy. */}
      {sourceMode === 'build' && (
        <div className="space-y-3">
          <label className="label">Build Strategy <span className="text-status-failed">*</span></label>
          <div className="grid grid-cols-3 gap-2">
            {[
              { value: 'dockerfile', label: 'Dockerfile', desc: 'Kaniko build' },
              { value: 'nixpacks', label: 'Nixpacks', desc: 'Auto-detect' },
              { value: 'buildpacks', label: 'Buildpacks', desc: 'Cloud Native' },
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
              <label className="text-xs text-text-tertiary mb-1 block">Dockerfile path</label>
              <input value={buildDockerfile} onChange={e => setBuildDockerfile(e.target.value)} className="input-field font-mono text-xs w-48" placeholder="Dockerfile" />
            </div>
          )}
        </div>
      )}

      {/* Template mode replaces the source fields entirely: it submits to
          POST /templates/:id/deploy, which takes the project, name and environments this
          form already collects plus whatever the template itself asks for. */}
      {sourceMode === 'template' && (
        <div className="space-y-3">
          <label className="label">Template <span className="text-status-failed">*</span></label>
          <select
            value={templateId}
            onChange={e => setTemplateId(e.target.value)}
            className="input-field w-full"
          >
            <option value="">Select a template…</option>
            {templates?.items?.map((t: any) => (
              <option key={t.id} value={t.id}>
                {t.name}{t.category ? ` — ${t.category}` : ''}
              </option>
            ))}
          </select>
          {(templates?.items?.length ?? 0) === 0 && (
            <p className="text-[11px] text-text-tertiary">No templates available.</p>
          )}
          <div>
            <label className="label">Storage size</label>
            <input
              value={templateStorage}
              onChange={e => setTemplateStorage(e.target.value)}
              className="input-field font-mono text-xs w-40"
              placeholder="10Gi"
            />
            <p className="text-[11px] text-text-tertiary mt-1">
              Only used by templates that store data. Leave empty for the template's default.
            </p>
          </div>
        </div>
      )}

      {/* Scheduled jobs are set up on the app's Cronjobs tab. Defining a schedule for an app
          that has never run once is unusual, and it made this form longer for everybody. */}

      <div className="flex gap-3 pt-1">
        <button
          type="submit"
          disabled={submitting || (sourceMode === 'template' && !templateId)}
          className="btn-primary"
        >
          {submitting ? 'Creating...' : 'Create App'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
      </div>
      {mutation.isError && (
        <p className="text-status-failed text-xs">{submitError?.message}</p>
      )}
    </form>
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
