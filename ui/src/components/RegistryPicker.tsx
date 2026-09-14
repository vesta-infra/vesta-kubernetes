import { useId } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'

/**
 * Image repository and tag inputs backed by the registry.
 *
 * These were free text on six screens, with nothing checking that what was typed existed.
 * A typo surfaced only as ImagePullBackOff after a deploy, which names the image rather
 * than the mistake.
 *
 * Two decisions worth knowing:
 *
 * They are `<input list>` with a `<datalist>`, not a custom dropdown. Typing a value the
 * registry did not offer stays valid, which matters because listing can fail, a credential
 * may not be able to see every repository, and an image can be pushed a second before the
 * form is opened. The free-text fallback is the element's own behaviour rather than
 * something that has to be remembered.
 *
 * The credential is passed in rather than inferred from the repository's host. Inference
 * would mean reimplementing the host normalisation that already exists in Go, and two
 * copies of that rule would drift. Every screen with an image field already has the pull
 * secret selected next to it.
 */

interface RepositoryInputProps {
  /** Registry credential to list through. Without one this is a plain text input. */
  secretName?: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
  className?: string
  id?: string
}

export function ImageRepositoryInput({
  secretName, value, onChange, placeholder, className, id,
}: RepositoryInputProps) {
  const listId = useId()

  const { data, isLoading, error } = useQuery({
    queryKey: ['registry-repositories', secretName],
    queryFn: () => api.listRegistryRepositories(secretName!),
    enabled: !!secretName,
    staleTime: 60_000,
    retry: false,
  })

  const repositories = data?.repositories ?? []

  return (
    <>
      <input
        id={id}
        list={secretName ? listId : undefined}
        value={value}
        onChange={e => onChange(e.target.value)}
        className={className ?? 'input-field font-mono text-xs w-full'}
        placeholder={placeholder ?? 'registry.example.com/org/app'}
        autoComplete="off"
      />
      {secretName && (
        <datalist id={listId}>
          {repositories.map(repo => (
            <option key={repo} value={repo} />
          ))}
        </datalist>
      )}
      <RegistryHint
        secretName={secretName}
        isLoading={isLoading}
        error={error as Error | null}
        count={repositories.length}
        noun="repositories"
      />
    </>
  )
}

interface TagInputProps {
  secretName?: string
  /** The repository whose tags to offer. */
  repository?: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
  className?: string
  id?: string
}

export function ImageTagInput({
  secretName, repository, value, onChange, placeholder, className, id,
}: TagInputProps) {
  const listId = useId()
  const enabled = !!secretName && !!repository

  const { data, isLoading, error } = useQuery({
    queryKey: ['registry-tags', secretName, repository],
    queryFn: () => api.listRegistryTags(secretName!, repository!),
    enabled,
    staleTime: 30_000,
    retry: false,
  })

  const tags = data?.tags ?? []

  return (
    <>
      <input
        id={id}
        list={enabled ? listId : undefined}
        value={value}
        onChange={e => onChange(e.target.value)}
        className={className ?? 'input-field font-mono text-xs w-full'}
        placeholder={placeholder ?? 'v1.2.3'}
        autoComplete="off"
      />
      {enabled && (
        <datalist id={listId}>
          {tags.map(tag => (
            <option key={tag} value={tag} />
          ))}
        </datalist>
      )}
      <RegistryHint
        secretName={secretName}
        isLoading={isLoading}
        error={error as Error | null}
        count={tags.length}
        noun="tags"
      />
    </>
  )
}

/**
 * Says what the list is doing, including when it failed.
 *
 * A failure is shown rather than swallowed: an empty dropdown that might mean "nothing
 * here" and might mean "the call failed" leaves the user retyping a value that was never
 * going to be offered, with no idea why.
 */
function RegistryHint({
  secretName, isLoading, error, count, noun,
}: {
  secretName?: string
  isLoading: boolean
  error: Error | null
  count: number
  noun: string
}) {
  if (!secretName) return null

  if (isLoading) {
    return <p className="text-[11px] text-text-quaternary mt-1">Loading {noun}…</p>
  }
  if (error) {
    return (
      <p className="text-[11px] text-status-degraded mt-1">
        Could not list {noun}: {error.message}. You can still type a value.
      </p>
    )
  }
  if (count === 0) {
    return (
      <p className="text-[11px] text-text-quaternary mt-1">
        No {noun} visible through this credential. You can still type a value.
      </p>
    )
  }
  return (
    <p className="text-[11px] text-text-quaternary mt-1">
      {count} {noun} available — start typing to filter.
    </p>
  )
}
