import { useState } from 'react'
import { api } from '../lib/api'
import { getAssertion, isWebAuthnAvailable, describeWebAuthnError } from '../lib/webauthn'

type Props = {
  /** What the confirmation is for, shown so the user knows what they are approving. */
  action: string
  /** True when the account has a passkey, which makes it the quicker path. */
  hasPasskey: boolean
  onConfirmed: (grantId: string) => void
  onCancel: () => void
}

/**
 * Asks the user to prove they are still there before a change that weakens their account.
 *
 * A session says who logged in, not who is at the keyboard now. Without this step anyone
 * who finds an unlocked laptop can strip the second factor off the account, which would
 * make 2FA protect only the first login and nothing after it.
 *
 * The grant it returns is single-use and expires in five minutes.
 */
export default function ReauthPrompt({ action, hasPasskey, onConfirmed, onCancel }: Props) {
  const passkeyUsable = hasPasskey && isWebAuthnAvailable()
  const [mode, setMode] = useState<'passkey' | 'password'>(passkeyUsable ? 'passkey' : 'password')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const withPassword = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(''); setLoading(true)
    try {
      const grant = await api.reauthPassword(password)
      onConfirmed(grant.grantId)
    } catch (err: any) {
      setError(err.message || 'Incorrect password')
      setPassword('')
    } finally {
      setLoading(false)
    }
  }

  const withPasskey = async () => {
    setError(''); setLoading(true)
    try {
      const begin = await api.reauthPasskeyBegin()
      const assertion = await getAssertion(begin.publicKey)
      const grant = await api.reauthPasskeyFinish(begin.sessionId, assertion)
      onConfirmed(grant.grantId)
    } catch (err: any) {
      setError(describeWebAuthnError(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="bg-surface-1 border border-border rounded-lg p-4 space-y-3">
      <div>
        <p className="text-sm font-medium text-text-primary">Confirm it&apos;s you</p>
        <p className="text-[11px] text-text-tertiary mt-1">{action}</p>
      </div>

      {mode === 'passkey' ? (
        <button type="button" onClick={withPasskey} disabled={loading} className="btn-primary text-xs w-full">
          {loading ? 'Waiting for passkey...' : 'Use passkey'}
        </button>
      ) : (
        <form onSubmit={withPassword} className="space-y-2">
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Your password"
            autoFocus
            autoComplete="current-password"
            className="input-field text-sm"
          />
          <button type="submit" disabled={loading || !password} className="btn-primary text-xs w-full">
            {loading ? 'Confirming...' : 'Confirm'}
          </button>
        </form>
      )}

      {error && <p className="text-status-failed text-xs">{error}</p>}

      <div className="flex items-center justify-between text-xs">
        {passkeyUsable ? (
          <button
            type="button"
            onClick={() => { setMode(mode === 'passkey' ? 'password' : 'passkey'); setError('') }}
            className="text-accent hover:text-accent-glow font-mono"
          >
            {mode === 'passkey' ? 'Use password instead' : 'Use passkey instead'}
          </button>
        ) : <span />}
        <button type="button" onClick={onCancel} className="text-text-tertiary hover:text-text-secondary font-mono">
          Cancel
        </button>
      </div>
    </div>
  )
}
