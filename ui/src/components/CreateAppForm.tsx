import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { ImageRepositoryInput, ImageTagInput } from './RegistryPicker'
import { RepositoryPicker } from './RepositoryPicker'
import { BranchPicker } from './BranchPicker'

/**
 * Creating an app, from wherever you started.
 *
 * There were two of these, with the same name and the same props, one on the Apps page and
 * one on the project page — and neither was a superset of the other. The project page could
 * not link a git repository at all, so an app created there could only ever deploy a
 * prebuilt image; the Apps page had no health check. Which page you happened to open
 * decided what you were allowed to configure, and nothing said so.
 *
 * This is the union of the two. The source mode decides what is shown, so the form is no
 * longer the sum of both — it is only ever as long as the choice you made needs.
 */

export interface EnvConfig {
  name: string
  replicas: number
  podSize?: string
  autoscale?: { enabled: boolean; minReplicas: number; maxReplicas: number; targetCPU: number }
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

export function CreateAppForm({ projectId, environments, onClose }: { projectId: string; environments: any[]; onClose: () => void }) {
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

  // Health check. Only the project page's form had this, so an app created from the Apps
  // page could not be given one at creation at all.
  const [hcEnabled, setHcEnabled] = useState(false)
  const [hcType, setHcType] = useState('http')
  const [hcPath, setHcPath] = useState('/')
  const [hcPort, setHcPort] = useState('')
  const [hcCommand, setHcCommand] = useState('')
  const [hcInitialDelay, setHcInitialDelay] = useState('0')
  const [hcPeriod, setHcPeriod] = useState('10')
  const [hcTimeout, setHcTimeout] = useState('1')
  const [hcFailureThreshold, setHcFailureThreshold] = useState('3')

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
      ...(hcEnabled && {
        healthCheck: {
          type: hcType,
          ...(hcType === 'http' && { path: hcPath }),
          ...(hcType !== 'exec' && hcPort && { port: parseInt(hcPort) }),
          ...(hcType === 'exec' && { command: hcCommand }),
          initialDelaySeconds: parseInt(hcInitialDelay) || 0,
          periodSeconds: parseInt(hcPeriod) || 10,
          timeoutSeconds: parseInt(hcTimeout) || 1,
          failureThreshold: parseInt(hcFailureThreshold) || 3,
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
                    <>
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
                      <div className="mt-3 pl-6 flex items-center gap-4 flex-wrap">
                        <div>
                          <label className="text-xs text-text-tertiary">Min</label>
                          <input type="number" min="1" value={config.autoscale.minReplicas} onChange={(e) => updateAutoscale(env.name, 'minReplicas', parseInt(e.target.value) || 1)} className="input-field w-16 mt-1" />
                        </div>
                        <div>
                          <label className="text-xs text-text-tertiary">Max</label>
                          <input type="number" min="1" value={config.autoscale.maxReplicas} onChange={(e) => updateAutoscale(env.name, 'maxReplicas', parseInt(e.target.value) || 1)} className="input-field w-16 mt-1" />
                        </div>
                        <div>
                          <label className="text-xs text-text-tertiary">Target CPU %</label>
                          <input type="number" min="1" max="100" value={config.autoscale.targetCPU} onChange={(e) => updateAutoscale(env.name, 'targetCPU', parseInt(e.target.value) || 80)} className="input-field w-20 mt-1" />
                        </div>
                      </div>
                    )}
                    </>
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
