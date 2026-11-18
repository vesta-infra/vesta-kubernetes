import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type EnvironmentQuota } from '../lib/api'

/**
 * Resource quotas for an environment.
 *
 * The hazard is not the quota, it is when it takes effect. Kubernetes never applies a
 * ResourceQuota retroactively -- pods that already exist keep running and the quota refuses
 * the NEXT admission. So a quota set too low is completely silent until somebody deploys,
 * and then it fails in the middle of their rollout and looks like a platform fault.
 *
 * This screen exists to move that discovery forward: the operator reports what is already
 * committed on every pass, enforced or not, so the answer is visible before anyone commits
 * to it. Setting a quota below what is committed is allowed -- lowering one deliberately and
 * then scaling down to fit is reasonable -- but it is never allowed to be a surprise.
 */

const FIELDS = [
  { key: 'requestsCpu', label: 'CPU requests', placeholder: '4', hint: 'cores, or 500m' },
  { key: 'requestsMemory', label: 'Memory requests', placeholder: '8Gi', hint: '' },
  { key: 'limitsCpu', label: 'CPU limits', placeholder: '8', hint: '' },
  { key: 'limitsMemory', label: 'Memory limits', placeholder: '16Gi', hint: '' },
  { key: 'storageTotal', label: 'Storage', placeholder: '100Gi', hint: '' },
] as const

type FieldKey = typeof FIELDS[number]['key']

function EnvironmentQuotaCard({ projectId, environment }: { projectId: string; environment: string }) {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [enforce, setEnforce] = useState(false)

  const { data, isLoading } = useQuery<EnvironmentQuota>({
    queryKey: ['quota', projectId, environment],
    queryFn: () => api.getEnvironmentQuota(projectId, environment),
  })

  const save = useMutation({
    mutationFn: () => {
      const quota: Record<string, unknown> = { enforce }
      for (const f of FIELDS) {
        const v = (draft[f.key] ?? '').trim()
        if (v) quota[f.key] = v
      }
      return api.setEnvironmentQuota(projectId, environment, quota)
    },
    onSuccess: () => {
      setEditing(false)
      queryClient.invalidateQueries({ queryKey: ['quota', projectId, environment] })
    },
  })

  function beginEditing() {
    const q = data?.quota ?? {}
    setDraft(Object.fromEntries(FIELDS.map(f => [f.key, String(q[f.key as FieldKey] ?? '')])))
    setEnforce(q.enforce ?? false)
    setEditing(true)
  }

  const quota = data?.quota
  const status = data?.status
  const configured = quota && FIELDS.some(f => quota[f.key as FieldKey])

  if (isLoading) return null

  return (
    <div className="border border-border rounded-lg p-4">
      <div className="flex items-baseline justify-between mb-3">
        <div className="flex items-center gap-2">
          <span className="text-sm text-text-primary font-mono">{environment}</span>
          {configured && (
            <span
              className={`text-[10px] px-1.5 py-0.5 rounded ${
                status?.enforced
                  ? 'text-status-running bg-status-running-bg'
                  : 'text-text-tertiary bg-surface-3'
              }`}
            >
              {status?.enforced ? 'enforced' : 'not enforced'}
            </span>
          )}
        </div>
        {!editing && (
          <button onClick={beginEditing} className="text-xs text-accent hover:text-accent-glow">
            {configured ? 'Edit' : 'Set a quota'}
          </button>
        )}
      </div>

      {/* Committed is shown whether or not a quota exists, because it is the number you need
          in order to choose one. */}
      {status?.committed && Object.keys(status.committed).length > 0 && (
        <div className="flex flex-wrap gap-x-5 gap-y-1 mb-3 text-[11px]">
          {Object.entries(status.committed).map(([name, value]) => (
            <span key={name} className="text-text-tertiary">
              {name} committed <span className="font-mono text-text-secondary">{value}</span>
            </span>
          ))}
        </div>
      )}

      {status?.wouldExceed && (
        <div className="mb-3 px-3 py-2 rounded border border-status-degraded/40 bg-status-degraded-bg">
          <p className="text-[11px] text-status-degraded">
            This quota is below what the environment has already committed, so it is not being
            enforced. Kubernetes would not stop the running pods &mdash; it would refuse the
            next deploy. {status.reason}
          </p>
        </div>
      )}

      {editing ? (
        <form
          onSubmit={e => {
            e.preventDefault()
            save.mutate()
          }}
          className="space-y-3"
        >
          <div className="grid grid-cols-2 gap-3">
            {FIELDS.map(f => (
              <div key={f.key}>
                <label className="block text-[10px] uppercase tracking-wide text-text-tertiary mb-1">
                  {f.label}
                </label>
                <input
                  value={draft[f.key] ?? ''}
                  onChange={e => setDraft(d => ({ ...d, [f.key]: e.target.value }))}
                  placeholder={f.placeholder}
                  className="input-field font-mono text-xs w-full py-1.5"
                />
              </div>
            ))}
          </div>

          <label className="flex items-start gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={enforce}
              onChange={e => setEnforce(e.target.checked)}
              className="mt-0.5"
            />
            <span className="text-xs text-text-secondary">
              Enforce this quota
              <span className="block text-[10px] text-text-tertiary">
                Off, the numbers are recorded and reported but nothing is refused &mdash; which
                is the way to find out what an environment actually needs before constraining
                it. Enforcing also installs default requests and limits, so pods created
                outside Vesta are still admitted.
              </span>
            </span>
          </label>

          {save.error && (
            <p className="text-xs text-status-failed">{(save.error as Error).message}</p>
          )}

          <div className="flex items-center gap-2">
            <button type="submit" disabled={save.isPending} className="btn-primary text-xs">
              {save.isPending ? 'Saving…' : 'Save'}
            </button>
            <button type="button" onClick={() => setEditing(false)} className="btn-ghost text-xs">
              Cancel
            </button>
          </div>
        </form>
      ) : configured ? (
        <div className="flex flex-wrap gap-x-5 gap-y-1 text-[11px]">
          {FIELDS.filter(f => quota?.[f.key as FieldKey]).map(f => (
            <span key={f.key} className="text-text-tertiary">
              {f.label} <span className="font-mono text-text-primary">{quota?.[f.key as FieldKey]}</span>
            </span>
          ))}
        </div>
      ) : (
        <p className="text-[11px] text-text-tertiary">
          No quota. This environment can use as much of the cluster as it asks for.
        </p>
      )}
    </div>
  )
}

export default function QuotaSection({
  projectId, environments,
}: {
  projectId: string
  environments: string[]
}) {
  if (environments.length === 0) return null

  return (
    <section className="card p-6">
      <h3 className="section-title mb-1">Quotas</h3>
      <p className="text-xs text-text-tertiary mb-4">
        A ceiling per environment. Quotas apply to the next thing created, never to what is
        already running, so the committed figure below is the one to size against.
      </p>

      <div className="space-y-3">
        {environments.map(env => (
          <EnvironmentQuotaCard key={env} projectId={projectId} environment={env} />
        ))}
      </div>
    </section>
  )
}
