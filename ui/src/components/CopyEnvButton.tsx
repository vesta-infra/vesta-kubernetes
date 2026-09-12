import { useEffect, useState } from 'react'
import { formatEnvContent } from '../lib/secretKeys'

type CopyEnvButtonProps = {
  values: Record<string, string>
  /** Overrides the resting label; the copied and failed states are fixed. */
  label?: string
  className?: string
}

/**
 * Copies a key/value map to the clipboard as .env text.
 *
 * navigator.clipboard is undefined outside a secure context, which for a self-hosted
 * Vesta reached over plain http on a LAN address is the normal case, not an edge one.
 * Fall back to the execCommand path there rather than throwing on click.
 */
async function writeToClipboard(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // Permission denied or a non-secure context that still exposed the API; fall through.
    }
  }

  const area = document.createElement('textarea')
  area.value = text
  // Keep it off-screen but still focusable — display:none would make execCommand a no-op.
  area.style.position = 'fixed'
  area.style.top = '-9999px'
  document.body.appendChild(area)
  area.select()
  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    document.body.removeChild(area)
  }
}

export default function CopyEnvButton({ values, label = 'Copy as .env', className = '' }: CopyEnvButtonProps) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const count = Object.keys(values).length

  useEffect(() => {
    if (state === 'idle') return
    const timer = setTimeout(() => setState('idle'), 2000)
    return () => clearTimeout(timer)
  }, [state])

  const handleCopy = async () => {
    const ok = await writeToClipboard(formatEnvContent(values))
    setState(ok ? 'copied' : 'failed')
  }

  return (
    <button
      type="button"
      onClick={handleCopy}
      disabled={count === 0}
      title={state === 'failed' ? 'Clipboard unavailable — this page must be served over HTTPS' : undefined}
      className={`text-xs transition-colors font-mono disabled:opacity-40 disabled:cursor-not-allowed ${
        state === 'failed' ? 'text-status-failed' : 'text-accent hover:text-accent-glow'
      } ${className}`}
    >
      {state === 'copied' ? `Copied ${count} line${count !== 1 ? 's' : ''}` : state === 'failed' ? 'Copy failed' : label}
    </button>
  )
}
