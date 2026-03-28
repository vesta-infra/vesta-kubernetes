import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'

type View = 'login' | 'forgot' | 'reset'

export default function LoginPage() {
  const navigate = useNavigate()
  const [view, setView] = useState<View>('login')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [checking, setChecking] = useState(true)
  const [forgotAvailable, setForgotAvailable] = useState(false)

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
      localStorage.setItem('vesta-token', res.token)
      try {
        const user = await api.getCurrentUser()
        localStorage.setItem('vesta-user', JSON.stringify({ username: user.username, email: user.email, role: user.role }))
      } catch { /* non-critical */ }
      navigate('/')
    } catch (err: any) {
      setError(err.message || 'Login failed')
    } finally {
      setLoading(false)
    }
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
        <svg className="w-5 h-5 animate-spin text-accent" fill="none" viewBox="0 0 24 24">
          <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
          <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
        </svg>
      </div>
    )
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-surface-0 noise-bg relative overflow-hidden">
      <div className="absolute inset-0 overflow-hidden">
        <div className="absolute -top-1/2 -left-1/2 w-full h-full bg-accent/[0.03] rounded-full blur-3xl" />
        <div className="absolute -bottom-1/2 -right-1/2 w-full h-full bg-accent/[0.02] rounded-full blur-3xl" />
      </div>

      <div className="w-full max-w-sm relative z-10 animate-slide-up">
        <div className="text-center mb-10">
          <div className="inline-flex items-center justify-center w-12 h-12 rounded-2xl bg-accent/10 mb-5">
            <div className="w-4 h-4 rounded bg-accent" />
          </div>
          <h1 className="text-3xl font-display text-text-primary">Vesta</h1>
          <p className="text-xs font-mono text-text-tertiary uppercase tracking-[0.25em] mt-2">Kubernetes PaaS</p>
        </div>

        <div className="card p-7">
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

        <p className="text-center text-[11px] text-text-tertiary mt-8 font-mono">
          kubernetes.getvesta.sh
        </p>
      </div>
    </div>
  )
}
