import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type Addon, type AddonType } from '../lib/api'
import CopyEnvButton from './CopyEnvButton'

/**
 * Managed datastores for a project.
 *
 * `spec.addons` has existed on apps for a long time and was reconciled by nothing, so
 * declaring an add-on did nothing at all. This is the surface for the kind that now runs
 * them.
 */

const ENGINES: Record<AddonType, { label: string; blurb: string; versions: string[] }> = {
  postgres: {
    label: 'PostgreSQL',
    blurb: 'Relational. The default choice for most applications.',
    versions: ['16', '15', '14'],
  },
  mysql: {
    label: 'MySQL',
    blurb: 'Relational.',
    versions: ['8'],
  },
  redis: {
    label: 'Redis',
    blurb: 'In-memory store, for caching and queues. Persists to disk.',
    versions: ['7', '6'],
  },
  mongodb: {
    label: 'MongoDB',
    blurb: 'Document store.',
    versions: ['7', '6'],
  },
}

export default function AddonsSection({
  projectId, environments,
}: {
  projectId: string
  environments: string[]
}) {
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null)

  const { data, isLoading, error } = useQuery({
    queryKey: ['addons', projectId],
    queryFn: () => api.listAddons(projectId),
    // A datastore becomes ready on its own schedule, with nothing else changing to say so.
    refetchInterval: 15_000,
  })

  const remove = useMutation({
    mutationFn: (name: string) => api.deleteAddon(projectId, name),
    onSuccess: () => {
      setConfirmDelete(null)
      queryClient.invalidateQueries({ queryKey: ['addons', projectId] })
    },
  })

  const addons = data?.items ?? []

  return (
    <section className="card p-6">
      <div className="flex items-baseline justify-between mb-1">
        <h3 className="section-title">Add-ons</h3>
        <button onClick={() => setCreating(v => !v)} className="text-xs text-accent hover:text-accent-glow">
          {creating ? 'Cancel' : '+ Add a datastore'}
        </button>
      </div>
      <p className="text-xs text-text-tertiary mb-5">
        Databases and caches Vesta runs for this project. Each gets its own volume and
        credentials; binding one to an app injects its connection details as environment
        variables.
      </p>

      {creating && (
        <CreateAddonForm
          projectId={projectId}
          environments={environments}
          onDone={() => {
            setCreating(false)
            queryClient.invalidateQueries({ queryKey: ['addons', projectId] })
          }}
        />
      )}

      {isLoading && <p className="text-xs text-text-tertiary">Loading…</p>}
      {error && <p className="text-xs text-status-failed">{(error as Error).message}</p>}

      {!isLoading && !error && addons.length === 0 && !creating && (
        <p className="text-xs text-text-tertiary">
          No add-ons yet. One is created per environment unless you pick a single one, so
          staging data never shares a database with production.
        </p>
      )}

      <div className="space-y-3">
        {addons.map(a => (
          <AddonRow
            key={a.name}
            addon={a}
            projectId={projectId}
            environments={environments}
            confirming={confirmDelete === a.name}
            onConfirm={() => setConfirmDelete(a.name)}
            onCancel={() => setConfirmDelete(null)}
            onDelete={() => remove.mutate(a.name)}
            deleting={remove.isPending}
          />
        ))}
      </div>

      {remove.isError && (
        <p className="text-xs text-status-failed mt-3">{(remove.error as Error).message}</p>
      )}
    </section>
  )
}

function AddonRow({
  addon, projectId, environments, confirming, onConfirm, onCancel, onDelete, deleting,
}: {
  addon: Addon
  projectId: string
  environments: string[]
  confirming: boolean
  onConfirm: () => void
  onCancel: () => void
  onDelete: () => void
  deleting: boolean
}) {
  const [showFor, setShowFor] = useState<string | null>(null)

  const { data: creds } = useQuery({
    queryKey: ['addon-credentials', projectId, addon.name, showFor],
    queryFn: () => api.getAddonCredentials(projectId, addon.name, showFor!),
    enabled: !!showFor,
  })

  // Which environments this add-on actually runs in: one named, or all of them.
  const targets = addon.environment ? [addon.environment] : environments

  return (
    <div className="rounded-lg border border-border bg-surface-1 p-4">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="font-mono text-xs text-text-primary">{addon.name}</span>
            <span className="chip">{ENGINES[addon.type]?.label ?? addon.type}</span>
            {addon.version && <span className="chip">{addon.version}</span>}
            <span className="chip">{addon.environment || 'all environments'}</span>
            <span
              className={`text-[11px] font-mono ${addon.ready ? 'text-status-running' : 'text-status-pending'}`}
            >
              {addon.ready ? 'ready' : (addon.phase?.toLowerCase() || 'starting')}
            </span>
          </div>
          {addon.reason && (
            <p className="text-[11px] text-text-tertiary mt-1">{addon.reason}</p>
          )}
        </div>

        <div className="flex items-center gap-3 shrink-0">
          {confirming ? (
            <>
              <button onClick={onDelete} disabled={deleting} className="text-xs text-status-failed hover:underline">
                {deleting ? 'Deleting…' : 'Confirm'}
              </button>
              <button onClick={onCancel} className="text-xs text-text-tertiary hover:text-text-secondary">
                Cancel
              </button>
            </>
          ) : (
            <button onClick={onConfirm} className="text-xs text-text-tertiary hover:text-status-failed">
              Delete
            </button>
          )}
        </div>
      </div>

      {confirming && (
        <p className="text-[11px] text-text-tertiary mt-3">
          The datastore stops and apps bound to it lose their database. The volume is kept
          unless this add-on was created with a Delete policy, so the data can be recovered —
          but nothing will be serving it.
        </p>
      )}

      <div className="mt-3 flex flex-wrap items-center gap-3">
        {targets.map(env => (
          <button
            key={env}
            onClick={() => setShowFor(showFor === env ? null : env)}
            className="text-[11px] text-accent hover:text-accent-glow"
          >
            {showFor === env ? `Hide ${env} credentials` : `Show ${env} credentials`}
          </button>
        ))}
      </div>

      {showFor && creds && (
        <div className="mt-3 rounded-md border border-border bg-surface-2 p-3">
          <div className="flex items-center justify-between mb-2">
            <span className="text-[11px] text-text-tertiary font-mono">{showFor}</span>
            <CopyEnvButton values={creds.credentials} />
          </div>
          <div className="space-y-0.5 max-h-48 overflow-y-auto">
            {Object.entries(creds.credentials).map(([k, v]) => (
              <div key={k} className="flex gap-2 text-[11px] font-mono">
                <span className="text-text-tertiary shrink-0">{k}</span>
                <span className="text-text-secondary break-all">{v}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function CreateAddonForm({
  projectId, environments, onDone,
}: {
  projectId: string
  environments: string[]
  onDone: () => void
}) {
  const [name, setName] = useState('')
  const [type, setType] = useState<AddonType>('postgres')
  const [version, setVersion] = useState('')
  const [environment, setEnvironment] = useState('')
  const [storage, setStorage] = useState('10Gi')
  const [deletionPolicy, setDeletionPolicy] = useState('Retain')

  const create = useMutation({
    mutationFn: () => api.createAddon(projectId, {
      name, type,
      version: version || undefined,
      environment: environment || undefined,
      storage: storage || undefined,
      deletionPolicy,
    }),
    onSuccess: onDone,
  })

  return (
    <form
      onSubmit={e => { e.preventDefault(); create.mutate() }}
      className="rounded-lg border border-border bg-surface-1 p-4 mb-4 space-y-3"
    >
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="text-xs text-text-tertiary mb-1 block">Name</label>
          <input
            value={name}
            onChange={e => setName(e.target.value)}
            className="input-field font-mono text-xs w-full"
            placeholder="db"
            required
          />
        </div>
        <div>
          <label className="text-xs text-text-tertiary mb-1 block">Engine</label>
          <select
            value={type}
            onChange={e => { setType(e.target.value as AddonType); setVersion('') }}
            className="input-field text-xs w-full"
          >
            {(Object.keys(ENGINES) as AddonType[]).map(k => (
              <option key={k} value={k}>{ENGINES[k].label}</option>
            ))}
          </select>
          <p className="text-[11px] text-text-tertiary mt-1">{ENGINES[type].blurb}</p>
        </div>
      </div>

      <div className="grid grid-cols-3 gap-3">
        <div>
          <label className="text-xs text-text-tertiary mb-1 block">Version</label>
          <select value={version} onChange={e => setVersion(e.target.value)} className="input-field text-xs w-full">
            <option value="">Default ({ENGINES[type].versions[0]})</option>
            {ENGINES[type].versions.map(v => <option key={v} value={v}>{v}</option>)}
          </select>
        </div>
        <div>
          <label className="text-xs text-text-tertiary mb-1 block">Environment</label>
          <select value={environment} onChange={e => setEnvironment(e.target.value)} className="input-field text-xs w-full">
            <option value="">One per environment</option>
            {environments.map(e => <option key={e} value={e}>{e} only</option>)}
          </select>
          <p className="text-[11px] text-text-tertiary mt-1">
            One each keeps staging data out of production.
          </p>
        </div>
        <div>
          <label className="text-xs text-text-tertiary mb-1 block">Storage</label>
          <input
            value={storage}
            onChange={e => setStorage(e.target.value)}
            className="input-field font-mono text-xs w-full"
            placeholder="10Gi"
          />
        </div>
      </div>

      <div>
        <label className="text-xs text-text-tertiary mb-1 block">When deleted</label>
        <select value={deletionPolicy} onChange={e => setDeletionPolicy(e.target.value)} className="input-field text-xs w-full max-w-md">
          <option value="Retain">Keep the volume — data can be recovered</option>
          <option value="Delete">Delete the volume and its data</option>
        </select>
      </div>

      <div className="flex gap-3 pt-1">
        <button type="submit" disabled={create.isPending || !name} className="btn-primary text-xs">
          {create.isPending ? 'Creating…' : 'Create'}
        </button>
        <button type="button" onClick={onDone} className="btn-ghost text-xs">Cancel</button>
      </div>

      {create.isError && (
        <p className="text-xs text-status-failed">{(create.error as Error).message}</p>
      )}
    </form>
  )
}
