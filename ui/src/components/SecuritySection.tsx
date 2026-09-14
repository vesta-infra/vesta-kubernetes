import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type SecurityPosture } from '../lib/api'

/**
 * Platform security posture.
 *
 * Both settings here change how workloads run, and both can break a working instance, so the
 * screen's job is less to offer the choice than to be honest about what each one does.
 *
 * The hardening profiles are a ladder because a single on/off switch would be unusable: the
 * restrictions that break real images (a non-root user, a read-only filesystem) are a
 * minority of what hardening means, and bundling them with the ones nothing notices would
 * mean the whole feature gets switched on once, breaks something, and stays off forever.
 */

const PROFILES = [
  {
    value: 'legacy' as const,
    label: 'Legacy',
    summary: 'No security context at all.',
    detail:
      'What every app already runs with. Nothing changes, and nothing is hardened.',
  },
  {
    value: 'baseline' as const,
    label: 'Baseline',
    summary: 'No privilege escalation, no capabilities, default seccomp.',
    detail:
      'The restrictions almost no image notices. Containers still run as whatever user they ' +
      'were built to run as, and can still write to their own filesystem. Safe to turn on ' +
      'across an instance.',
  },
  {
    value: 'restricted' as const,
    label: 'Restricted',
    summary: 'Baseline, plus a non-root user and a read-only filesystem.',
    detail:
      'The two that break real images. An app that writes anywhere other than /tmp, /var/run ' +
      'or a mounted volume will fail. Best set per app rather than instance-wide.',
  },
]

function ObservedIsolation({ posture }: { posture: SecurityPosture }) {
  const observed = posture.observed ?? []
  if (!posture.networkIsolation?.enabled || observed.length === 0) return null

  // One environment's answer is every environment's answer -- it is a property of the
  // cluster's CNI -- so this collapses to a single verdict rather than repeating it per row.
  const sample = observed[0]

  if (sample.enforced) {
    return (
      <p className="text-[11px] text-status-running mt-2">
        Enforced. {sample.note}
      </p>
    )
  }

  if (sample.enforcementKnown) {
    return (
      <div className="mt-2 px-3 py-2 rounded border border-status-failed/40 bg-status-failed-bg">
        <p className="text-[11px] text-status-failed">
          <strong>Not enforced.</strong> {sample.note}. The policies exist and are visible in
          kubectl, but no traffic is being filtered &mdash; environments are not isolated.
        </p>
      </div>
    )
  }

  return (
    <div className="mt-2 px-3 py-2 rounded border border-status-pending/40 bg-status-pending-bg">
      <p className="text-[11px] text-status-pending">
        <strong>Unverified.</strong> {sample.note}
      </p>
    </div>
  )
}

export default function SecuritySection() {
  const queryClient = useQueryClient()
  const { data, isLoading } = useQuery({
    queryKey: ['securityPosture'],
    queryFn: () => api.getSecurityPosture(),
  })

  const [confirmRestricted, setConfirmRestricted] = useState(false)

  const save = useMutation({
    mutationFn: (posture: Partial<SecurityPosture>) => api.setSecurityPosture(posture),
    onSuccess: () => {
      setConfirmRestricted(false)
      queryClient.invalidateQueries({ queryKey: ['securityPosture'] })
    },
  })

  if (isLoading || !data) return null

  const isolation = data.networkIsolation ?? { enabled: false }

  function chooseProfile(value: SecurityPosture['profile']) {
    // Restricted is the one that breaks images, so it asks. Baseline and legacy do not:
    // a confirmation on every choice trains people to click through the one that matters.
    if (value === 'restricted' && !confirmRestricted) {
      setConfirmRestricted(true)
      return
    }
    save.mutate({ profile: value })
  }

  return (
    <section className="card p-6">
      <h3 className="section-title mb-1">Security</h3>
      <p className="text-xs text-text-tertiary mb-5">
        How app containers are constrained, and whether environments can reach each other.
        Both are off by default and neither changes a running pod until it is next deployed.
      </p>

      <div className="mb-6">
        <h4 className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-3">
          Pod hardening
        </h4>

        <div className="space-y-2">
          {PROFILES.map(p => {
            const active = data.profile === p.value
            return (
              <button
                key={p.value}
                onClick={() => chooseProfile(p.value)}
                disabled={save.isPending}
                className={`w-full text-left px-4 py-3 rounded-lg border transition-colors ${
                  active
                    ? 'border-accent bg-accent-muted'
                    : 'border-border hover:border-border-hover'
                }`}
              >
                <div className="flex items-baseline justify-between">
                  <span className={`text-sm ${active ? 'text-accent' : 'text-text-primary'}`}>
                    {p.label}
                  </span>
                  <span className="text-[11px] text-text-secondary">{p.summary}</span>
                </div>
                <p className="text-[11px] text-text-tertiary mt-1">{p.detail}</p>
              </button>
            )
          })}
        </div>

        {confirmRestricted && (
          <div className="mt-3 px-3 py-3 rounded border border-status-pending/40 bg-status-pending-bg">
            <p className="text-[11px] text-status-pending mb-2">
              Restricted forces every app to run as a non-root user with a read-only root
              filesystem. Images that were not built for that will fail to start as each app is
              next reconciled. Individual apps can opt back out.
            </p>
            <div className="flex items-center gap-2">
              <button
                onClick={() => save.mutate({ profile: 'restricted' })}
                className="btn-primary text-xs"
              >
                Apply to every app
              </button>
              <button onClick={() => setConfirmRestricted(false)} className="btn-ghost text-xs">
                Cancel
              </button>
            </div>
          </div>
        )}
      </div>

      <div className="mb-6">
        <h4 className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-3">
          Default secret scope
        </h4>

        <select
          value={data.defaultSecretScope ?? 'global'}
          disabled={save.isPending}
          onChange={e => save.mutate({ defaultSecretScope: e.target.value as 'global' | 'project' })}
          className="input-field text-xs py-1.5 w-full max-w-sm"
        >
          <option value="global">Global — usable across the instance</option>
          <option value="project">Project — private to the project that owns it</option>
        </select>

        <p className="text-[11px] text-text-tertiary mt-2">
          The scope given to a registry credential created without one. This applies to{' '}
          <strong>new</strong> secrets only &mdash; existing credentials keep the scope they
          have. Narrowing one an app already pulls with would break that app's next deploy,
          and the only symptom would be an ImagePullBackOff naming the image rather than the
          cause.
        </p>
      </div>

      <div>
        <h4 className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-3">
          Network isolation
        </h4>

        <label className="flex items-start gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={isolation.enabled}
            disabled={save.isPending}
            onChange={e =>
              save.mutate({ networkIsolation: { ...isolation, enabled: e.target.checked } })
            }
            className="mt-0.5"
          />
          <span className="text-xs text-text-secondary">
            Stop environments reaching each other
            <span className="block text-[10px] text-text-tertiary mt-0.5">
              Denies inbound traffic to each environment except from within that environment
              and from the ingress controller. Outbound traffic is untouched &mdash; a
              default-deny on egress breaks DNS, and the isolation worth having is entirely an
              inbound property.
            </span>
          </span>
        </label>

        <ObservedIsolation posture={data} />
      </div>

      {save.error && (
        <p className="text-xs text-status-failed mt-3">{(save.error as Error).message}</p>
      )}
    </section>
  )
}
