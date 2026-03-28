import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

export default function SecretsPage() {
  const queryClient = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['secrets'], queryFn: () => api.listSecrets() })
  const { data: apps } = useQuery({ queryKey: ['apps'], queryFn: () => api.listApps() })
  const [showCreate, setShowCreate] = useState(false)

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deleteSecret(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['secrets'] }),
  })

  const grouped = groupSecrets(data?.items || [])

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm text-text-secondary">
            {data?.total ?? 0} secret{(data?.total ?? 0) !== 1 ? 's' : ''}
          </p>
          <p className="text-xs text-text-tertiary mt-0.5">
            Values are write-only and cannot be read back after creation.
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
              <div key={s.id} className="card-hover flex items-center justify-between px-5 py-4 group">
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
                    {s.keys?.length > 0 && (
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
                <button
                  onClick={() => deleteMutation.mutate(s.id)}
                  className="text-xs text-text-tertiary hover:text-status-failed transition-colors opacity-0 group-hover:opacity-100"
                >
                  Delete
                </button>
              </div>
            ))}
          </div>
        </section>
      ))}

      {!isLoading && (data?.items?.length ?? 0) > 0 && Object.keys(grouped).length === 0 && (
        <div className="space-y-2">
          {data?.items?.map((s: any) => (
            <div key={s.id} className="card-hover flex items-center justify-between px-5 py-4 group">
              <div className="flex items-center gap-4">
                <div className="w-9 h-9 rounded-lg bg-surface-3 flex items-center justify-center">
                  <svg className="w-4 h-4 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
                  </svg>
                </div>
                <div>
                  <p className="text-sm font-medium text-text-primary">{s.name}</p>
                  <span className="text-xs text-text-tertiary font-mono">{s.type || 'Opaque'}</span>
                </div>
              </div>
              <button
                onClick={() => deleteMutation.mutate(s.id)}
                className="text-xs text-text-tertiary hover:text-status-failed transition-colors opacity-0 group-hover:opacity-100"
              >
                Delete
              </button>
            </div>
          ))}
        </div>
      )}
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

  const selectedApp = apps.find((a: any) => a.id === selectedAppId)
  const appEnvironments: string[] = selectedApp?.environments || selectedApp?.spec?.environments || []

  const mutation = useMutation({
    mutationFn: () => {
      const data: Record<string, string> = {}
      keys.forEach((kv) => { if (kv.key) data[kv.key] = kv.value })

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
    const data: Record<string, string> = {}
    keys.forEach((kv) => { if (kv.key) data[kv.key] = kv.value })

    if (selectedAppId && selectedEnv) {
      api.createAppEnvSecret(selectedAppId, selectedEnv, { data })
        .then(() => {
          queryClient.invalidateQueries({ queryKey: ['secrets'] })
          onClose()
        })
        .catch(() => {})
    }
  }

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
        <label className="label">Key-Value Pairs</label>
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
      </div>

      <div className="flex gap-3 pt-1">
        <button type="submit" disabled={!selectedAppId || !selectedEnv || !name || mutation.isPending} className="btn-primary">
          {mutation.isPending ? 'Creating...' : 'Create Secret'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
      </div>
    </form>
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
