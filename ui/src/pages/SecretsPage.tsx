import { useState, useRef, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { useUserRole } from '../lib/useRole'

export default function SecretsPage() {
  const queryClient = useQueryClient()
  const role = useUserRole()
  const isAdmin = role === 'admin'
  const { data, isLoading } = useQuery({ queryKey: ['secrets'], queryFn: () => api.listSecrets() })
  const { data: apps } = useQuery({ queryKey: ['apps'], queryFn: () => api.listApps() })
  const [showCreate, setShowCreate] = useState(false)
  const [activeTab, setActiveTab] = useState<'app' | 'registry' | 'shared'>('registry')

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deleteSecret(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['secrets'] }),
  })

  const grouped = groupSecrets(data?.items || [])

  return (
    <div className="space-y-6">
      <div className="flex border-b border-border mb-2">
        {(['registry', 'shared', 'app'] as const).map((tab) => (
          <button
            key={tab}
            onClick={() => setActiveTab(tab)}
            className={`px-5 py-3 text-xs font-mono tracking-wider uppercase transition-colors ${
              activeTab === tab
                ? 'text-accent border-b-2 border-accent'
                : 'text-text-tertiary hover:text-text-secondary'
            }`}
          >
            {tab === 'registry' ? 'Registry Credentials' : tab === 'shared' ? 'Shared Secrets' : 'App Secrets'}
          </button>
        ))}
      </div>

      {activeTab === 'registry' && <RegistrySecretsSection />}

      {activeTab === 'shared' && <SharedSecretsSection />}

      {activeTab === 'app' && (
        <>
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm text-text-secondary">
            {data?.total ?? 0} secret{(data?.total ?? 0) !== 1 ? 's' : ''}
          </p>
          <p className="text-xs text-text-tertiary mt-0.5">
            {isAdmin ? 'Admins can reveal values. All access is audit-logged.' : 'Values are write-only and cannot be read back after creation.'}
          </p>
        </div>
        <button onClick={() => setShowCreate(!showCreate)} className="btn-primary">
          <span className="flex items-center gap-2">
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
            </svg>
            New Secret
          </span>
        </button>
      </div>

      {showCreate && (
        <CreateSecretForm
          apps={apps?.items || []}
          onClose={() => setShowCreate(false)}
        />
      )}

      {isLoading && <Spinner />}

      {!isLoading && data?.items?.length === 0 && (
        <div className="card px-6 py-12 text-center">
          <div className="w-10 h-10 rounded-xl bg-surface-3 flex items-center justify-center mx-auto mb-3">
            <svg className="w-5 h-5 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
            </svg>
          </div>
          <p className="text-sm text-text-secondary">No secrets yet</p>
        </div>
      )}

      {Object.entries(grouped).map(([groupKey, secrets]) => (
        <section key={groupKey}>
          <h3 className="section-title mb-3">{groupKey}</h3>
          <div className="space-y-2">
            {secrets.map((s: any) => (
              <SecretItem key={s.id} secret={s} isAdmin={isAdmin} onDelete={(id) => deleteMutation.mutate(id)} />
            ))}
          </div>
        </section>
      ))}

      {!isLoading && (data?.items?.length ?? 0) > 0 && Object.keys(grouped).length === 0 && (
        <div className="space-y-2">
          {data?.items?.map((s: any) => (
            <SecretItem key={s.id} secret={s} isAdmin={isAdmin} onDelete={(id) => deleteMutation.mutate(id)} />
          ))}
        </div>
      )}
        </>
      )}
    </div>
  )
}

function SecretItem({ secret: s, isAdmin, onDelete }: { secret: any; isAdmin: boolean; onDelete: (id: string) => void }) {
  const [revealed, setRevealed] = useState<Record<string, string> | null>(null)
  const [loading, setLoading] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)

  useEffect(() => {
    if (!revealed) return
    const timer = setTimeout(() => setRevealed(null), 30000)
    return () => clearTimeout(timer)
  }, [revealed])

  const handleReveal = async () => {
    setConfirmOpen(false)
    setLoading(true)
    try {
      const res = await api.revealSecretValues(s.id)
      setRevealed(res.values)
    } catch {
      setRevealed(null)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="card-hover px-5 py-4 group">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-4">
          <div className="w-9 h-9 rounded-lg bg-surface-3 flex items-center justify-center">
            <svg className="w-4 h-4 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
            </svg>
          </div>
          <div>
            <p className="text-sm font-medium text-text-primary">{s.name}</p>
            <div className="flex items-center gap-2 mt-0.5">
              <span className="text-xs text-text-tertiary font-mono">{s.type || 'Opaque'}</span>
              {s.environment && (
                <span className="text-[11px] font-mono bg-surface-3 text-text-tertiary px-2 py-0.5 rounded">
                  {s.environment}
                </span>
              )}
            </div>
            {!revealed && s.keys?.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mt-2">
                {s.keys.map((k: string) => (
                  <span key={k} className="text-[11px] font-mono bg-surface-3 text-text-tertiary px-2 py-0.5 rounded">
                    {k}
                  </span>
                ))}
              </div>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2">
          {isAdmin && s.keys?.length > 0 && !revealed && (
            <button
              onClick={() => setConfirmOpen(true)}
              disabled={loading}
              className="text-xs text-text-tertiary hover:text-accent transition-colors opacity-0 group-hover:opacity-100"
            >
              {loading ? 'Loading...' : 'Reveal'}
            </button>
          )}
          {revealed && (
            <button
              onClick={() => setRevealed(null)}
              className="text-xs text-text-tertiary hover:text-accent transition-colors"
            >
              Hide
            </button>
          )}
          <button
            onClick={() => onDelete(s.id)}
            className="text-xs text-text-tertiary hover:text-status-failed transition-colors opacity-0 group-hover:opacity-100"
          >
            Delete
          </button>
        </div>
      </div>

      {revealed && (
        <div className="mt-3 ml-13 border-t border-border pt-3">
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

      {confirmOpen && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50" onClick={() => setConfirmOpen(false)}>
          <div className="card p-6 w-full max-w-sm animate-slide-up" onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center gap-3 mb-4">
              <div className="w-10 h-10 rounded-xl bg-yellow-500/10 flex items-center justify-center">
                <svg className="w-5 h-5 text-yellow-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                </svg>
              </div>
              <div>
                <h3 className="text-sm font-semibold text-text-primary">Reveal secret values</h3>
                <p className="text-xs text-text-tertiary">This action is audit-logged</p>
              </div>
            </div>
            <p className="text-xs text-text-secondary mb-4">
              You are about to reveal the values of <span className="font-mono text-text-primary">{s.name}</span>.
              This will be recorded in the audit log with your user ID and IP address.
            </p>
            <div className="flex gap-3">
              <button onClick={handleReveal} className="btn-primary text-xs">
                Reveal Values
              </button>
              <button onClick={() => setConfirmOpen(false)} className="btn-ghost text-xs">
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function SharedSecretsSection() {
  const queryClient = useQueryClient()
  const { data: projects } = useQuery({ queryKey: ['projects'], queryFn: () => api.listProjects() })
  const [selectedProject, setSelectedProject] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [name, setName] = useState('')
  const [keys, setKeys] = useState([{ key: '', value: '' }])

  const projectId = selectedProject || projects?.items?.[0]?.name || ''

  const { data, isLoading } = useQuery({
    queryKey: ['sharedSecrets', projectId],
    queryFn: () => api.listSharedSecrets(projectId),
    enabled: !!projectId,
  })

  const createMutation = useMutation({
    mutationFn: () => {
      const secretData: Record<string, string> = {}
      keys.forEach(kv => { if (kv.key) secretData[kv.key] = kv.value })
      return api.createSharedSecret(projectId, { name, data: secretData })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['sharedSecrets'] })
      setShowCreate(false)
      setName('')
      setKeys([{ key: '', value: '' }])
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (n: string) => api.deleteSharedSecret(projectId, n),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['sharedSecrets'] }),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm text-text-secondary">
            Project-scoped secrets shared across multiple apps.
          </p>
          <p className="text-xs text-text-tertiary mt-0.5">
            Bind shared secrets to any app in the same project — all keys injected as env vars.
          </p>
        </div>
        <div className="flex items-center gap-3">
          {(projects?.items?.length ?? 0) > 0 && (
            <select
              value={projectId}
              onChange={(e) => setSelectedProject(e.target.value)}
              className="input-field text-xs"
            >
              {projects?.items?.map((p: any) => (
                <option key={p.name} value={p.name}>{p.name}</option>
              ))}
            </select>
          )}
          <button onClick={() => setShowCreate(!showCreate)} className="btn-primary" disabled={!projectId}>
            <span className="flex items-center gap-2">
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
              </svg>
              New Shared Secret
            </span>
          </button>
        </div>
      </div>

      {showCreate && (
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate() }} className="card p-5 space-y-4 animate-slide-up">
          <h3 className="section-title">Create Shared Secret</h3>
          <div>
            <label className="label">Secret Name</label>
            <input value={name} onChange={(e) => setName(e.target.value)} className="input-field" placeholder="redis-config" required />
            <p className="text-[11px] text-text-tertiary mt-1">Apps will reference this name when binding</p>
          </div>
          <div>
            <label className="label mb-2">Key-Value Pairs</label>
            <div className="space-y-2">
              {keys.map((kv, i) => (
                <div key={i} className="flex gap-2">
                  <input
                    value={kv.key}
                    onChange={(e) => { const u = [...keys]; u[i].key = e.target.value; setKeys(u) }}
                    placeholder="REDIS_URL"
                    className="input-field flex-1 font-mono text-xs"
                  />
                  <input
                    value={kv.value}
                    onChange={(e) => { const u = [...keys]; u[i].value = e.target.value; setKeys(u) }}
                    placeholder="redis://redis:6379"
                    type="password"
                    className="input-field flex-1"
                  />
                  {keys.length > 1 && (
                    <button type="button" onClick={() => setKeys(keys.filter((_, idx) => idx !== i))} className="px-2 text-text-tertiary hover:text-status-failed transition-colors">
                      &times;
                    </button>
                  )}
                </div>
              ))}
            </div>
            <button type="button" onClick={() => setKeys([...keys, { key: '', value: '' }])} className="text-xs text-accent hover:text-accent-glow transition-colors mt-2 font-mono">
              + Add key
            </button>
          </div>
          <div className="flex gap-3 pt-1">
            <button type="submit" disabled={!name || !keys.some(k => k.key) || createMutation.isPending} className="btn-primary">
              {createMutation.isPending ? 'Creating...' : 'Create'}
            </button>
            <button type="button" onClick={() => setShowCreate(false)} className="btn-ghost">Cancel</button>
          </div>
          {createMutation.isError && (
            <p className="text-status-failed text-xs">{(createMutation.error as Error).message}</p>
          )}
        </form>
      )}

      {isLoading && <Spinner />}

      {!isLoading && (!data?.items || data.items.length === 0) && projectId && (
        <div className="card px-6 py-12 text-center">
          <div className="w-10 h-10 rounded-xl bg-surface-3 flex items-center justify-center mx-auto mb-3">
            <svg className="w-5 h-5 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
            </svg>
          </div>
          <p className="text-sm text-text-secondary">No shared secrets in {projectId}</p>
          <p className="text-xs text-text-tertiary mt-1">Create a shared secret and bind it to apps that need it</p>
        </div>
      )}

      {data?.items?.map((s: any) => (
        <div key={s.name} className="card-hover px-5 py-4 group">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-4">
              <div className="w-9 h-9 rounded-lg bg-accent/10 flex items-center justify-center">
                <svg className="w-4 h-4 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
                </svg>
              </div>
              <div>
                <p className="text-sm font-medium text-text-primary font-mono">{s.name}</p>
                <div className="flex items-center gap-3 mt-1 flex-wrap">
                  {s.keys?.map((k: string) => (
                    <span key={k} className="px-2 py-0.5 bg-surface-3 rounded text-[11px] font-mono text-text-secondary">{k}</span>
                  ))}
                </div>
                {s.environments?.length > 0 && (
                  <div className="flex items-center gap-2 mt-1">
                    <span className="text-[11px] text-text-tertiary">Environments:</span>
                    {s.environments.map((env: string) => (
                      <span key={env} className="text-[11px] font-mono text-text-secondary">{env}</span>
                    ))}
                  </div>
                )}
              </div>
            </div>
            <button
              onClick={() => { if (confirm(`Delete shared secret "${s.name}" from all environments?`)) deleteMutation.mutate(s.name) }}
              className="text-xs text-text-tertiary hover:text-status-failed transition-colors opacity-0 group-hover:opacity-100"
            >
              Delete
            </button>
          </div>
        </div>
      ))}
    </div>
  )
}

function RegistrySecretsSection() {
  const queryClient = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['registrySecrets'], queryFn: () => api.listRegistrySecrets() })
  const [showCreate, setShowCreate] = useState(false)
  const [name, setName] = useState('')
  const [registry, setRegistry] = useState('https://index.docker.io/v1/')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')

  const createMutation = useMutation({
    mutationFn: () => api.createRegistrySecret({ name, registry, username, password }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['registrySecrets'] })
      setShowCreate(false)
      setName('')
      setRegistry('https://index.docker.io/v1/')
      setUsername('')
      setPassword('')
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (n: string) => api.deleteRegistrySecret(n),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['registrySecrets'] }),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm text-text-secondary">
            Docker registry credentials for pulling private images.
          </p>
          <p className="text-xs text-text-tertiary mt-0.5">
            Attach to projects or override per app environment.
          </p>
        </div>
        <button onClick={() => setShowCreate(!showCreate)} className="btn-primary">
          <span className="flex items-center gap-2">
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
            </svg>
            Add Registry
          </span>
        </button>
      </div>

      {showCreate && (
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate() }} className="card p-5 space-y-4 animate-slide-up">
          <h3 className="section-title">Add Registry Credential</h3>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="label">Name</label>
              <input value={name} onChange={(e) => setName(e.target.value)} className="input-field" placeholder="ghcr-creds" required />
            </div>
            <div>
              <label className="label">Registry URL</label>
              <input value={registry} onChange={(e) => setRegistry(e.target.value)} className="input-field font-mono text-xs" placeholder="https://index.docker.io/v1/" required />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="label">Username</label>
              <input value={username} onChange={(e) => setUsername(e.target.value)} className="input-field" placeholder="username" required />
            </div>
            <div>
              <label className="label">Password / Token</label>
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} className="input-field" placeholder="••••••••" required />
            </div>
          </div>
          <div className="flex gap-3 pt-1">
            <button type="submit" disabled={createMutation.isPending} className="btn-primary">
              {createMutation.isPending ? 'Creating...' : 'Create'}
            </button>
            <button type="button" onClick={() => setShowCreate(false)} className="btn-ghost">Cancel</button>
          </div>
          {createMutation.isError && (
            <p className="text-status-failed text-xs">{(createMutation.error as Error).message}</p>
          )}
        </form>
      )}

      {isLoading && <Spinner />}

      {!isLoading && (!data?.items || data.items.length === 0) && (
        <div className="card px-6 py-12 text-center">
          <div className="w-10 h-10 rounded-xl bg-surface-3 flex items-center justify-center mx-auto mb-3">
            <svg className="w-5 h-5 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
            </svg>
          </div>
          <p className="text-sm text-text-secondary">No registry credentials yet</p>
          <p className="text-xs text-text-tertiary mt-1">Add Docker login credentials to pull private images</p>
        </div>
      )}

      {data?.items?.map((s: any) => (
        <div key={s.id} className="card-hover flex items-center justify-between px-5 py-4 group">
          <div className="flex items-center gap-4">
            <div className="w-9 h-9 rounded-lg bg-surface-3 flex items-center justify-center">
              <svg className="w-4 h-4 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M5 12h14M12 5l7 7-7 7" />
              </svg>
            </div>
            <div>
              <p className="text-sm font-medium text-text-primary">{s.name}</p>
              <div className="flex items-center gap-3 mt-0.5">
                <span className="text-xs text-text-tertiary font-mono">{s.registry}</span>
                <span className="text-[11px] text-text-tertiary">user: {s.username}</span>
              </div>
            </div>
          </div>
          <button
            onClick={() => { if (confirm(`Delete registry credential "${s.name}"?`)) deleteMutation.mutate(s.name) }}
            className="text-xs text-text-tertiary hover:text-status-failed transition-colors opacity-0 group-hover:opacity-100"
          >
            Delete
          </button>
        </div>
      ))}
    </div>
  )
}

function groupSecrets(secrets: any[]): Record<string, any[]> {
  const groups: Record<string, any[]> = {}
  for (const s of secrets) {
    const project = s.projectName || s.project || ''
    const app = s.appName || s.app || ''
    const env = s.environment || ''
    const key = [project, app, env].filter(Boolean).join(' / ') || ''
    if (!key) continue
    if (!groups[key]) groups[key] = []
    groups[key].push(s)
  }
  return groups
}

function CreateSecretForm({ apps, onClose }: { apps: any[]; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [selectedAppId, setSelectedAppId] = useState('')
  const [selectedEnv, setSelectedEnv] = useState('')
  const [name, setName] = useState('')
  const [type, setType] = useState('Opaque')
  const [keys, setKeys] = useState([{ key: '', value: '' }])
  const [inputMode, setInputMode] = useState<'manual' | 'env'>('manual')
  const [envInput, setEnvInput] = useState('')
  const fileInputRef = useRef<HTMLInputElement>(null)

  const selectedApp = apps.find((a: any) => a.id === selectedAppId)
  const appEnvironments: string[] = selectedApp?.environments || selectedApp?.spec?.environments || []

  const parseEnvContent = (content: string): Record<string, string> => {
    const result: Record<string, string> = {}
    for (const line of content.split('\n')) {
      const trimmed = line.trim()
      if (!trimmed || trimmed.startsWith('#')) continue
      const eqIdx = trimmed.indexOf('=')
      if (eqIdx === -1) continue
      const key = trimmed.slice(0, eqIdx).trim()
      let value = trimmed.slice(eqIdx + 1).trim()
      if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
        value = value.slice(1, -1)
      }
      if (key) result[key] = value
    }
    return result
  }

  const parsedEnvKeys = inputMode === 'env' ? parseEnvContent(envInput) : {}

  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = (ev) => setEnvInput(ev.target?.result as string)
    reader.readAsText(file)
    e.target.value = ''
  }

  const mutation = useMutation({
    mutationFn: (data: Record<string, string>) => {
      if (selectedAppId && selectedEnv) {
        return api.createAppEnvSecret(selectedAppId, selectedEnv, { data })
      }
      return api.updateSecret('', { data })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['secrets'] })
      onClose()
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    let data: Record<string, string> = {}
    if (inputMode === 'env') {
      data = parseEnvContent(envInput)
    } else {
      keys.forEach((kv) => { if (kv.key) data[kv.key] = kv.value })
    }
    if (Object.keys(data).length === 0) return
    mutation.mutate(data)
  }

  const hasData = inputMode === 'env'
    ? Object.keys(parsedEnvKeys).length > 0
    : keys.some(kv => kv.key)

  return (
    <form onSubmit={handleSubmit} className="card p-5 space-y-4 animate-slide-up">
      <h3 className="section-title">Create Secret</h3>

      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="label">App</label>
          <select
            value={selectedAppId}
            onChange={(e) => { setSelectedAppId(e.target.value); setSelectedEnv('') }}
            className="input-field"
            required
          >
            <option value="">Select an app...</option>
            {apps.map((a: any) => (
              <option key={a.id} value={a.id}>{a.name} ({a.projectName || a.project || '—'})</option>
            ))}
          </select>
        </div>
        <div>
          <label className="label">Environment</label>
          <select
            value={selectedEnv}
            onChange={(e) => setSelectedEnv(e.target.value)}
            className="input-field"
            required
            disabled={!selectedAppId || appEnvironments.length === 0}
          >
            <option value="">Select environment...</option>
            {appEnvironments.map((env: string) => (
              <option key={env} value={env}>{env}</option>
            ))}
          </select>
          {selectedAppId && appEnvironments.length === 0 && (
            <p className="text-[11px] text-text-tertiary mt-1">No environments on this app</p>
          )}
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="label">Name</label>
          <input value={name} onChange={(e) => setName(e.target.value)} className="input-field" required placeholder="my-secret" />
        </div>
        <div>
          <label className="label">Type</label>
          <select value={type} onChange={(e) => setType(e.target.value)} className="input-field">
            <option value="Opaque">Opaque</option>
            <option value="kubernetes.io/dockerconfigjson">Docker Registry</option>
            <option value="kubernetes.io/tls">TLS</option>
          </select>
        </div>
      </div>

      <div>
        <div className="flex items-center justify-between mb-2">
          <label className="label mb-0">Secret Data</label>
          <div className="flex items-center gap-1 bg-surface-1 border border-border rounded-lg p-0.5">
            <button
              type="button"
              onClick={() => setInputMode('manual')}
              className={`px-3 py-1 text-xs rounded-md transition-colors ${
                inputMode === 'manual'
                  ? 'bg-surface-3 text-accent'
                  : 'text-text-tertiary hover:text-text-secondary'
              }`}
            >
              Key-Value
            </button>
            <button
              type="button"
              onClick={() => setInputMode('env')}
              className={`px-3 py-1 text-xs rounded-md transition-colors ${
                inputMode === 'env'
                  ? 'bg-surface-3 text-accent'
                  : 'text-text-tertiary hover:text-text-secondary'
              }`}
            >
              Paste .env
            </button>
          </div>
        </div>

        {inputMode === 'manual' && (
          <>
            <div className="space-y-2">
              {keys.map((kv, i) => (
                <div key={i} className="flex gap-2">
                  <input
                    value={kv.key}
                    onChange={(e) => { const u = [...keys]; u[i].key = e.target.value; setKeys(u) }}
                    placeholder="KEY"
                    className="input-field flex-1 font-mono text-xs"
                  />
                  <input
                    value={kv.value}
                    onChange={(e) => { const u = [...keys]; u[i].value = e.target.value; setKeys(u) }}
                    placeholder="value"
                    type="password"
                    className="input-field flex-1"
                  />
                  {keys.length > 1 && (
                    <button type="button" onClick={() => setKeys(keys.filter((_, idx) => idx !== i))} className="px-2 text-text-tertiary hover:text-status-failed transition-colors">
                      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  )}
                </div>
              ))}
            </div>
            <button type="button" onClick={() => setKeys([...keys, { key: '', value: '' }])} className="text-xs text-accent hover:text-accent-glow transition-colors mt-2 font-mono">
              + Add key
            </button>
          </>
        )}

        {inputMode === 'env' && (
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <p className="text-[11px] text-text-tertiary">Paste .env content or upload a file. Format: KEY=value (one per line, # comments ignored).</p>
              <button
                type="button"
                onClick={() => fileInputRef.current?.click()}
                className="text-xs text-accent hover:text-accent-glow transition-colors font-mono flex-shrink-0 ml-2"
              >
                Upload file
              </button>
              <input ref={fileInputRef} type="file" accept=".env,.env.*,text/plain" onChange={handleFileUpload} className="hidden" />
            </div>
            <textarea
              value={envInput}
              onChange={(e) => setEnvInput(e.target.value)}
              placeholder={"DATABASE_URL=postgres://...\nAPI_KEY=sk-...\nSECRET_TOKEN=abc123\n# Comments are ignored"}
              rows={8}
              className="input-field font-mono text-xs w-full"
              spellCheck={false}
            />
            {envInput && (
              <p className="text-[11px] text-text-tertiary">
                {Object.keys(parsedEnvKeys).length} key{Object.keys(parsedEnvKeys).length !== 1 ? 's' : ''} detected
              </p>
            )}
          </div>
        )}
      </div>

      <div className="flex gap-3 pt-1">
        <button type="submit" disabled={!selectedAppId || !selectedEnv || !name || !hasData || mutation.isPending} className="btn-primary">
          {mutation.isPending ? 'Creating...' : 'Create Secret'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
      </div>
      {mutation.isError && (
        <p className="text-status-failed text-xs">{(mutation.error as Error).message}</p>
      )}
    </form>
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
