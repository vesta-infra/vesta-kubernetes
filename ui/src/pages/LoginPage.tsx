import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import type { AuthResult } from '../lib/api'
import MFAChallenge from '../components/MFAChallenge'
import MFAEnrollment from '../components/MFAEnrollment'

type View = 'login' | 'forgot' | 'reset' | 'mfa' | 'mfa-enroll'

export default function LoginPage() {
  const navigate = useNavigate()
  const [view, setView] = useState<View>('login')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [checking, setChecking] = useState(true)
  const [forgotAvailable, setForgotAvailable] = useState(false)

  // Second-factor state. `pending` holds the short-lived token that reaches only the 2FA
  // endpoints; it is written to storage because the shared api client reads the bearer
  // token from there, and replaced by a real session the moment one is issued.
  const [mfaMethods, setMfaMethods] = useState<('totp' | 'webauthn' | 'backup')[]>([])
  const [totpAvailable, setTotpAvailable] = useState(true)
  const [enrollReason, setEnrollReason] = useState('')

  // Forgot password state
  const [forgotEmail, setForgotEmail] = useState('')
  const [forgotSent, setForgotSent] = useState(false)

  // Reset password state
  const [resetToken, setResetToken] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [resetSuccess, setResetSuccess] = useState(false)

  useEffect(() => {
    api.setupStatus()
      .then((res) => {
        if (res.needsSetup) navigate('/setup', { replace: true })
        else setChecking(false)
      })
      .catch(() => setChecking(false))

    api.forgotPasswordStatus()
      .then((res) => setForgotAvailable(res.available))
      .catch(() => {})
  }, [navigate])

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res = await api.login(username, password)

      // A correct password no longer implies a session. Both partial tokens are stored
      // the same way, but neither is accepted anywhere except the 2FA endpoints.
      localStorage.setItem('vesta-token', res.token)

      if (res.mfaRequired) {
        setMfaMethods(res.methods || [])
        setView('mfa')
        return
      }
      if (res.mfaEnrollmentRequired) {
        setTotpAvailable(res.totpAvailable !== false)
        setEnrollReason(res.reason || '')
        setView('mfa-enroll')
        return
      }

      await finishLogin()
    } catch (err: any) {
      setError(err.message || 'Login failed')
    } finally {
      setLoading(false)
    }
  }

  // Caches the profile the layout reads, then lands on the dashboard. Shared so the
  // password-only and two-factor paths cannot drift.
  const finishLogin = async () => {
    try {
      const user = await api.getCurrentUser()
      localStorage.setItem('vesta-user', JSON.stringify({ username: user.username, email: user.email, role: user.role }))
    } catch { /* non-critical */ }
    navigate('/')
  }

  const handleVerified = async (result: AuthResult) => {
    localStorage.setItem('vesta-token', result.token)
    await finishLogin()
  }

  // Enrolling under a mandatory policy does not itself produce a session -- the enroll
  // token cannot be upgraded in place -- so the user signs in again, this time through
  // the challenge they just set up.
  const handleEnrolled = () => {
    localStorage.removeItem('vesta-token')
    setPassword('')
    setView('login')
    setError('Two-factor authentication is set up. Sign in again to continue.')
  }

  const cancelMFA = () => {
    localStorage.removeItem('vesta-token')
    setPassword('')
    setError('')
    setView('login')
  }

  const handleForgot = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await api.forgotPassword(forgotEmail)
      setForgotSent(true)
    } catch (err: any) {
      setError(err.message || 'Failed to send reset email')
    } finally {
      setLoading(false)
    }
  }

  const handleReset = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    if (newPassword !== confirmPassword) {
      setError('Passwords do not match')
      return
    }
    setLoading(true)
    try {
      await api.resetPassword(resetToken, newPassword)
      setResetSuccess(true)
    } catch (err: any) {
      setError(err.message || 'Failed to reset password')
    } finally {
      setLoading(false)
    }
  }

  const switchView = (v: View) => {
    setView(v)
    setError('')
    setForgotSent(false)
    setResetSuccess(false)
  }

  if (checking) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-surface-0">
        <div className="relative">
          <div className="w-10 h-10 rounded-xl bg-accent/10 border border-accent/20 flex items-center justify-center animate-glow-pulse">
            <div className="w-3 h-3 rounded bg-accent" />
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-surface-0 noise-bg relative overflow-hidden">
      {/* Atmospheric background */}
      <div className="absolute inset-0 overflow-hidden">
        <div className="absolute top-[-30%] left-[-20%] w-[70%] h-[70%] bg-accent/[0.03] rounded-full blur-[120px] animate-float" />
        <div className="absolute bottom-[-20%] right-[-15%] w-[60%] h-[60%] bg-accent/[0.02] rounded-full blur-[100px] animate-float" style={{ animationDelay: '-4s' }} />
        <div className="absolute top-[20%] right-[10%] w-[30%] h-[30%] bg-blue-500/[0.015] rounded-full blur-[80px]" />
      </div>

      {/* Dot grid overlay */}
      <div className="absolute inset-0 dot-grid opacity-30" />

      <div className="w-full max-w-[380px] relative z-10 animate-slide-up px-4">
        {/* Brand */}
        <div className="text-center mb-12">
          <div className="inline-flex items-center justify-center w-14 h-14 rounded-2xl bg-accent/10 border border-accent/20 mb-6 shadow-glow animate-slide-up">
            <div className="w-5 h-5 rounded-md bg-accent shadow-[0_0_12px_rgba(245,158,11,0.5)]" />
          </div>
          <h1 className="text-4xl font-display italic text-text-primary animate-slide-up" style={{ animationDelay: '0.05s', animationFillMode: 'both' }}>Vesta</h1>
          <p className="text-[10px] font-mono text-text-tertiary uppercase tracking-[0.3em] mt-3 animate-slide-up" style={{ animationDelay: '0.1s', animationFillMode: 'both' }}>Kubernetes Platform</p>
        </div>

        <div className="card p-8 gradient-border animate-slide-up" style={{ animationDelay: '0.15s', animationFillMode: 'both' }}>
          {view === 'mfa' && (
            <MFAChallenge methods={mfaMethods} onVerified={handleVerified} onCancel={cancelMFA} />
          )}

          {view === 'mfa-enroll' && (
            <div className="space-y-4">
              <div>
                <h2 className="text-lg font-medium text-text-primary">Set up two-factor authentication</h2>
                <p className="text-xs text-text-tertiary mt-1">
                  {enrollReason || 'Your account requires a second factor before you can continue.'}
                </p>
              </div>
              {/* No onCancel: the policy is what makes this step mandatory. */}
              <MFAEnrollment totpAvailable={totpAvailable} onEnrolled={handleEnrolled} />
              <button type="button" onClick={cancelMFA}
                      className="text-xs text-text-tertiary hover:text-text-secondary font-mono">
                Sign in as someone else
              </button>
            </div>
          )}

          {view === 'login' && (
            <form onSubmit={handleLogin} className="space-y-5">
              {error && (
                <div className="bg-status-failed-bg border border-status-failed/20 text-status-failed text-sm px-4 py-3 rounded-lg">
                  {error}
                </div>
              )}
              <div>
                <label className="label">Username</label>
                <input
                  type="text"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  className="input-field"
                  required
                  autoFocus
                />
              </div>
              <div>
                <label className="label">Password</label>
                <input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  className="input-field"
                  required
                />
              </div>
              <button type="submit" disabled={loading} className="btn-primary w-full">
                {loading ? (
                  <span className="flex items-center justify-center gap-2">
                    <svg className="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                    </svg>
                    Signing in...
                  </span>
                ) : (
                  'Sign in'
                )}
              </button>
              {forgotAvailable && (
                <button
                  type="button"
                  onClick={() => switchView('forgot')}
                  className="w-full text-center text-xs text-text-tertiary hover:text-accent transition-colors"
                >
                  Forgot password?
                </button>
              )}
            </form>
          )}

          {view === 'forgot' && (
            <div className="space-y-5">
              <div>
                <h2 className="text-sm font-semibold text-text-primary">Reset Password</h2>
                <p className="text-xs text-text-tertiary mt-1">Enter your email to receive a reset code.</p>
              </div>
              {error && (
                <div className="bg-status-failed-bg border border-status-failed/20 text-status-failed text-sm px-4 py-3 rounded-lg">
                  {error}
                </div>
              )}
              {forgotSent ? (
                <div className="space-y-4">
                  <div className="bg-status-running-bg border border-status-running/20 text-status-running text-sm px-4 py-3 rounded-lg">
                    If an account with that email exists, a reset code has been sent.
                  </div>
                  <button
                    onClick={() => switchView('reset')}
                    className="btn-primary w-full"
                  >
                    I have a reset code
                  </button>
                </div>
              ) : (
                <form onSubmit={handleForgot} className="space-y-5">
                  <div>
                    <label className="label">Email</label>
                    <input
                      type="email"
                      value={forgotEmail}
                      onChange={(e) => setForgotEmail(e.target.value)}
                      className="input-field"
                      required
                      autoFocus
                      placeholder="you@example.com"
                    />
                  </div>
                  <button type="submit" disabled={loading} className="btn-primary w-full">
                    {loading ? 'Sending...' : 'Send Reset Code'}
                  </button>
                </form>
              )}
              <button
                type="button"
                onClick={() => switchView('login')}
                className="w-full text-center text-xs text-text-tertiary hover:text-accent transition-colors"
              >
                &larr; Back to sign in
              </button>
            </div>
          )}

          {view === 'reset' && (
            <div className="space-y-5">
              <div>
                <h2 className="text-sm font-semibold text-text-primary">Set New Password</h2>
                <p className="text-xs text-text-tertiary mt-1">Enter the code from your email and your new password.</p>
              </div>
              {error && (
                <div className="bg-status-failed-bg border border-status-failed/20 text-status-failed text-sm px-4 py-3 rounded-lg">
                  {error}
                </div>
              )}
              {resetSuccess ? (
                <div className="space-y-4">
                  <div className="bg-status-running-bg border border-status-running/20 text-status-running text-sm px-4 py-3 rounded-lg">
                    Password has been reset successfully.
                  </div>
                  <button
                    onClick={() => switchView('login')}
                    className="btn-primary w-full"
                  >
                    Sign in
                  </button>
                </div>
              ) : (
                <form onSubmit={handleReset} className="space-y-5">
                  <div>
                    <label className="label">Reset Code</label>
                    <input
                      type="text"
                      value={resetToken}
                      onChange={(e) => setResetToken(e.target.value)}
                      className="input-field font-mono text-xs"
                      required
                      autoFocus
                      placeholder="vst_..."
                    />
                  </div>
                  <div>
                    <label className="label">New Password</label>
                    <input
                      type="password"
                      value={newPassword}
                      onChange={(e) => setNewPassword(e.target.value)}
                      className="input-field"
                      required
                      minLength={8}
                    />
                  </div>
                  <div>
                    <label className="label">Confirm Password</label>
                    <input
                      type="password"
                      value={confirmPassword}
                      onChange={(e) => setConfirmPassword(e.target.value)}
                      className="input-field"
                      required
                      minLength={8}
                    />
                  </div>
                  <button type="submit" disabled={loading} className="btn-primary w-full">
                    {loading ? 'Resetting...' : 'Reset Password'}
                  </button>
                </form>
              )}
              <button
                type="button"
                onClick={() => switchView('login')}
                className="w-full text-center text-xs text-text-tertiary hover:text-accent transition-colors"
              >
                &larr; Back to sign in
              </button>
            </div>
          )}
        </div>

        <p className="text-center text-[10px] text-text-tertiary/40 mt-10 font-mono tracking-wider">
          kubernetes.getvesta.sh
        </p>
      </div>
    </div>
  )
}
