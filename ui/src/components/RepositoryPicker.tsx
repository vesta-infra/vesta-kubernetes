import { useQuery } from '@tanstack/react-query'
import { api, type AccessibleRepo } from '../lib/api'

/**
 * Picks a repository from the connections Vesta can reach.
 *
 * Choosing from a list rather than typing matters more here than it looks. The webhook
 * matcher compares an app's repository against the one a push names, so a value that does
 * not correspond to a real repository simply never deploys, with nothing said anywhere. The
 * old create form made that easy: its "+ Link Repository" button seeded the field with the
 * literal string "org/repo", which an unedited form saved verbatim.
 *
 * Choosing also records which connection serves the repository, and on which host, so a
 * self-managed GitLab and gitlab.com can both be connected without either shadowing the
 * other.
 */

export interface RepositorySelection {
  repository: string
  provider: string
  host: string
  connectionId: string
  defaultBranch?: string
}

export function RepositoryPicker({
  value, provider, onSelect, onClear,
}: {
  value: string
  provider: string
  onSelect: (sel: RepositorySelection) => void
  /** Called when the user wants to type a value the list does not offer. */
  onClear?: () => void
}) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['accessible-repos'],
    queryFn: () => api.listAccessibleRepos(),
    staleTime: 60_000,
    retry: false,
  })

  const repos = data?.repos ?? []
  const problems = data?.problems ?? []

  // Several connections can reach different hosts, so the list is grouped by connection
  // rather than presented as one flat set of names that look interchangeable.
  const byConnection = repos.reduce<Record<string, AccessibleRepo[]>>((acc, r) => {
    (acc[r.connectionName] ||= []).push(r)
    return acc
  }, {})

  return (
    <div className="space-y-2">
      <select
        value={value}
        onChange={e => {
          const chosen = repos.find(r => r.full_name === e.target.value)
          if (chosen) {
            onSelect({
              repository: chosen.full_name,
              provider: chosen.provider,
              host: chosen.host,
              connectionId: chosen.connectionId,
              defaultBranch: chosen.defaultBranch,
            })
          }
        }}
        className="input-field font-mono text-xs w-full"
      >
        <option value="">
          {isLoading ? 'Loading repositories…' : 'Select a repository…'}
        </option>

        {/* Keep whatever is already set, even when the list does not contain it -- a
            connection can be removed while an app still names its repository. */}
        {value && !repos.some(r => r.full_name === value) && (
          <option value={value}>{value} (not in any connection)</option>
        )}

        {Object.entries(byConnection).map(([connection, items]) => (
          <optgroup key={connection} label={connection}>
            {items.map(r => (
              <option key={`${r.connectionId}:${r.full_name}`} value={r.full_name}>
                {r.full_name}{r.private ? '' : ' (public)'}
              </option>
            ))}
          </optgroup>
        ))}
      </select>

      {error && (
        <p className="text-[11px] text-status-failed">
          Could not list repositories: {(error as Error).message}
        </p>
      )}

      {/* A connection that failed is named, rather than silently contributing nothing.
          Without this, an expired token looks identical to an empty account. */}
      {problems.map(p => (
        <p key={p.connectionName} className="text-[11px] text-status-degraded">
          {p.connectionName}: {p.error}
        </p>
      ))}

      {!isLoading && !error && repos.length === 0 && (
        <p className="text-[11px] text-text-tertiary">
          No repositories available. Connect a git provider under Settings → Integrations.
        </p>
      )}

      {onClear && (
        <button type="button" onClick={onClear} className="text-[11px] text-text-tertiary hover:text-accent">
          Enter a repository manually instead
        </button>
      )}

      <ConnectMoreRepos provider={provider} />
    </div>
  )
}

/**
 * Sends the user to wherever this provider grants access to more repositories.
 *
 * The link already existed, but only in Settings — which is the wrong place, because the
 * moment you notice a repository is missing is the moment you are trying to pick it. The URL
 * comes from the connection rather than being rebuilt here, so the two cannot drift, and it
 * is absent for a provider that has no such page.
 */
function ConnectMoreRepos({ provider }: { provider: string }) {
  const { data } = useQuery({
    queryKey: ['git-connections'],
    queryFn: () => api.listGitConnections(),
    staleTime: 60_000,
  })

  const withInstall = (data?.items ?? []).filter(c => c.installUrl && (!provider || c.provider === provider))
  if (withInstall.length === 0) return null

  return (
    <div className="flex flex-wrap items-center gap-3 pt-1">
      {withInstall.map(c => (
        <a
          key={c.id}
          href={c.installUrl}
          target="_blank"
          rel="noreferrer"
          className="text-[11px] text-accent hover:text-accent-glow"
          title={
            c.provider === 'github'
              ? 'Grant the Vesta app access to more repositories, then reopen this list'
              : 'Access follows this token’s scope — widen or replace it, then reopen this list'
          }
        >
          Add repositories in {c.displayName} →
        </a>
      ))}
    </div>
  )
}
