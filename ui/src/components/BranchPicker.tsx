import { useId } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'

/**
 * Branch selection, offered from the repository but never restricted to it.
 *
 * A datalist rather than a select, matching ImageRepositoryInput next door. The edit page
 * grew three inline copies that switch between a <select> when branches loaded and an
 * <input> when they did not — which means that once the list arrives you can no longer type
 * a branch that does not exist yet. Naming the branch a release will be cut from, before
 * cutting it, is an ordinary thing to do.
 *
 * Listing can also fail: a token without repository scope, a rate limit, a repository the
 * connection can no longer see. That must not take the field with it, so the input works
 * whether or not the query succeeds, and the hint says which happened.
 */
export function BranchPicker({
  repository, provider, host, connectionId, value, onChange, className, id, placeholder,
}: {
  /** Empty disables the lookup and leaves a plain text input. */
  repository?: string
  provider?: string
  host?: string
  connectionId?: string
  value: string
  onChange: (value: string) => void
  className?: string
  id?: string
  placeholder?: string
}) {
  const listId = useId()

  // A repository needs an owner and a name before it can be looked up; asking about "web"
  // just spends a round trip to be told nothing.
  const enabled = !!repository && repository.includes('/')

  const { data, isLoading, isError } = useQuery({
    queryKey: ['repoBranches', repository, provider, host, connectionId],
    queryFn: () => api.listRepoBranches(repository!, { provider, host, connectionId }),
    enabled,
    staleTime: 60_000,
    retry: false,
  })

  const branches = data?.branches ?? []

  return (
    <>
      <input
        id={id}
        list={enabled ? listId : undefined}
        value={value}
        onChange={e => onChange(e.target.value)}
        className={className ?? 'input-field font-mono text-xs w-full'}
        placeholder={placeholder ?? 'main'}
        autoComplete="off"
      />
      {enabled && (
        <datalist id={listId}>
          {branches.map(b => (
            <option key={b} value={b} />
          ))}
        </datalist>
      )}
      {enabled && isLoading && (
        <p className="text-[11px] text-text-quaternary mt-1">Loading branches…</p>
      )}
      {enabled && isError && (
        <p className="text-[11px] text-status-degraded mt-1">
          Could not list branches — type one instead.
        </p>
      )}
      {enabled && !isLoading && !isError && branches.length === 0 && (
        <p className="text-[11px] text-text-quaternary mt-1">
          No branches visible through this connection. You can still type one.
        </p>
      )}
    </>
  )
}
