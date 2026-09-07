import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import type { SSLProvider, SSLProviderInput } from '../lib/api'
import RevealableInput from './RevealableInput'

/**
 * Provider kinds, as a table rather than a switch, so adding a CA is one row.
 *
 * `eabRequired` matters more than it looks: ZeroSSL and Google Trust Services reject
 * account registration outright without External Account Binding credentials, and the
 * failure surfaces asynchronously as a Pending issuer with an opaque message. Demanding
 * them in the form turns that into a field-level error.
 */
const PROVIDER_KINDS: {
  value: string
  label: string
  acme: boolean
  eabRequired?: boolean
  note?: string
}[] = [
  { value: 'letsencrypt', label: "Let's Encrypt", acme: true, note: 'Free, trusted everywhere. Rate limited to 50 certificates per domain per week.' },
  { value: 'letsencrypt-staging', label: "Let's Encrypt (staging)", acme: true, note: 'Untrusted certificates with generous rate limits. Use this while testing DNS and ingress.' },
  { value: 'zerossl', label: 'ZeroSSL', acme: true, eabRequired: true, note: 'Requires EAB credentials from your ZeroSSL dashboard.' },
  { value: 'buypass', label: 'Buypass Go', acme: true, note: 'Free 180-day certificates.' },
  { value: 'google', label: 'Google Trust Services', acme: true, eabRequired: true, note: 'Requires EAB credentials from your Google Cloud project.' },
  { value: 'custom-acme', label: 'Custom ACME server', acme: true, note: 'Any RFC 8555 endpoint — a private ACME server, or a CA not listed here.' },
  { value: 'selfsigned', label: 'Self-signed', acme: false, note: 'Browsers will warn. Useful for internal environments behind a VPN.' },
  { value: 'ca', label: 'Private CA', acme: false, note: 'Signs with a CA keypair you already hold in a Kubernetes Secret.' },
]

const DNS_PROVIDERS: {
  value: string
  label: string
  fields: { key: string; label: string; placeholder?: string; secret?: boolean }[]
}[] = [
  { value: 'cloudflare', label: 'Cloudflare', fields: [{ key: 'apiToken', label: 'API token', placeholder: 'Zone:DNS:Edit scoped token', secret: true }] },
  {
    value: 'route53', label: 'AWS Route 53', fields: [
      { key: 'accessKeyId', label: 'Access key ID', placeholder: 'AKIA...' },
      { key: 'secretAccessKey', label: 'Secret access key', secret: true },
      { key: 'region', label: 'Region', placeholder: 'us-east-1' },
    ],
  },
  { value: 'digitalocean', label: 'DigitalOcean', fields: [{ key: 'token', label: 'API token', secret: true }] },
  {
    value: 'clouddns', label: 'Google Cloud DNS', fields: [
      { key: 'project', label: 'Project ID', placeholder: 'my-gcp-project' },
      { key: 'serviceAccountJson', label: 'Service account JSON', secret: true },
    ],
  },
]

// Which of a DNS provider's fields are credentials rather than plain config. Credentials
// go to a Secret in cert-manager's namespace; config is inlined in the ClusterIssuer.
const SECRET_FIELDS = new Set(['apiToken', 'secretAccessKey', 'token', 'serviceAccountJson'])

export function providerKindLabel(kind: string): string {
  return PROVIDER_KINDS.find(k => k.value === kind)?.label || kind || 'Unknown'
}

export default function SSLProvidersSection() {
  const queryClient = useQueryClient()
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<SSLProvider | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['ssl-providers'],
    queryFn: api.listSSLProviders,
    // A cluster without cert-manager returns a normal response, not an error — retrying
    // would just delay showing the install instructions.
    retry: false,
  })

  const setDefault = useMutation({
    mutationFn: (name: string) => api.setDefaultSSLProvider(name),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['ssl-providers'] }),
  })

  const remove = useMutation({
    mutationFn: ({ name, force }: { name: string; force: boolean }) => api.deleteSSLProvider(name, force),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['ssl-providers'] }),
  })

  if (isLoading) {
    return <p className="text-xs text-text-tertiary">Loading certificate providers…</p>
  }

  if (data && !data.certManagerInstalled) {
    return <CertManagerMissing />
  }

  const providers = data?.providers || []

  return (
    <div className="space-y-4">
      {providers.length === 0 && !adding && (
        <p className="text-xs text-text-tertiary">
          No certificate providers yet. Apps with TLS enabled will not get a certificate until one exists.
        </p>
      )}

      <div className="space-y-2">
        {providers.map(p => (
          <ProviderRow
            key={p.name}
            provider={p}
            onEdit={() => setEditing(p)}
            onSetDefault={() => setDefault.mutate(p.name)}
            onDelete={(force) => remove.mutate({ name: p.name, force })}
            deleteError={remove.variables?.name === p.name ? (remove.error as Error | null) : null}
            busy={setDefault.isPending || remove.isPending}
          />
        ))}
      </div>

      {editing && (
        <ProviderForm
          existing={editing}
          onClose={() => setEditing(null)}
          onSaved={() => { setEditing(null); queryClient.invalidateQueries({ queryKey: ['ssl-providers'] }) }}
        />
      )}

      {adding ? (
        <ProviderForm
          onClose={() => setAdding(false)}
          onSaved={() => { setAdding(false); queryClient.invalidateQueries({ queryKey: ['ssl-providers'] }) }}
        />
      ) : (
        !editing && (
          <button onClick={() => setAdding(true)} className="btn-outline text-xs">
            + Add provider
          </button>
        )
      )}
    </div>
  )
}

function CertManagerMissing() {
  return (
    <div className="space-y-3">
      <p className="text-xs text-text-secondary">
        cert-manager is not installed in this cluster. Vesta issues certificates through it, so TLS
        cannot be provisioned until it is present.
      </p>
      <pre className="text-[11px] font-mono bg-surface-1 border border-border rounded p-3 overflow-x-auto">
{`helm repo add jetstack https://charts.jetstack.io
helm install cert-manager jetstack/cert-manager \\
  --namespace cert-manager --create-namespace --set crds.enabled=true`}
      </pre>
      <p className="text-[11px] text-text-tertiary">
        Once it is running, reload this page to add a provider.
      </p>
    </div>
  )
}

function ProviderRow({ provider, onEdit, onSetDefault, onDelete, deleteError, busy }: {
  provider: SSLProvider
  onEdit: () => void
  onSetDefault: () => void
  onDelete: (force: boolean) => void
  deleteError: Error | null
  busy: boolean
}) {
  const [confirming, setConfirming] = useState(false)

  const statusColor = provider.ready
    ? 'text-status-running'
    : provider.status === 'Failed' ? 'text-status-failed' : 'text-text-tertiary'

  return (
    <div className="panel p-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-xs font-mono">{provider.name}</span>
            {provider.isDefault && <span className="chip">Default</span>}
            {!provider.managed && (
              <span className="chip" title="Created outside Vesta — editable only with kubectl">
                External
              </span>
            )}
          </div>
          <p className="text-[11px] text-text-tertiary mt-1">
            {providerKindLabel(provider.kind)}
            {provider.email && ` · ${provider.email}`}
            {provider.solver && ` · ${provider.solver}`}
            {provider.dnsProvider && ` (${provider.dnsProvider})`}
          </p>
          <p className={`text-[11px] mt-1 ${statusColor}`}>
            {provider.status}
            {provider.statusReason && `: ${provider.statusReason}`}
          </p>
        </div>

        <div className="flex items-center gap-2 shrink-0">
          {!provider.isDefault && (
            <button onClick={onSetDefault} disabled={busy} className="btn-ghost text-[11px]">
              Make default
            </button>
          )}
          {provider.managed && (
            <button onClick={onEdit} disabled={busy} className="btn-ghost text-[11px]">Edit</button>
          )}
          <button
            onClick={() => setConfirming(true)}
            disabled={busy}
            className="btn-ghost text-[11px] text-status-failed"
          >
            Delete
          </button>
        </div>
      </div>

      {confirming && (
        <div className="mt-3 pt-3 border-t border-border space-y-2">
          <p className="text-[11px] text-text-secondary">
            Delete <span className="font-mono">{provider.name}</span>? Certificates already issued keep
            working until they expire, then stop renewing.
          </p>
          {/* The 409 body names the apps still pointing at this provider — showing it is
              the whole reason the delete is guarded rather than silently succeeding. */}
          {deleteError && <p className="text-[11px] text-status-failed">{deleteError.message}</p>}
          <div className="flex gap-2">
            <button onClick={() => onDelete(false)} className="btn-primary text-[11px]">Delete</button>
            {deleteError && (
              <button onClick={() => onDelete(true)} className="btn-outline text-[11px] text-status-failed">
                Delete anyway
              </button>
            )}
            <button onClick={() => setConfirming(false)} className="btn-ghost text-[11px]">Cancel</button>
          </div>
        </div>
      )}
    </div>
  )
}

function ProviderForm({ existing, onClose, onSaved }: {
  existing?: SSLProvider
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(existing?.name || '')
  const [kind, setKind] = useState(existing?.kind || 'letsencrypt')
  const [email, setEmail] = useState(existing?.email || '')
  const [acmeServer, setAcmeServer] = useState(existing?.acmeServer || '')
  const [eabKeyId, setEabKeyId] = useState('')
  const [eabHmacKey, setEabHmacKey] = useState('')
  const [ingressClass, setIngressClass] = useState(existing?.ingressClass || '')
  const [dnsProvider, setDnsProvider] = useState(existing?.dnsProvider || '')
  const [dnsFields, setDnsFields] = useState<Record<string, string>>({})
  const [dnsZones, setDnsZones] = useState((existing?.dnsZones || []).join(', '))
  const [caSecretName, setCaSecretName] = useState('')

  const kindDef = PROVIDER_KINDS.find(k => k.value === kind)!
  const dnsDef = DNS_PROVIDERS.find(d => d.value === dnsProvider)

  const save = useMutation({
    mutationFn: (input: SSLProviderInput) =>
      existing ? api.updateSSLProvider(existing.name, input) : api.createSSLProvider(input),
    onSuccess: onSaved,
  })

  const submit = (e: React.FormEvent) => {
    e.preventDefault()

    const dnsConfig: Record<string, string> = {}
    const dnsCredentials: Record<string, string> = {}
    for (const [key, value] of Object.entries(dnsFields)) {
      if (!value) continue
      if (SECRET_FIELDS.has(key)) dnsCredentials[key] = value
      else dnsConfig[key] = value
    }

    save.mutate({
      name: name.trim(),
      kind,
      ...(kindDef.acme && { email: email.trim() }),
      ...(kind === 'custom-acme' && { acmeServer: acmeServer.trim() }),
      ...(eabKeyId && { eabKeyId: eabKeyId.trim() }),
      ...(eabHmacKey && { eabHmacKey: eabHmacKey.trim() }),
      ...(kind === 'ca' && { caSecretName: caSecretName.trim() }),
      ...(dnsProvider
        ? { dnsProvider, dnsConfig, dnsCredentials }
        : { ingressClass: ingressClass.trim() }),
      ...(dnsZones.trim() && { dnsZones: dnsZones.split(',').map(z => z.trim()).filter(Boolean) }),
    })
  }

  return (
    <form onSubmit={submit} className="panel p-4 space-y-4">
      <div className="panel-header">{existing ? `Edit ${existing.name}` : 'Add certificate provider'}</div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <label className="label">Name</label>
          <input
            value={name}
            onChange={e => setName(e.target.value)}
            disabled={!!existing}
            className="input-field font-mono text-xs"
            placeholder="letsencrypt-prod"
            required
          />
          <p className="text-[11px] text-text-tertiary mt-1">
            Lowercase letters, digits and dashes. Cannot be changed later.
          </p>
        </div>

        <div>
          <label className="label">Certificate authority</label>
          <select
            value={kind}
            onChange={e => { setKind(e.target.value); setDnsFields({}) }}
            className="input-field"
          >
            {PROVIDER_KINDS.map(k => <option key={k.value} value={k.value}>{k.label}</option>)}
          </select>
          {kindDef.note && <p className="text-[11px] text-text-tertiary mt-1">{kindDef.note}</p>}
        </div>
      </div>

      {kindDef.acme && (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label className="label">Account email</label>
            <input
              value={email}
              onChange={e => setEmail(e.target.value)}
              className="input-field text-xs"
              placeholder="ops@example.com"
              type="email"
              required
            />
            <p className="text-[11px] text-text-tertiary mt-1">The CA sends expiry warnings here.</p>
          </div>

          {kind === 'custom-acme' && (
            <div>
              <label className="label">ACME directory URL</label>
              <input
                value={acmeServer}
                onChange={e => setAcmeServer(e.target.value)}
                className="input-field font-mono text-xs"
                placeholder="https://acme.internal/directory"
                required
              />
            </div>
          )}
        </div>
      )}

      {kindDef.eabRequired && (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label className="label">EAB key ID</label>
            <input
              value={eabKeyId}
              onChange={e => setEabKeyId(e.target.value)}
              className="input-field font-mono text-xs"
              required={!existing}
            />
          </div>
          <div>
            <label className="label">EAB HMAC key</label>
            <RevealableInput
              value={eabHmacKey}
              onChange={e => setEabHmacKey(e.target.value)}
              className="input-field font-mono text-xs"
              type="password"
              required={!existing}
              placeholder={existing ? 'unchanged' : ''}
            />
          </div>
        </div>
      )}

      {kind === 'ca' && (
        <div>
          <label className="label">CA keypair Secret</label>
          <input
            value={caSecretName}
            onChange={e => setCaSecretName(e.target.value)}
            className="input-field font-mono text-xs"
            placeholder="corp-ca-keypair"
            required
          />
          <p className="text-[11px] text-text-tertiary mt-1">
            A Secret of type kubernetes.io/tls in cert-manager's namespace, holding the CA certificate and key.
          </p>
        </div>
      )}

      {kindDef.acme && (
        <>
          <div>
            <label className="label">Validation method</label>
            <select
              value={dnsProvider}
              onChange={e => { setDnsProvider(e.target.value); setDnsFields({}) }}
              className="input-field"
            >
              <option value="">HTTP-01 (through the ingress)</option>
              {DNS_PROVIDERS.map(d => (
                <option key={d.value} value={d.value}>DNS-01 — {d.label}</option>
              ))}
            </select>
            <p className="text-[11px] text-text-tertiary mt-1">
              HTTP-01 needs each domain to resolve to this cluster on port 80. DNS-01 is required for
              wildcard certificates and for clusters not reachable from the internet.
            </p>
          </div>

          {!dnsProvider && (
            <div>
              <label className="label">Ingress class</label>
              <input
                value={ingressClass}
                onChange={e => setIngressClass(e.target.value)}
                className="input-field font-mono text-xs"
                placeholder="leave empty to use the cluster default"
              />
            </div>
          )}

          {dnsDef && (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              {dnsDef.fields.map(f => (
                <div key={f.key}>
                  <label className="label">{f.label}</label>
                  {f.secret ? (
                    <RevealableInput
                      value={dnsFields[f.key] || ''}
                      onChange={e => setDnsFields(prev => ({ ...prev, [f.key]: e.target.value }))}
                      className="input-field font-mono text-xs"
                      type="password"
                      placeholder={existing ? 'unchanged' : f.placeholder}
                    />
                  ) : (
                    <input
                      value={dnsFields[f.key] || ''}
                      onChange={e => setDnsFields(prev => ({ ...prev, [f.key]: e.target.value }))}
                      className="input-field font-mono text-xs"
                      placeholder={f.placeholder}
                    />
                  )}
                </div>
              ))}
            </div>
          )}

          <div>
            <label className="label">Restrict to DNS zones (optional)</label>
            <input
              value={dnsZones}
              onChange={e => setDnsZones(e.target.value)}
              className="input-field font-mono text-xs"
              placeholder="example.com, example.org"
            />
          </div>
        </>
      )}

      {save.isError && (
        <p className="text-status-failed text-xs">{(save.error as Error).message}</p>
      )}

      <div className="flex gap-2">
        <button type="submit" disabled={save.isPending} className="btn-primary text-xs">
          {save.isPending ? 'Saving…' : existing ? 'Save changes' : 'Add provider'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost text-xs">Cancel</button>
      </div>
    </form>
  )
}
