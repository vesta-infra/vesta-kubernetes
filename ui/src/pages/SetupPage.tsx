import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'

export default function SetupPage() {
  const navigate = useNavigate()
  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [teamName, setTeamName] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [checking, setChecking] = useState(true)

  useEffect(() => {
    api.setupStatus()
      .then((res) => {
        if (!res.needsSetup) navigate('/', { replace: true })
        else setChecking(false)
      })
      .catch(() => setChecking(false))
  }, [navigate])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    if (password !== confirmPassword) {
      setError('Passwords do not match')
      return
    }
    if (password.length < 8) {
      setError('Password must be at least 8 characters')
      return
    }

    setLoading(true)
    try {
      const res = await api.setup({ username, email, password, teamName })
      localStorage.setItem('vesta-token', res.token)
      try {
        const user = await api.getCurrentUser()
        localStorage.setItem('vesta-user', JSON.stringify({ username: user.username, email: user.email, role: user.role }))
      } catch {
        localStorage.setItem('vesta-user', JSON.stringify({ username, email, role: 'admin' }))
      }
      navigate('/')
    } catch (err: any) {
      setError(err.message || 'Setup failed')
    } finally {
      setLoading(false)
    }
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
      <div className="absolute inset-0 overflow-hidden">
        <div className="absolute top-[-30%] left-[-20%] w-[70%] h-[70%] bg-accent/[0.03] rounded-full blur-[120px] animate-float" />
        <div className="absolute bottom-[-20%] right-[-15%] w-[60%] h-[60%] bg-accent/[0.02] rounded-full blur-[100px] animate-float" style={{ animationDelay: '-4s' }} />
      </div>

      <div className="absolute inset-0 dot-grid opacity-30" />

      <div className="w-full max-w-md relative z-10 animate-slide-up px-4">
        <div className="text-center mb-12">
          <div className="inline-flex items-center justify-center w-14 h-14 rounded-2xl bg-accent/10 border border-accent/20 mb-6 shadow-glow">
            <div className="w-5 h-5 rounded-md bg-accent shadow-[0_0_12px_rgba(245,158,11,0.5)]" />
          </div>
          <h1 className="text-4xl font-display italic text-text-primary">Welcome to Vesta</h1>
          <p className="text-[10px] font-mono text-text-tertiary uppercase tracking-[0.3em] mt-3">Initial Setup</p>
        </div>

        <div className="card p-8 gradient-border">
          <form onSubmit={handleSubmit} className="space-y-5">
            {error && (
              <div className="bg-status-failed-bg border border-status-failed/20 text-status-failed text-sm px-4 py-3 rounded-lg">
                {error}
              </div>
            )}

            <div>
              <label className="label">Admin Username</label>
              <input
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className="input-field"
                required
                autoFocus
                placeholder="admin"
              />
            </div>

            <div>
              <label className="label">Email</label>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="input-field"
                required
                placeholder="admin@example.com"
              />
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="label">Password</label>
                <input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
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
                />
              </div>
            </div>

            <div className="pt-2 border-t border-border-subtle">
              <label className="label">Team Name</label>
              <input
                type="text"
                value={teamName}
                onChange={(e) => setTeamName(e.target.value)}
                className="input-field"
                required
                placeholder="my-team"
              />
              <p className="text-[11px] text-text-tertiary mt-1.5">
                Your first team. You can create more later.
              </p>
            </div>

            <button type="submit" disabled={loading} className="btn-primary w-full">
              {loading ? (
                <span className="flex items-center justify-center gap-2">
                  <svg className="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
                    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                  </svg>
                  Setting up...
                </span>
              ) : (
                'Complete Setup'
              )}
            </button>
          </form>
        </div>

        <p className="text-center text-[10px] text-text-tertiary/40 mt-10 font-mono tracking-wider">
          kubernetes.getvesta.sh
        </p>
      </div>
    </div>
  )
}
