import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

// Attaching middlewares to one environment. The list is ordered and the order is the
// meaning: Traefik runs middlewares in sequence, so an allow list placed after a basic-auth
// check prompts strangers for a password before rejecting them. Hence the explicit
// up/down controls rather than a set of checkboxes.
export default function MiddlewareAttachSection({ appId, environments, role }: {
  appId: string
  environments: string[]
  role: string
}) {
  const queryClient = useQueryClient()
  const [env, setEnv] = useState(environments[0] ?? '')
  const [attached, setAttached] = useState<string[]>([])
  const [inherited, setInherited] = useState(true)
  const [dirty, setDirty] = useState(false)

  const canEdit = role === 'admin' || role === 'owner' || role === 'developer'

  const { data: available } = useQuery({
    queryKey: ['middlewares'],
    queryFn: () => api.listMiddlewares(),
  })

  const { data: current } = useQuery({
    queryKey: ['appMiddlewares', appId, env],
    queryFn: () => api.getAppMiddlewares(appId, env),
    enabled: !!env,
  })

  useEffect(() => {
    if (current) {
      setAttached(current.middlewares ?? [])
      setInherited(current.inherited)
      setDirty(false)
    }
  }, [current])

  const save = useMutation({
    mutationFn: (payload: { middlewares: string[]; inherit: boolean }) =>
      api.updateAppMiddlewares(appId, { environment: env, ...payload }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['appMiddlewares', appId, env] })
      queryClient.invalidateQueries({ queryKey: ['middlewares'] })
      setDirty(false)
    },
  })

  const all = available?.middlewares ?? []
  const unattached = all.filter(m => !attached.includes(m.name))

  function move(index: number, delta: number) {
    const target = index + delta
    if (target < 0 || target >= attached.length) return
    const next = [...attached]
    ;[next[index], next[target]] = [next[target], next[index]]
    setAttached(next)
    setInherited(false)
    setDirty(true)
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <label className="label">Middlewares</label>
        {environments.length > 1 && (
          <select value={env} onChange={e => setEnv(e.target.value)} className="input-field text-xs py-1">
            {environments.map(e => <option key={e} value={e}>{e}</option>)}
          </select>
        )}
      </div>

      <p className="text-[10px] text-text-tertiary mb-2">
        Applied in order, top to bottom. Order matters — an allow list above an auth check
        rejects unknown addresses without prompting for a password.
      </p>

      {inherited && attached.length > 0 && (
        <p className="text-[10px] text-text-tertiary mb-2">
          Inherited from the app. Changing the order or contents here sets them for
          <span className="font-mono"> {env}</span> only.
        </p>
      )}

      {attached.length === 0 && (
        <p className="text-xs text-text-tertiary mb-2">None attached.</p>
      )}

      <div className="space-y-1.5 mb-3">
        {attached.map((name, i) => {
          const meta = all.find(m => m.name === name)
          return (
            <div key={name} className="flex items-center gap-2 bg-surface-hover rounded px-2.5 py-1.5">
              <span className="text-[10px] text-text-tertiary w-4">{i + 1}</span>
              <span className="font-mono text-xs flex-1">{name}</span>
              {meta && !meta.ready && meta.reason && (
                <span className="text-[10px] text-status-failed" title={meta.reason}>not active</span>
              )}
              {canEdit && (
                <>
                  <button onClick={() => move(i, -1)} disabled={i === 0}
                    className="text-xs text-text-tertiary hover:text-accent disabled:opacity-30">↑</button>
                  <button onClick={() => move(i, 1)} disabled={i === attached.length - 1}
                    className="text-xs text-text-tertiary hover:text-accent disabled:opacity-30">↓</button>
                  <button
                    onClick={() => { setAttached(attached.filter(n => n !== name)); setInherited(false); setDirty(true) }}
                    className="text-xs text-text-tertiary hover:text-status-failed px-1">&times;</button>
                </>
              )}
            </div>
          )
        })}
      </div>

      {canEdit && unattached.length > 0 && (
        <select
          value=""
          onChange={e => {
            if (!e.target.value) return
            setAttached([...attached, e.target.value])
            setInherited(false)
            setDirty(true)
          }}
          className="input-field text-xs w-full mb-2"
        >
          <option value="">+ Attach a middleware…</option>
          {unattached.map(m => (
            <option key={m.name} value={m.name}>{m.name} — {m.type}</option>
          ))}
        </select>
      )}

      {canEdit && all.length === 0 && (
        <p className="text-[10px] text-text-tertiary">
          No middlewares defined yet. Create one under Middlewares in the sidebar.
        </p>
      )}

      {canEdit && dirty && (
        <div className="flex gap-2 items-center">
          <button
            onClick={() => save.mutate({ middlewares: attached, inherit: false })}
            disabled={save.isPending}
            className="btn-primary text-xs"
          >
            {save.isPending ? 'Saving…' : 'Save'}
          </button>
          <button
            onClick={() => { setDirty(false); setAttached(current?.middlewares ?? []); setInherited(current?.inherited ?? true) }}
            className="btn-secondary text-xs"
          >
            Cancel
          </button>
          {!inherited && (
            // Restoring inheritance is a distinct action from emptying the list: absent
            // inherits the app's middlewares, empty applies none.
            <button
              onClick={() => save.mutate({ middlewares: [], inherit: true })}
              className="text-[10px] text-text-tertiary hover:text-accent ml-auto"
            >
              Reset to app default
            </button>
          )}
        </div>
      )}

      {save.isError && (
        <p className="text-xs text-status-failed mt-2">{(save.error as Error).message}</p>
      )}
    </div>
  )
}
