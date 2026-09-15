import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type LogDrain, type LogDrainType, type LogDrainPayload } from '../lib/api'

type FieldKind = 'text' | 'number' | 'boolean' | 'map' | 'secret'

interface FieldSpec {
  /** For secret fields this is the Secret key, which is also the env-var suffix the
   *  generated collector config references. For others it is the config field. */
  key: string
  label: string
  kind: FieldKind
  help?: string
  placeholder?: string
}

// Each destination described as data. The field names are Traefik's -- sorry, Fluent Bit's
// -- own, kept verbatim so that searching its documentation when a drain misbehaves
// actually finds the setting.
const TYPE_FIELDS: Record<LogDrainType, { label: string; blurb: string; fields: FieldSpec[] }> = {
  http: {
    label: 'HTTP endpoint',
    blurb: 'POSTs batches of JSON. Works with most SaaS log services and anything custom.',
    fields: [
      { key: 'uri', label: 'URL', kind: 'text', placeholder: 'https://logs.example.com/ingest' },
      { key: 'AUTH', label: 'Authorization header', kind: 'secret', help: 'Sent verbatim as the Authorization header, e.g. "Bearer …". Stored in a Secret.' },
      { key: 'format', label: 'Format', kind: 'text', placeholder: 'json' },
      { key: 'dateKey', label: 'Timestamp field', kind: 'text', placeholder: 'timestamp', help: 'The field the destination reads event time from. Leave blank unless it expects its own — a mismatch is accepted silently and every record gets its ingestion time instead.' },
      { key: 'headers', label: 'Extra headers', kind: 'map' },
    ],
  },
  loki: {
    label: 'Loki',
    blurb: 'Grafana Loki. Logs land beside the metrics you already have in Grafana.',
    fields: [
      { key: 'host', label: 'Host', kind: 'text', placeholder: 'loki.monitoring.svc' },
      { key: 'port', label: 'Port', kind: 'number', placeholder: '3100' },
      { key: 'USER', label: 'Username', kind: 'secret' },
      { key: 'PASSWORD', label: 'Password', kind: 'secret' },
      { key: 'tenantId', label: 'Tenant ID', kind: 'text' },
      { key: 'labels', label: 'Extra stream labels', kind: 'map', help: 'project, namespace and app are always added.' },
    ],
  },
  syslog: {
    label: 'Syslog',
    blurb: 'RFC5424 over TCP or TLS. What most on-premise aggregators accept.',
    fields: [
      { key: 'host', label: 'Host', kind: 'text' },
      { key: 'port', label: 'Port', kind: 'number', placeholder: '514' },
      { key: 'mode', label: 'Mode', kind: 'text', placeholder: 'tcp', help: 'tcp, udp or tls.' },
      { key: 'format', label: 'Format', kind: 'text', placeholder: 'rfc5424' },
    ],
  },
  elasticsearch: {
    label: 'Elasticsearch / OpenSearch',
    blurb: 'Indexes each record. Compatible with Elasticsearch 8 by default.',
    fields: [
      { key: 'host', label: 'Host', kind: 'text' },
      { key: 'port', label: 'Port', kind: 'number', placeholder: '9200' },
      { key: 'index', label: 'Index', kind: 'text', placeholder: 'vesta' },
      { key: 'USER', label: 'Username', kind: 'secret' },
      { key: 'PASSWORD', label: 'Password', kind: 'secret' },
      { key: 'logstashFormat', label: 'Dated indices (Logstash format)', kind: 'boolean' },
    ],
  },
  datadog: {
    label: 'Datadog',
    blurb: 'Datadog Logs.',
    fields: [
      { key: 'API_KEY', label: 'API key', kind: 'secret', help: 'Stored in a Secret, never on the drain.' },
      { key: 'site', label: 'Site', kind: 'text', placeholder: 'datadoghq.com', help: 'Use datadoghq.eu for EU accounts — the wrong site is accepted and lands elsewhere.' },
      { key: 'service', label: 'Service', kind: 'text' },
      { key: 'tags', label: 'Extra tags', kind: 'map' },
    ],
  },
  openobserve: {
    label: 'OpenObserve',
    blurb: 'OpenObserve\u2019s JSON ingest API. The endpoint path and timestamp field are filled in for you.',
    fields: [
      { key: 'endpoint', label: 'Endpoint', kind: 'text', placeholder: 'https://openobserve.example.com', help: 'Base URL only \u2014 no path. The ingest path is built from the organization and stream below.' },
      { key: 'organization', label: 'Organization', kind: 'text', placeholder: 'default' },
      { key: 'stream', label: 'Stream', kind: 'text', placeholder: 'vesta', help: 'Created on first write.' },
      { key: 'USER', label: 'Email', kind: 'secret', help: 'The account OpenObserve authenticates as.' },
      { key: 'PASSWORD', label: 'Password', kind: 'secret', help: 'Stored in a Secret; the collector handles the encoding.' },
    ],
  },
  forward: {
    label: 'Fluent Bit / Fluentd (forward)',
    blurb: 'Sends to an aggregator you already run, which keeps owning where logs finally go. Records arrive already tagged with project, namespace and app.',
    fields: [
      { key: 'host', label: 'Host', kind: 'text', placeholder: 'fluentd.logging.svc' },
      { key: 'port', label: 'Port', kind: 'number', placeholder: '24224' },
      { key: 'SHARED_KEY', label: 'Shared key', kind: 'secret', help: 'Enables the handshake. Without it the aggregator accepts records from anything that can reach the port.' },
      { key: 'tls', label: 'TLS', kind: 'boolean' },
    ],
  },
  s3: {
    label: 'S3',
    blurb: 'Batches to object storage. Cheapest long-term retention.',
    fields: [
      { key: 'bucket', label: 'Bucket', kind: 'text' },
      { key: 'region', label: 'Region', kind: 'text', placeholder: 'eu-west-1' },
      { key: 'AWS_ACCESS_KEY_ID', label: 'Access key ID', kind: 'secret', help: 'Leave both empty to use the node role or IRSA.' },
      { key: 'AWS_SECRET_ACCESS_KEY', label: 'Secret access key', kind: 'secret' },
      { key: 'endpoint', label: 'Endpoint', kind: 'text', help: 'For S3-compatible storage.' },
      { key: 'totalFileSize', label: 'File size before upload', kind: 'text', placeholder: '50M' },
    ],
  },
}

export default function LogDrainsPage() {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<LogDrain | null>(null)
  const [creating, setCreating] = useState(false)

  const { data, isLoading } = useQuery({
    queryKey: ['logDrains'],
    queryFn: () => api.listLogDrains(),
    // Delivery counters move without anything in the cluster changing.
    refetchInterval: 30_000,
  })

  const remove = useMutation({
    mutationFn: (name: string) => api.deleteLogDrain(name),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['logDrains'] }),
  })

  const drains = data?.drains ?? []

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-4">
        <p className="text-sm text-text-secondary">
          {drains.length} log drain{drains.length !== 1 ? 's' : ''}
        </p>
        <button onClick={() => { setCreating(true); setEditing(null) }} className="btn-primary whitespace-nowrap">
          <span className="flex items-center gap-2">
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
            </svg>
            New Log Drain
          </span>
        </button>
      </div>

      {isLoading && <p className="text-sm text-text-tertiary">Loading…</p>}

      {!isLoading && drains.length === 0 && !creating && (
        <div className="card p-8 text-center">
          <p className="text-sm text-text-secondary">No log drains yet.</p>
          <p className="text-xs text-text-tertiary mt-2 max-w-md mx-auto leading-relaxed">
            Pod logs are read on demand and not retained — when a pod is replaced its logs are
            gone. A drain ships them somewhere that keeps them. Scope it to everything, to one
            project, or to a single app.
          </p>
        </div>
      )}

      <div className="space-y-2">
        {drains.map(drain => (
          <div key={drain.name} className="card p-4">
            <div className="flex items-start justify-between gap-4">
              <div className="min-w-0">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="font-mono text-sm">{drain.name}</span>
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-surface-hover text-text-tertiary">
                    {TYPE_FIELDS[drain.type]?.label ?? drain.type}
                  </span>
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-surface-hover text-text-tertiary">
                    {drain.scope ?? scopeLabel(drain)}
                  </span>
                  {!drain.enabled && (
                    <span className="text-[10px] px-1.5 py-0.5 rounded bg-surface-hover text-text-tertiary">disabled</span>
                  )}
                </div>
                {drain.description && <p className="text-xs text-text-tertiary mt-1">{drain.description}</p>}

                {/* Delivery counts, not just configuration. A drain that is configured and
                    not delivering looks identical to an app that logged nothing otherwise. */}
                <p className="text-[11px] text-text-tertiary mt-1.5">
                  {drain.recordsDelivered > 0
                    ? `${drain.recordsDelivered.toLocaleString()} records delivered`
                    : 'No records delivered yet'}
                  {drain.errors > 0 && (
                    <span className="text-status-failed"> · {drain.errors.toLocaleString()} errors</span>
                  )}
                </p>
                {!drain.ready && drain.reason && (
                  <p className="text-[11px] text-status-failed mt-1">{drain.reason}</p>
                )}
              </div>
              <div className="flex gap-2 shrink-0">
                <button onClick={() => { setEditing(drain); setCreating(false) }} className="text-xs text-accent hover:text-accent-glow">
                  Edit
                </button>
                <button
                  onClick={() => { if (confirm(`Delete ${drain.name}?`)) remove.mutate(drain.name) }}
                  className="text-xs text-text-tertiary hover:text-status-failed"
                >
                  Delete
                </button>
              </div>
            </div>
          </div>
        ))}
      </div>

      {remove.isError && <p className="text-xs text-status-failed">{(remove.error as Error).message}</p>}

      {(creating || editing) && (
        <LogDrainForm
          existing={editing}
          onClose={() => { setCreating(false); setEditing(null) }}
          onSaved={() => {
            queryClient.invalidateQueries({ queryKey: ['logDrains'] })
            setCreating(false); setEditing(null)
          }}
        />
      )}
    </div>
  )
}

function scopeLabel(drain: LogDrain): string {
  // Both forms, unioned, the same way the operator reads them. A drain that names several
  // projects would otherwise read as "all apps" here, which is the one description that is
  // certainly wrong for a drain that named projects explicitly.
  const projects = [...new Set([...(drain.project ? [drain.project] : []), ...(drain.projects ?? [])])]
  // Named up to three, then counted: a drain covering a dozen projects should not push the
  // rest of the row off the screen.
  const list = projects.length > 3
    ? `${projects.slice(0, 3).join(', ')} +${projects.length - 3} more`
    : projects.join(', ')

  if (drain.app && drain.environment) return `${drain.app} in ${list}/${drain.environment}`
  if (drain.app) return `app ${drain.app}`
  if (drain.environment && projects.length > 0) return `${list}/${drain.environment}`
  if (drain.environment) return `environment ${drain.environment}`
  if (projects.length === 1) return `project ${list}`
  if (projects.length > 1) return `projects ${list}`
  return 'all apps'
}

function LogDrainForm({ existing, onClose, onSaved }: {
  existing: LogDrain | null
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(existing?.name ?? '')
  const [type, setType] = useState<LogDrainType>(existing?.type ?? 'http')
  const [description, setDescription] = useState(existing?.description ?? '')
  // A drain usually belongs to a team rather than to one project. Covering several used to
  // mean several drains pointed at the same destination, each with its own credentials to
  // rotate and its own status to read.
  //
  // Seeded from the singular field as well, so editing a drain written before this does not
  // quietly drop the project it already had.
  const [projects, setProjects] = useState<string[]>(() => {
    const many = existing?.projects ?? []
    if (many.length > 0) return many
    return existing?.project ? [existing.project] : []
  })
  // Environments and apps are listed from the first selected project. Environment names are
  // shared across projects in practice, and the value applies to every project selected.
  const project = projects[0] ?? ''
  const [environment, setEnvironment] = useState(existing?.environment ?? '')
  const [app, setApp] = useState(existing?.app ?? '')
  const [excludeApps, setExcludeApps] = useState<string[]>(existing?.excludeApps ?? [])
  const [config, setConfig] = useState<Record<string, any>>(existing?.config ?? {})
  const [credentials, setCredentials] = useState<Record<string, string>>({})
  const [error, setError] = useState('')

  const { data: allProjects } = useQuery({ queryKey: ['projects'], queryFn: () => api.listProjects() })

  // Environments belong to a project, so they can only be offered once one is chosen.
  // Typing a free-form name here produced a drain that matched no namespace and shipped
  // nothing, with nothing saying so.
  const { data: environments } = useQuery({
    queryKey: ['environments', project],
    queryFn: () => api.listEnvironments(project),
    enabled: !!project,
  })

  const { data: apps } = useQuery({
    queryKey: ['apps', project],
    queryFn: () => api.listApps(project ? { project } : undefined),
  })

  const save = useMutation({
    mutationFn: (payload: LogDrainPayload) =>
      existing ? api.updateLogDrain(existing.name, payload) : api.createLogDrain(payload),
    onSuccess: onSaved,
    onError: (e: Error) => setError(e.message),
  })

  const spec = TYPE_FIELDS[type]

  function submit() {
    setError('')
    if (environment && !project) {
      setError('Pick a project before narrowing to an environment.')
      return
    }

    // Credential fields live in `credentials`, never in `config` -- the server strips them
    // from config regardless, but sending them there would put them in a request body that
    // looks like ordinary configuration.
    const secretKeys = new Set(spec.fields.filter(f => f.kind === 'secret').map(f => f.key))
    const plainConfig = Object.fromEntries(
      Object.entries(config).filter(([k, v]) =>
        !secretKeys.has(k) && v !== '' && v !== undefined && v !== null))

    save.mutate({
      name: existing ? undefined : name,
      type,
      description: description || undefined,
      // Only the list is sent. The singular field stays readable for drains written before
      // it existed -- the form seeds from it -- but writing both would leave two places
      // saying what the scope is, and they could disagree on the next edit.
      project: undefined,
      projects: projects.length > 0 ? projects : undefined,
      environment: environment || undefined,
      app: app || undefined,
      excludeApps: excludeApps.length > 0 ? excludeApps : undefined,
      config: plainConfig,
      credentials: Object.keys(credentials).length > 0 ? credentials : undefined,
    })
  }

  return (
    <div className="fixed inset-0 bg-black/50 flex items-start justify-center overflow-y-auto py-10 z-50">
      <div className="card p-6 w-full max-w-2xl mx-4">
        <h2 className="text-base font-semibold mb-4">
          {existing ? `Edit ${existing.name}` : 'New log drain'}
        </h2>

        {!existing && (
          <div className="mb-4">
            <label className="label">Name</label>
            <input value={name} onChange={e => setName(e.target.value)}
              placeholder="central-logging" className="input-field w-full font-mono text-sm" />
          </div>
        )}

        <div className="mb-4">
          <label className="label">Destination</label>
          <select value={type} onChange={e => { setType(e.target.value as LogDrainType); setConfig({}); setCredentials({}) }}
            disabled={!!existing} className="input-field w-full text-sm">
            {(Object.keys(TYPE_FIELDS) as LogDrainType[]).map(t => (
              <option key={t} value={t}>{TYPE_FIELDS[t].label}</option>
            ))}
          </select>
          <p className="text-[10px] text-text-tertiary mt-1">{spec.blurb}</p>
        </div>

        <div className="mb-4">
          <label className="label">Which apps ship here</label>
          <div className="flex gap-2">
            <select
              value=""
              onChange={e => {
                const chosen = e.target.value
                if (!chosen) return
                setProjects(prev => prev.includes(chosen) ? prev : [...prev, chosen])
                setEnvironment(''); setApp(''); setExcludeApps([])
              }}
              className="input-field flex-1 text-sm"
            >
              <option value="">{projects.length === 0 ? 'All projects' : '+ Add a project'}</option>
              {allProjects?.items
                ?.filter((p: any) => !projects.includes(p.name))
                .map((p: any) => <option key={p.name} value={p.name}>{p.name}</option>)}
            </select>
            <select value={environment} onChange={e => setEnvironment(e.target.value)}
              disabled={!project} className="input-field flex-1 text-sm">
              <option value="">{project ? 'All environments' : 'Pick a project first'}</option>
              {environments?.items?.map((e: any) => <option key={e.name} value={e.name}>{e.name}</option>)}
            </select>
            <select value={app} onChange={e => setApp(e.target.value)} className="input-field flex-1 text-sm">
              <option value="">All apps</option>
              {apps?.items?.map((a: any) => <option key={a.name} value={a.name}>{a.name}</option>)}
            </select>
          </div>
          {projects.length > 0 && (
            <div className="flex flex-wrap gap-2 mt-2">
              {projects.map(name => (
                <span key={name} className="inline-flex items-center gap-1.5 px-2.5 py-1 bg-surface-2 border border-border rounded-lg text-xs font-mono text-text-secondary">
                  {name}
                  <button
                    type="button"
                    onClick={() => {
                      const next = projects.filter(p => p !== name)
                      setProjects(next)
                      if (next.length === 0) { setEnvironment(''); setApp(''); setExcludeApps([]) }
                    }}
                    className="text-text-tertiary hover:text-status-failed"
                  >&times;</button>
                </span>
              ))}
            </div>
          )}
          <p className="text-[10px] text-text-tertiary mt-1">
            Leave blank for everything. Scope is the attachment — an app ships to every drain
            whose scope covers it, so a project drain adds to the platform one rather than
            replacing it.
            {projects.length > 1 && ' The environment applies to each project selected.'}
          </p>
        </div>

        {!app && (
          <div className="mb-4">
            <label className="label">Except these apps</label>
            <div className="flex flex-wrap gap-1.5 mb-2">
              {excludeApps.map(name => (
                <span key={name} className="text-[11px] font-mono px-2 py-1 rounded bg-surface-hover flex items-center gap-1.5">
                  {name}
                  <button type="button" onClick={() => setExcludeApps(excludeApps.filter(n => n !== name))}
                    className="text-text-tertiary hover:text-status-failed">&times;</button>
                </span>
              ))}
              {excludeApps.length === 0 && (
                <span className="text-xs text-text-tertiary">None — every app in scope ships here.</span>
              )}
            </div>
            <select value="" className="input-field text-xs w-full"
              onChange={e => {
                if (e.target.value && !excludeApps.includes(e.target.value)) {
                  setExcludeApps([...excludeApps, e.target.value])
                }
              }}>
              <option value="">+ Exempt an app…</option>
              {apps?.items
                ?.filter((a: any) => !excludeApps.includes(a.name))
                .map((a: any) => <option key={a.name} value={a.name}>{a.name}</option>)}
            </select>
            <p className="text-[10px] text-text-tertiary mt-1">
              For the app that is too noisy, or whose logs should not leave the cluster.
              Everything else in scope still ships.
            </p>
          </div>
        )}

        <div className="mb-4">
          <label className="label">Description</label>
          <input value={description} onChange={e => setDescription(e.target.value)}
            placeholder="Long-term retention for audit" className="input-field w-full text-sm" />
        </div>

        <div className="space-y-3 mb-4">
          {spec.fields.map(field => (
            <DrainField
              key={field.key}
              field={field}
              value={field.kind === 'secret' ? credentials[field.key] : config[field.key]}
              hasStoredSecret={field.kind === 'secret' && hasStoredCredential(existing, type, field.key)}
              onChange={v => field.kind === 'secret'
                ? setCredentials(prev => ({ ...prev, [field.key]: v }))
                : setConfig(prev => ({ ...prev, [field.key]: v }))}
            />
          ))}
        </div>

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

function DrainField({ field, value, hasStoredSecret, onChange }: {
  field: FieldSpec
  value: any
  hasStoredSecret: boolean
  onChange: (v: any) => void
}) {
  if (field.kind === 'secret') {
    return (
      <div>
        <label className="label">{field.label}</label>
        <input
          type="password"
          value={value ?? ''}
          onChange={e => onChange(e.target.value)}
          placeholder={hasStoredSecret ? 'unchanged' : ''}
          autoComplete="new-password"
          className="input-field w-full text-sm"
        />
        <p className="text-[10px] text-text-tertiary mt-1">
          {hasStoredSecret
            ? 'Stored and not readable. Type a new value to replace it.'
            : field.help ?? 'Stored in a Secret, never on the drain.'}
        </p>
      </div>
    )
  }

  if (field.kind === 'boolean') {
    return (
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={!!value} onChange={e => onChange(e.target.checked)} />
        <span>{field.label}</span>
      </label>
    )
  }

  if (field.kind === 'map') {
    const entries: [string, string][] = Object.entries(value ?? {})
    return (
      <div>
        <label className="label">{field.label}</label>
        {entries.map(([k, v], i) => (
          <div key={i} className="flex gap-2 mb-1.5">
            <input value={k} placeholder="key" className="input-field flex-1 font-mono text-xs"
              onChange={e => onChange(Object.fromEntries(
                entries.map(([ek, ev], ei) => ei === i ? [e.target.value, ev] : [ek, ev])))} />
            <input value={v} placeholder="value" className="input-field flex-1 text-xs"
              onChange={e => onChange({ ...(value ?? {}), [k]: e.target.value })} />
            <button onClick={() => onChange(Object.fromEntries(entries.filter((_, ei) => ei !== i)))}
              className="text-text-tertiary hover:text-status-failed text-xs px-2">&times;</button>
          </div>
        ))}
        <button onClick={() => onChange({ ...(value ?? {}), '': '' })}
          className="text-xs text-accent hover:text-accent-glow">+ Add</button>
        {field.help && <p className="text-[10px] text-text-tertiary mt-1">{field.help}</p>}
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


// Which config field records that a given Secret key is stored. The API sets one reference
// per credential, keyed by config field, so a Secret key has to map back to it before the
// form can say "unchanged".
const CREDENTIAL_CONFIG_FIELD: Record<string, Record<string, string>> = {
  http: { AUTH: 'authHeader' },
  loki: { USER: 'basicAuth', PASSWORD: 'basicAuth' },
  elasticsearch: { USER: 'basicAuth', PASSWORD: 'basicAuth', CLOUD_ID: 'cloudId' },
  datadog: { API_KEY: 'apiKey' },
  s3: { AWS_ACCESS_KEY_ID: 'credentials', AWS_SECRET_ACCESS_KEY: 'credentials' },
  openobserve: { USER: 'credentials', PASSWORD: 'credentials' },
  forward: { SHARED_KEY: 'sharedKey' },
  syslog: {},
}

function hasStoredCredential(existing: LogDrain | null, type: LogDrainType, secretKey: string): boolean {
  if (!existing) return false
  const field = CREDENTIAL_CONFIG_FIELD[type]?.[secretKey]
  return !!field && !!existing.config?.[field]
}
