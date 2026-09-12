import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { useUserRole } from '../lib/useRole'

const DISMISSED_KEY = 'vesta-dismissed-update'

/**
 * Tells an admin a newer Vesta exists.
 *
 * Only admins see it: nobody else can act on it, and a banner you cannot dismiss by
 * acting is just noise. Dismissal is per-version and kept in localStorage, so dismissing
 * 0.6.4 does not also hide 0.7.0 when it lands.
 */
export default function UpdateBanner() {
  const isAdmin = useUserRole() === 'admin'
  const [dismissed, setDismissed] = useState(() => {
    try {
      return localStorage.getItem(DISMISSED_KEY) || ''
    } catch {
      return ''
    }
  })

  const { data } = useQuery({
    queryKey: ['updateStatus'],
    queryFn: () => api.getUpdateStatus(),
    // The server caches the result of its own daily poll, so this is a cheap read of a
    // settings row rather than a call out to GitHub.
    staleTime: 5 * 60 * 1000,
    enabled: isAdmin,
    retry: false,
  })

  if (!isAdmin || !data?.updateAvailable || dismissed === data.latest) return null

  const dismiss = () => {
    try {
      localStorage.setItem(DISMISSED_KEY, data.latest)
    } catch { /* a browser that refuses storage just gets the banner again */ }
    setDismissed(data.latest)
  }

  return (
    <div className="flex items-center gap-3 px-4 py-2.5 bg-accent/10 border border-accent/20 rounded-lg mb-4">
      <span className="text-xs text-text-primary">
        Vesta <span className="font-mono">{data.latest}</span> is available — you are running{' '}
        <span className="font-mono">{data.current}</span>.
      </span>
      <div className="ml-auto flex items-center gap-3">
        {data.releaseNotesUrl && (
          <a
            href={data.releaseNotesUrl}
            target="_blank"
            rel="noreferrer noopener"
            className="text-xs text-accent hover:text-accent-glow font-mono"
          >
            Release notes
          </a>
        )}
        <a href="/settings?tab=system" className="text-xs text-accent hover:text-accent-glow font-mono">
          Update
        </a>
        <button onClick={dismiss} className="text-xs text-text-tertiary hover:text-text-secondary font-mono">
          Dismiss
        </button>
      </div>
    </div>
  )
}
