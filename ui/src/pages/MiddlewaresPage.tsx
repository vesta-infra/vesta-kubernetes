import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type Middleware, type MiddlewareType, type MiddlewarePayload } from '../lib/api'

// Each type's form is described as data rather than as nine bespoke components. The fields
// are few and regular, and a table keeps the Traefik field names visible -- which matters
// because the Traefik documentation is what people will search when a middleware does not
// behave, and a renamed field makes that search fail.
type FieldKind = 'number' | 'text' | 'list' | 'boolean' | 'map'

interface FieldSpec {
  key: string
  label: string
  kind: FieldKind
  help?: string
  placeholder?: string
}

const TYPE_FIELDS: Record<MiddlewareType, { blurb: string; fields: FieldSpec[] }> = {
  rateLimit: {
    blurb: 'Caps requests per client over a sliding window.',
    fields: [
      { key: 'average', label: 'Average', kind: 'number', help: 'Sustained requests allowed per period.' },
      { key: 'burst', label: 'Burst', kind: 'number', help: 'How far a short spike may exceed the average.' },
      { key: 'period', label: 'Period', kind: 'text', placeholder: '1s', help: 'Go duration. Empty means 1s.' },
    ],
  },
  basicAuth: {
    blurb: 'Prompts for a username and password. Passwords are hashed before they are stored — Vesta keeps them in a Secret, never on the middleware.',
    fields: [
      { key: 'realm', label: 'Realm', kind: 'text', placeholder: 'Staging' },
      { key: 'removeHeader', label: 'Strip Authorization header before proxying', kind: 'boolean' },
    ],
  },
  ipAllowList: {
    blurb: 'Rejects requests from outside the listed ranges.',
    fields: [
      { key: 'sourceRange', label: 'Allowed CIDRs', kind: 'list', placeholder: '10.0.0.0/8', help: 'One per line. An empty list would reject everyone, so it is refused.' },
    ],
  },
  headers: {
    blurb: 'Sets request and response headers, including CORS.',
    fields: [
      { key: 'accessControlAllowOriginList', label: 'CORS allowed origins', kind: 'list', placeholder: 'https://example.com' },
      { key: 'accessControlAllowMethods', label: 'CORS allowed methods', kind: 'list', placeholder: 'GET' },
      { key: 'accessControlAllowHeaders', label: 'CORS allowed headers', kind: 'list', placeholder: 'Authorization' },
      { key: 'accessControlMaxAge', label: 'CORS max age (seconds)', kind: 'number' },
      { key: 'customRequestHeaders', label: 'Request headers', kind: 'map' },
      { key: 'customResponseHeaders', label: 'Response headers', kind: 'map' },
      { key: 'stsSeconds', label: 'HSTS max-age (seconds)', kind: 'number' },
      { key: 'frameDeny', label: 'Deny framing (X-Frame-Options)', kind: 'boolean' },
      { key: 'contentTypeNosniff', label: 'Disable MIME sniffing', kind: 'boolean' },
    ],
  },
  stripPrefix: {
    blurb: 'Removes a path prefix before the request reaches the app.',
    fields: [
      { key: 'prefixes', label: 'Prefixes', kind: 'list', placeholder: '/api' },
    ],
  },
  compress: {
    blurb: 'Compresses responses. The defaults are sensible, so no configuration is required.',
    fields: [
      { key: 'minResponseBodyBytes', label: 'Minimum body to compress (bytes)', kind: 'number' },
      { key: 'excludedContentTypes', label: 'Excluded content types', kind: 'list', placeholder: 'image/png' },
    ],
  },
  retry: {
    blurb: 'Retries when the backend closes the connection before responding. A 500 that arrived is never retried.',
    fields: [
      { key: 'attempts', label: 'Attempts', kind: 'number' },
      { key: 'initialInterval', label: 'Initial interval', kind: 'text', placeholder: '100ms' },
    ],
  },
  circuitBreaker: {
    blurb: 'Stops sending traffic to a backend that is failing.',
    fields: [
      { key: 'expression', label: 'Expression', kind: 'text', placeholder: 'NetworkErrorRatio() > 0.5' },
      { key: 'checkPeriod', label: 'Check period', kind: 'text', placeholder: '10s' },
      { key: 'fallbackDuration', label: 'Fallback duration', kind: 'text', placeholder: '10s' },
      { key: 'recoveryDuration', label: 'Recovery duration', kind: 'text', placeholder: '10s' },
    ],
  },
  buffering: {
    blurb: 'Caps request and response body sizes -- the Traefik equivalent of nginx client_max_body_size.',
    fields: [
      { key: 'maxRequestBodyBytes', label: 'Max request body (bytes)', kind: 'number' },
      { key: 'maxResponseBodyBytes', label: 'Max response body (bytes)', kind: 'number' },
      { key: 'memRequestBodyBytes', label: 'Buffer in memory up to (bytes)', kind: 'number' },
    ],
  },
  raw: {
    blurb: 'Passes a Traefik middleware spec through untouched, for plugins and anything without a form here.',
    fields: [],
  },
}

const TYPE_LABELS: Record<MiddlewareType, string> = {
  rateLimit: 'Rate limit', basicAuth: 'Basic auth', ipAllowList: 'IP allow list',
  headers: 'Headers & CORS', stripPrefix: 'Strip prefix', compress: 'Compress',
  retry: 'Retry', circuitBreaker: 'Circuit breaker', buffering: 'Buffering', raw: 'Raw / plugin',
}

export default function MiddlewaresPage() {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<Middleware | null>(null)
  const [creating, setCreating] = useState(false)

  const { data, isLoading } = useQuery({
    queryKey: ['middlewares'],
    queryFn: () => api.listMiddlewares(),
  })

  const remove = useMutation({
    mutationFn: ({ name, force }: { name: string; force: boolean }) => api.deleteMiddleware(name, force),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['middlewares'] }),
  })

  const middlewares = data?.middlewares ?? []

  return (
    <div className="space-y-6">
      {/* Layout already renders the page title, so this row carries the count and the
          action only -- the explanation lives in the empty state, where someone who has
          not met the feature is the one reading. */}
      <div className="flex items-center justify-between gap-4">
        <p className="text-sm text-text-secondary">
          {middlewares.length} middleware{middlewares.length !== 1 ? 's' : ''}
        </p>
        <button
          onClick={() => { setCreating(true); setEditing(null) }}
          className="btn-primary whitespace-nowrap"
        >
          <span className="flex items-center gap-2">
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
            </svg>
            New Middleware
          </span>
        </button>
      </div>

      {isLoading && <p className="text-sm text-text-tertiary">Loading…</p>}

      {!isLoading && middlewares.length === 0 && !creating && (
        <div className="card p-8 text-center">
          <p className="text-sm text-text-secondary">No middlewares yet.</p>
          <p className="text-xs text-text-tertiary mt-2 max-w-md mx-auto leading-relaxed">
            Reusable ingress policy — rate limits, authentication, allow lists, headers.
            Define one here, then attach it to an app’s environments from the app’s Ingress tab.
          </p>
        </div>
      )}

      <div className="space-y-2">
        {middlewares.map(mw => (
          <div key={mw.name} className="card p-4">
            <div className="flex items-start justify-between gap-4">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-sm">{mw.name}</span>
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-surface-hover text-text-tertiary">
                    {TYPE_LABELS[mw.type] ?? mw.type}
                  </span>
                  {mw.project && (
                    <span className="text-[10px] px-1.5 py-0.5 rounded bg-surface-hover text-text-tertiary">
                      {mw.project}
                    </span>
                  )}
                </div>
                {mw.description && <p className="text-xs text-text-tertiary mt-1">{mw.description}</p>}
                <p className="text-[11px] text-text-tertiary mt-1.5">
                  {mw.appliedCount > 0
                    ? `Applied in ${mw.appliedCount} namespace${mw.appliedCount === 1 ? '' : 's'}`
                    : 'Not attached to any app yet'}
                </p>
                {/* A middleware that cannot be projected is configuration that looks applied
                    and is not, which is the failure this whole surface exists to prevent. */}
                {!mw.ready && mw.reason && (
                  <p className="text-[11px] text-status-failed mt-1.5">Not active: {mw.reason}</p>
                )}
              </div>
              <div className="flex gap-2 shrink-0">
                <button onClick={() => { setEditing(mw); setCreating(false) }} className="text-xs text-accent hover:text-accent-glow">
                  Edit
                </button>
                <button
                  onClick={() => {
                    if (!confirm(`Delete ${mw.name}?`)) return
                    remove.mutate({ name: mw.name, force: false })
                  }}
                  className="text-xs text-text-tertiary hover:text-status-failed"
                >
                  Delete
                </button>
              </div>
            </div>
          </div>
        ))}
      </div>

      {remove.isError && (
        <p className="text-xs text-status-failed mt-3">{(remove.error as Error).message}</p>
      )}

      {(creating || editing) && (
        <MiddlewareForm
          existing={editing}
          onClose={() => { setCreating(false); setEditing(null) }}
          onSaved={() => {
            queryClient.invalidateQueries({ queryKey: ['middlewares'] })
            setCreating(false); setEditing(null)
          }}
        />
      )}
    </div>
  )
}

function MiddlewareForm({ existing, onClose, onSaved }: {
  existing: Middleware | null
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(existing?.name ?? '')
  const [type, setType] = useState<MiddlewareType>(existing?.type ?? 'rateLimit')
  const [description, setDescription] = useState(existing?.description ?? '')
  const [config, setConfig] = useState<Record<string, any>>(() => {
    const initial = { ...(existing?.config ?? {}) }
    if (existing?.type === 'basicAuth') {
      // Passwords are hashed and never come back, so each existing account starts with a
      // blank password field meaning "leave this one alone".
      initial.users = (existing.config?.users ?? []).map((u: string) => ({ username: u, password: '' }))
    }
    return initial
  })
  const [rawText, setRawText] = useState(
    existing?.type === 'raw' ? JSON.stringify(existing.config, null, 2) : '{\n  "compress": {}\n}')
  const [error, setError] = useState('')

  const save = useMutation({
    mutationFn: (payload: MiddlewarePayload) =>
      existing ? api.updateMiddleware(existing.name, payload) : api.createMiddleware(payload),
    onSuccess: onSaved,
    onError: (e: Error) => setError(e.message),
  })

  const spec = TYPE_FIELDS[type]

  function submit() {
    setError('')
    let finalConfig = config

    if (type === 'raw') {
      try {
        finalConfig = JSON.parse(rawText)
      } catch (e) {
        setError('Raw configuration must be valid JSON: ' + (e as Error).message)
        return
      }
    } else {
      // Strip empty values so an untouched field is absent rather than an explicit zero.
      // Traefik distinguishes the two for several of these fields.
      finalConfig = Object.fromEntries(
        Object.entries(config).filter(([, v]) =>
          v !== '' && v !== undefined && v !== null && !(Array.isArray(v) && v.length === 0)))

      if (type === 'basicAuth') {
        const users = (config.users ?? []).filter((u: any) => u.username?.trim())
        if (users.length === 0) {
          setError('Add at least one user — a basic auth middleware with none would lock everyone out.')
          return
        }
        // A blank password means "keep the stored one", which only exists for an account
        // that is already there. A new one needs a password now.
        const known = new Set(existing?.type === 'basicAuth' ? (existing.config?.users ?? []) : [])
        const missing = users.find((u: any) => !u.password && !known.has(u.username.trim()))
        if (missing) {
          setError(`Set a password for ${missing.username}.`)
          return
        }
        finalConfig.users = users
      }
    }

    save.mutate({
      name: existing ? undefined : name,
      type,
      description: description || undefined,
      config: finalConfig,
    })
  }

  function setField(key: string, value: any) {
    setConfig(prev => ({ ...prev, [key]: value }))
  }

  return (
    <div className="fixed inset-0 bg-black/50 flex items-start justify-center overflow-y-auto py-10 z-50">
      <div className="card p-6 w-full max-w-2xl mx-4">
        <h2 className="text-base font-semibold mb-4">
          {existing ? `Edit ${existing.name}` : 'New middleware'}
        </h2>

        {!existing && (
          <div className="mb-4">
            <label className="label">Name</label>
            <input
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder="corporate-ip-allowlist"
              className="input-field w-full font-mono text-sm"
            />
            <p className="text-[10px] text-text-tertiary mt-1">
              Apps reference this name, so it cannot change later. Lowercase letters, digits and dashes.
            </p>
          </div>
        )}

        <div className="mb-4">
          <label className="label">Type</label>
          <select
            value={type}
            onChange={e => { setType(e.target.value as MiddlewareType); setConfig({}) }}
            disabled={!!existing}
            className="input-field w-full text-sm"
          >
            {(Object.keys(TYPE_LABELS) as MiddlewareType[]).map(t => (
              <option key={t} value={t}>{TYPE_LABELS[t]}</option>
            ))}
          </select>
          <p className="text-[10px] text-text-tertiary mt-1">{spec.blurb}</p>
        </div>

        <div className="mb-4">
          <label className="label">Description</label>
          <input
            value={description}
            onChange={e => setDescription(e.target.value)}
            placeholder="Office and VPN ranges only"
            className="input-field w-full text-sm"
          />
        </div>

        {type === 'raw' ? (
          <div className="mb-4">
            <label className="label">Traefik middleware spec</label>
            <textarea
              value={rawText}
              onChange={e => setRawText(e.target.value)}
              rows={10}
              className="input-field w-full font-mono text-xs"
            />
            <p className="text-[10px] text-text-tertiary mt-1">
              Exactly one top-level key naming the Traefik middleware type. Passed through untouched.
            </p>
          </div>
        ) : (
          <div className="space-y-3 mb-4">
            {type === 'basicAuth' && (
              <BasicAuthUsers
                value={config.users ?? []}
                existingUsernames={existing?.type === 'basicAuth' ? (existing.config?.users ?? []) : []}
                onChange={v => setField('users', v)}
              />
            )}
            {spec.fields.map(field => (
              <FieldInput key={field.key} field={field} value={config[field.key]} onChange={v => setField(field.key, v)} />
            ))}
          </div>
        )}

        {error && <p className="text-xs text-status-failed mb-3">{error}</p>}

        <div className="flex gap-2 justify-end">
          <button onClick={onClose} className="btn-secondary text-sm">Cancel</button>
          <button onClick={submit} disabled={save.isPending} className="btn-primary text-sm">
            {save.isPending ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    </div>
  )
}

function FieldInput({ field, value, onChange }: { field: FieldSpec; value: any; onChange: (v: any) => void }) {
  if (field.kind === 'boolean') {
    return (
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={!!value} onChange={e => onChange(e.target.checked)} />
        <span>{field.label}</span>
      </label>
    )
  }

  if (field.kind === 'list') {
    return (
      <div>
        <label className="label">{field.label}</label>
        <textarea
          value={Array.isArray(value) ? value.join('\n') : ''}
          onChange={e => onChange(e.target.value.split('\n').map(s => s.trim()).filter(Boolean))}
          placeholder={field.placeholder}
          rows={3}
          className="input-field w-full font-mono text-xs"
        />
        {field.help && <p className="text-[10px] text-text-tertiary mt-1">{field.help}</p>}
      </div>
    )
  }

  if (field.kind === 'map') {
    const entries: [string, string][] = Object.entries(value ?? {})
    return (
      <div>
        <label className="label">{field.label}</label>
        {entries.map(([k, v], i) => (
          <div key={i} className="flex gap-2 mb-1.5">
            <input
              value={k}
              onChange={e => {
                const next = Object.fromEntries(entries.map(([ek, ev], ei) => ei === i ? [e.target.value, ev] : [ek, ev]))
                onChange(next)
              }}
              placeholder="X-Header-Name"
              className="input-field flex-1 font-mono text-xs"
            />
            <input
              value={v}
              onChange={e => onChange({ ...(value ?? {}), [k]: e.target.value })}
              placeholder="value"
              className="input-field flex-1 text-xs"
            />
            <button
              onClick={() => onChange(Object.fromEntries(entries.filter((_, ei) => ei !== i)))}
              className="text-text-tertiary hover:text-status-failed text-xs px-2"
            >&times;</button>
          </div>
        ))}
        <button onClick={() => onChange({ ...(value ?? {}), '': '' })} className="text-xs text-accent hover:text-accent-glow">
          + Add
        </button>
      </div>
    )
  }

  return (
    <div>
      <label className="label">{field.label}</label>
      <input
        type={field.kind === 'number' ? 'number' : 'text'}
        value={value ?? ''}
        onChange={e => onChange(field.kind === 'number'
          ? (e.target.value === '' ? '' : Number(e.target.value))
          : e.target.value)}
        placeholder={field.placeholder}
        className="input-field w-full text-sm"
      />
      {field.help && <p className="text-[10px] text-text-tertiary mt-1">{field.help}</p>}
    </div>
  )
}


// Credentials for a basicAuth middleware. Passwords are sent once and hashed server-side;
// they are never read back, so an existing account shows a blank password field that means
// "leave this one as it is" rather than "clear it".
function BasicAuthUsers({ value, existingUsernames, onChange }: {
  value: { username: string; password: string }[]
  existingUsernames: string[]
  onChange: (v: { username: string; password: string }[]) => void
}) {
  const users = value.length > 0 ? value : [{ username: '', password: '' }]

  function update(i: number, patch: Partial<{ username: string; password: string }>) {
    onChange(users.map((u, ui) => ui === i ? { ...u, ...patch } : u))
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <label className="label">Users</label>
        <button
          type="button"
          onClick={() => onChange([...users, { username: '', password: '' }])}
          className="text-xs text-accent hover:text-accent-glow"
        >
          + Add user
        </button>
      </div>

      {users.map((user, i) => {
        const isExisting = existingUsernames.includes(user.username.trim())
        return (
          <div key={i} className="mb-2">
            <div className="flex gap-2">
              <input
                value={user.username}
                onChange={e => update(i, { username: e.target.value })}
                placeholder="username"
                className="input-field flex-1 font-mono text-xs"
                autoComplete="off"
              />
              <input
                type="password"
                value={user.password}
                onChange={e => update(i, { password: e.target.value })}
                placeholder={isExisting ? 'unchanged' : 'password'}
                className="input-field flex-1 text-xs"
                autoComplete="new-password"
              />
              <button
                type="button"
                onClick={() => onChange(users.filter((_, ui) => ui !== i))}
                className="text-text-tertiary hover:text-status-failed text-xs px-2"
              >&times;</button>
            </div>
            {isExisting && !user.password && (
              <p className="text-[10px] text-text-tertiary mt-1">
                Password unchanged. Type a new one to replace it.
              </p>
            )}
          </div>
        )
      })}

      <p className="text-[10px] text-text-tertiary mt-1">
        Hashed with bcrypt and stored in a Secret Vesta manages. Removing a user here
        revokes their access on the next save.
      </p>
    </div>
  )
}
