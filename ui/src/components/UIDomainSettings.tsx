import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

/**
 * Sets the hostname and certificate the dashboard itself is served on.
 *
 * This edits the live Ingress, which Helm also renders from ui.ingress.* values. The next
 * `helm upgrade` re-renders it and reverts whatever is set here, so the panel shows the
 * matching --set flags rather than letting that surprise anyone later.
 */
export default function UIDomainSettings() {
  const queryClient = useQueryClient()
  const [host, setHost] = useState('')
  const [tls, setTls] = useState(true)
  const [issuer, setIssuer] = useState('')
  const [error, setError] = useState('')
  const [saved, setSaved] = useState<string[] | null>(null)
  const [confirming, setConfirming] = useState(false)

  const { data, isLoading } = useQuery({
    queryKey: ['uiDomain'],
    queryFn: () => api.getUIDomain(),
    // The certificate takes a minute or so to issue after a change.
    refetchInterval: (q) => (q.state.data?.settings.tls && !q.state.data?.certReady ? 10000 : false),
  })

  const { data: ssl } = useQuery({
    queryKey: ['sslProviders'],
    queryFn: () => api.listSSLProviders(),
  })

  useEffect(() => {
    if (!data?.settings) return
    setHost(data.settings.host)
    setTls(data.settings.tls)
    setIssuer(data.settings.clusterIssuer)
  }, [data])

  const save = useMutation({
    mutationFn: () => api.setUIDomain({
      host: host.trim(),
      tls,
      clusterIssuer: tls ? issuer : '',
      ingressClassName: data?.settings.ingressClassName ?? '',
    }),
    onSuccess: (res) => {
      setError(''); setConfirming(false); setSaved(res.helmValues)
      queryClient.invalidateQueries({ queryKey: ['uiDomain'] })
    },
    onError: (e: Error) => { setError(e.message); setConfirming(false) },
  })

  if (isLoading) return <section className="card p-6"><Spinner /></section>

  const current = data?.settings
  const changed = current && (host.trim() !== current.host || tls !== current.tls || (tls && issuer !== current.clusterIssuer))
  const providers = ssl?.providers ?? []

  return (
    <section className="card p-6 space-y-5">
      <div>
        <h3 className="section-title">Dashboard Domain</h3>
        <p className="text-xs text-text-tertiary mt-1">
          The hostname Vesta's own interface is served on. Separate from the domain your apps use.
        </p>
      </div>

      {!data?.configured ? (
        <div className="bg-surface-1 border border-border rounded-lg p-4">
          <p className="text-xs text-text-secondary">
            The dashboard has no ingress yet, so it is reachable only by port-forward. Create one once with Helm,
            after which you can change the hostname here:
          </p>
          <code className="block mt-2 text-[11px] font-mono text-text-tertiary bg-surface-0 rounded px-3 py-2 overflow-x-auto">
            helm upgrade vesta oci://ghcr.io/vesta-infra/charts/vesta -n {data?.namespace ?? 'vesta-system'} \<br />
            &nbsp;&nbsp;--reuse-values --set ui.ingress.enabled=true --set ui.ingress.host=YOUR.DOMAIN
          </code>
        </div>
      ) : (
        <>
          <div>
            <label className="label">Hostname</label>
            <input
              value={host}
              onChange={(e) => { setHost(e.target.value); setSaved(null) }}
              placeholder="vesta.example.com"
              spellCheck={false}
              className="input-field font-mono text-sm"
            />
            <p className="text-[11px] text-text-tertiary mt-1">
              Point this name at your ingress controller before saving, or the certificate cannot be issued.
            </p>
          </div>

          <div className="space-y-2">
            <label className="flex items-center gap-2 text-sm text-text-primary cursor-pointer">
              <input type="checkbox" checked={tls} onChange={(e) => { setTls(e.target.checked); setSaved(null) }} />
              Serve over HTTPS
            </label>

            {tls && (
              <div className="pl-6">
                <label className="label">Certificate issuer</label>
                <select
                  value={issuer}
                  onChange={(e) => { setIssuer(e.target.value); setSaved(null) }}
                  className="input-field text-sm"
                >
                  <option value="">Use the instance default</option>
                  {providers.map((p: any) => (
                    <option key={p.name} value={p.name}>
                      {p.name}{p.isDefault ? ' (default)' : ''}{p.ready ? '' : ' — not ready'}
                    </option>
                  ))}
                </select>
                {providers.length === 0 && (
                  <p className="text-[11px] text-text-tertiary mt-1">
                    No issuers configured. Add one under SSL Certificates first.
                  </p>
                )}
              </div>
            )}
          </div>

          {current?.tls && (
            <div className="flex items-center gap-2 text-[11px]">
              <span className={`w-1.5 h-1.5 rounded-full ${data?.certReady ? 'bg-status-running' : 'bg-status-pending'}`} />
              <span className="text-text-tertiary">
                {data?.certReady ? 'Certificate issued' : (data?.certMessage || 'Certificate pending')}
              </span>
            </div>
          )}

          {confirming ? (
            <div className="bg-surface-1 border border-status-failed/30 rounded-lg p-4 space-y-3">
              <p className="text-sm text-text-primary">Change the dashboard hostname to {host.trim()}?</p>
              <p className="text-[11px] text-text-tertiary">
                {current?.host} stops serving immediately. If the new name does not resolve to your ingress
                controller, the dashboard is reachable only by port-forward until you fix it.
              </p>
              <div className="flex gap-3">
                <button onClick={() => save.mutate()} disabled={save.isPending} className="btn-primary text-xs">
                  {save.isPending ? 'Applying...' : 'Change it'}
                </button>
                <button onClick={() => setConfirming(false)} className="btn-ghost text-xs">Cancel</button>
              </div>
            </div>
          ) : (
            <button
              onClick={() => { setError(''); setConfirming(true) }}
              disabled={!changed || !host.trim()}
              className="btn-primary text-xs"
            >
              Save
            </button>
          )}

          {/* Helm re-renders this Ingress from values, so a change here is reverted on the
              next upgrade unless the same values are passed. Say so, with the flags. */}
          <div className="pt-4 border-t border-border">
            <p className="text-[11px] text-text-tertiary mb-2">
              {saved
                ? 'Saved. Helm re-creates this ingress from its values on every upgrade, so pass these to keep it:'
                : 'Helm owns this ingress. To make the current setting survive `helm upgrade`, pass:'}
            </p>
            <code className="block text-[11px] font-mono text-text-secondary bg-surface-0 rounded px-3 py-2 overflow-x-auto whitespace-pre">
              {(saved ?? data?.helmValues ?? []).join(' \\\n  ')}
            </code>
          </div>
        </>
      )}

      {error && <p className="text-xs text-status-failed">{error}</p>}
    </section>
  )
}

function Spinner() {
  return <div className="flex justify-center py-6"><div className="w-5 h-5 border-2 border-accent/30 border-t-accent rounded-full animate-spin" /></div>
}
