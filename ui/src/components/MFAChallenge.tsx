import { useState } from 'react'
import { api } from '../lib/api'
import type { AuthResult } from '../lib/api'
import { getAssertion, isWebAuthnAvailable, describeWebAuthnError } from '../lib/webauthn'

type Props = {
  methods: ('totp' | 'webauthn' | 'backup')[]
  onVerified: (result: AuthResult) => void
  onCancel: () => void
}

/**
 * The second step of login: exchange a factor for a real session.
 *
 * Reached holding an mfa_challenge token, which the server accepts on these endpoints and
 * nowhere else, so there is nothing useful to do here except finish or start over.
 */
export default function MFAChallenge({ methods, onVerified, onCancel }: Props) {
  const hasPasskey = methods.includes('webauthn') && isWebAuthnAvailable()
  const hasCode = methods.includes('totp')

  // Default to whichever factor needs the least from the user. A passkey is one tap; a
  // code means fetching a phone.
  const [mode, setMode] = useState<'passkey' | 'code'>(hasPasskey ? 'passkey' : 'code')
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const submitCode = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      onVerified(await api.mfaVerify(code.trim()))
    } catch (err: any) {
      setError(err.message || 'Verification failed')
      setCode('')
    } finally {
      setLoading(false)
    }
  }

  const usePasskey = async () => {
    setError('')
    setLoading(true)
    try {
      const begin = await api.passkeyAuthBegin()
      const assertion = await getAssertion(begin.publicKey)
      onVerified(await api.passkeyAuthFinish(begin.sessionId, assertion))
    } catch (err: any) {
      setError(describeWebAuthnError(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-medium text-text-primary">Two-factor authentication</h2>
        <p className="text-xs text-text-tertiary mt-1">
          {mode === 'passkey'
            ? 'Use your passkey to finish signing in.'
            : 'Enter the code from your authenticator app, or one of your recovery codes.'}
        </p>
      </div>

      {mode === 'passkey' ? (
        <button type="button" onClick={usePasskey} disabled={loading} className="btn-primary w-full">
          {loading ? 'Waiting for passkey...' : 'Use passkey'}
        </button>
      ) : (
        <form onSubmit={submitCode} className="space-y-3">
          <input
            value={code}
            onChange={(e) => setCode(e.target.value)}
            placeholder="123456"
            autoFocus
            autoComplete="one-time-code"
            spellCheck={false}
            className="input-field font-mono tracking-[0.3em] text-center text-lg"
          />
          <button type="submit" disabled={loading || !code.trim()} className="btn-primary w-full">
            {loading ? 'Verifying...' : 'Verify'}
          </button>
        </form>
      )}

      {error && <p className="text-status-failed text-xs">{error}</p>}

      <div className="flex items-center justify-between text-xs">
        {hasPasskey && hasCode ? (
          <button
            type="button"
            onClick={() => { setMode(mode === 'passkey' ? 'code' : 'passkey'); setError('') }}
            className="text-accent hover:text-accent-glow font-mono"
          >
            {mode === 'passkey' ? 'Use a code instead' : 'Use passkey instead'}
          </button>
        ) : <span />}
        <button type="button" onClick={onCancel} className="text-text-tertiary hover:text-text-secondary font-mono">
          Back to sign in
        </button>
      </div>

      {mode === 'code' && !hasCode && (
        <p className="text-[11px] text-text-tertiary">
          No authenticator app is enrolled on this account — enter a recovery code.
        </p>
      )}
    </div>
  )
}

/** Shown once, immediately after a factor is enrolled. These cannot be retrieved later. */
export function BackupCodes({ codes, onDone }: { codes: string[]; onDone?: () => void }) {
  const [copied, setCopied] = useState(false)

  const copy = () => {
    navigator.clipboard?.writeText(codes.join('\n'))
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  const download = () => {
    const blob = new Blob([`Vesta recovery codes\n\nEach code works once.\n\n${codes.join('\n')}\n`], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = 'vesta-recovery-codes.txt'
    link.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="bg-surface-1 border border-border rounded-lg p-4 space-y-3">
      <div>
        <p className="text-sm font-medium text-text-primary">Save your recovery codes</p>
        <p className="text-[11px] text-text-tertiary mt-1">
          Each code works once and gets you in if you lose your device. This is the only time they are shown.
        </p>
      </div>

      <div className="grid grid-cols-2 gap-x-6 gap-y-1 font-mono text-xs text-text-secondary select-all">
        {codes.map((c) => <span key={c}>{c}</span>)}
      </div>

      <div className="flex items-center gap-3">
        <button type="button" onClick={copy} className="btn-outline text-xs">{copied ? 'Copied' : 'Copy'}</button>
        <button type="button" onClick={download} className="btn-outline text-xs">Download</button>
        {onDone && <button type="button" onClick={onDone} className="btn-primary text-xs ml-auto">I have saved them</button>}
      </div>
    </div>
  )
}
