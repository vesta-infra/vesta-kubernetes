import { useState } from 'react'
import { api } from '../lib/api'
import { createCredential, isWebAuthnAvailable, describeWebAuthnError } from '../lib/webauthn'
import { BackupCodes } from './MFAChallenge'

type Props = {
  totpAvailable: boolean
  /** Called once a factor is registered and any recovery codes have been acknowledged. */
  onEnrolled: () => void
  /** Absent when enrollment is mandatory, which is what removes the way out. */
  onCancel?: () => void
  /**
   * Single-use proof of identity, required only when the account already holds a factor.
   * Undefined for a first enrollment and for the mandatory-enrollment step at login.
   */
  reauthGrant?: string
  /**
   * Raised when the server rejects the grant -- spent on an attempt that was cancelled,
   * or simply expired. The parent collects a fresh one; retrying with the same grant
   * would fail again, since a grant is only ever good once.
   */
  onReauthRequired?: () => void
}

/**
 * Registers a first second-factor.
 *
 * Used from two places with the same code: the Settings screen, where enrolling is
 * optional, and the forced-enrollment step of login, where it is not. The only difference
 * is whether onCancel is supplied.
 */
export default function MFAEnrollment({ totpAvailable, onEnrolled, onCancel, reauthGrant, onReauthRequired }: Props) {
  const passkeysAvailable = isWebAuthnAvailable()
  const [method, setMethod] = useState<'choose' | 'totp' | 'passkey'>('choose')
  const [enrollment, setEnrollment] = useState<{ secret: string; qrDataUri: string } | null>(null)
  const [code, setCode] = useState('')
  const [passkeyName, setPasskeyName] = useState('')
  const [backupCodes, setBackupCodes] = useState<string[] | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  // The API marks these with details "reauth_required"; the message is what surfaces
  // through the shared fetch helper, so match on that.
  const isReauthFailure = (err: any) => /confirm your identity|expired or was already used/i.test(err?.message || '')

  const handleFailure = (err: any, fallback: string) => {
    if (isReauthFailure(err) && onReauthRequired) {
      onReauthRequired()
      return
    }
    setError(err?.message || fallback)
  }

  const startTOTP = async () => {
    setError(''); setLoading(true)
    try {
      const res = await api.totpEnroll(reauthGrant)
      setEnrollment({ secret: res.secret, qrDataUri: res.qrDataUri })
      setMethod('totp')
    } catch (err: any) {
      handleFailure(err, 'Could not start enrollment')
    } finally {
      setLoading(false)
    }
  }

  const confirmTOTP = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(''); setLoading(true)
    try {
      const res = await api.totpConfirm(code.trim())
      setBackupCodes(res.backupCodes)
    } catch (err: any) {
      setError(err.message || 'That code is not correct')
      setCode('')
    } finally {
      setLoading(false)
    }
  }

  const registerPasskey = async () => {
    setError(''); setLoading(true)
    try {
      const begin = await api.passkeyRegisterBegin(reauthGrant)
      const credential = await createCredential(begin.publicKey)
      const res = await api.passkeyRegisterFinish(begin.sessionId, passkeyName.trim() || 'Passkey', credential)
      // Codes only come back for the first factor; a second one must not invalidate
      // codes the user has already written down.
      if (res.backupCodes?.length) setBackupCodes(res.backupCodes)
      else onEnrolled()
    } catch (err: any) {
      if (isReauthFailure(err) && onReauthRequired) onReauthRequired()
      else setError(describeWebAuthnError(err))
    } finally {
      setLoading(false)
    }
  }

  if (backupCodes) {
    return <BackupCodes codes={backupCodes} onDone={onEnrolled} />
  }

  return (
    <div className="space-y-4">
      {method === 'choose' && (
        <div className="space-y-3">
          {passkeysAvailable && (
            <button type="button" onClick={() => setMethod('passkey')} className="btn-outline w-full text-left">
              <span className="block text-sm text-text-primary">Passkey</span>
              <span className="block text-[11px] text-text-tertiary mt-0.5">
                Touch ID, Windows Hello, or a security key. Nothing to type.
              </span>
            </button>
          )}
          {totpAvailable && (
            <button type="button" onClick={startTOTP} disabled={loading} className="btn-outline w-full text-left">
              <span className="block text-sm text-text-primary">Authenticator app</span>
              <span className="block text-[11px] text-text-tertiary mt-0.5">
                A six-digit code from 1Password, Authy, Google Authenticator and the like.
              </span>
            </button>
          )}
          {!passkeysAvailable && !totpAvailable && (
            <p className="text-xs text-status-failed">
              No second factor can be enrolled here. Passkeys need HTTPS, and authenticator apps need this
              instance to have an encryption key configured.
            </p>
          )}
          {!passkeysAvailable && totpAvailable && (
            <p className="text-[11px] text-text-tertiary">Passkeys are unavailable because this page is not served over HTTPS.</p>
          )}
        </div>
      )}

      {method === 'totp' && enrollment && (
        <form onSubmit={confirmTOTP} className="space-y-3">
          <p className="text-xs text-text-tertiary">Scan this with your authenticator app, then enter the code it shows.</p>
          {enrollment.qrDataUri && (
            <img src={enrollment.qrDataUri} alt="Authenticator QR code" width={200} height={200}
                 className="rounded-lg bg-white p-2" />
          )}
          <div>
            <label className="label">Or enter this key manually</label>
            <code className="block text-xs font-mono text-text-secondary bg-surface-1 border border-border rounded-lg px-3 py-2 break-all select-all">
              {enrollment.secret}
            </code>
          </div>
          <input
            value={code}
            onChange={(e) => setCode(e.target.value)}
            placeholder="123456"
            autoComplete="one-time-code"
            className="input-field font-mono tracking-[0.3em] text-center"
          />
          <button type="submit" disabled={loading || !code.trim()} className="btn-primary w-full">
            {loading ? 'Verifying...' : 'Confirm'}
          </button>
        </form>
      )}

      {method === 'passkey' && (
        <div className="space-y-3">
          <div>
            <label className="label">Name this passkey</label>
            <input
              value={passkeyName}
              onChange={(e) => setPasskeyName(e.target.value)}
              placeholder="MacBook Touch ID"
              className="input-field text-sm"
            />
            <p className="text-[11px] text-text-tertiary mt-1">So you can tell it apart from other devices later.</p>
          </div>
          <button type="button" onClick={registerPasskey} disabled={loading} className="btn-primary w-full">
            {loading ? 'Waiting for passkey...' : 'Create passkey'}
          </button>
        </div>
      )}

      {error && <p className="text-status-failed text-xs">{error}</p>}

      <div className="flex items-center justify-between text-xs">
        {method !== 'choose' ? (
          <button type="button" onClick={() => { setMethod('choose'); setError(''); setEnrollment(null) }}
                  className="text-accent hover:text-accent-glow font-mono">
            Choose another method
          </button>
        ) : <span />}
        {onCancel && (
          <button type="button" onClick={onCancel} className="text-text-tertiary hover:text-text-secondary font-mono">
            Cancel
          </button>
        )}
      </div>
    </div>
  )
}
