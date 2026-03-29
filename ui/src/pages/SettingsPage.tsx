import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

export default function SettingsPage() {
  const [activeTab, setActiveTab] = useState<'general' | 'teams' | 'users' | 'roles'>('general')

  const tabs = [
    { key: 'general' as const, label: 'General' },
    { key: 'teams' as const, label: 'Teams' },
    { key: 'users' as const, label: 'Users' },
    { key: 'roles' as const, label: 'Roles' },
  ]

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-display italic text-text-primary">Settings</h2>

      <div className="flex border-b border-border">
        {tabs.map((tab) => (
          <button
            key={tab.key}
            onClick={() => setActiveTab(tab.key)}
            className={`px-5 py-3 text-xs font-mono tracking-wider uppercase transition-colors ${
              activeTab === tab.key
                ? 'text-accent border-b-2 border-accent'
                : 'text-text-tertiary hover:text-text-secondary'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {activeTab === 'general' && (
        <div className="space-y-8">
          <ProfileSection />
          <ChangePasswordSection />
          <APIKeysSection />
        </div>
      )}

      {activeTab === 'teams' && (
        <div className="space-y-8">
          <TeamsSection />
        </div>
      )}

      {activeTab === 'users' && (
        <div className="space-y-8">
          <UserManagementSection />
        </div>
      )}

      {activeTab === 'roles' && (
        <div className="space-y-8">
          <RolesSection />
        </div>
      )}
    </div>
  )
}

function ProfileSection() {
  const queryClient = useQueryClient()
  const { data: user, isLoading } = useQuery({
    queryKey: ['currentUser'],
    queryFn: () => api.getCurrentUser(),
  })

  const [displayName, setDisplayName] = useState('')
  const [email, setEmail] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    if (user) {
      setDisplayName(user.displayName || '')
      setEmail(user.email || '')
    }
  }, [user])

  const mutation = useMutation({
    mutationFn: () => api.updateProfile({ displayName, email }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['currentUser'] })
      const stored = localStorage.getItem('vesta-user')
      if (stored) {
        try {
          const parsed = JSON.parse(stored)
          parsed.email = email
          localStorage.setItem('vesta-user', JSON.stringify(parsed))
        } catch { /* ignore */ }
      }
      setSaved(true)
      setTimeout(() => setSaved(false), 3000)
    },
  })

  if (isLoading) return <Spinner />

  return (
    <section className="card p-6">
      <h3 className="section-title mb-5">Profile</h3>
      <form
        onSubmit={(e) => { e.preventDefault(); mutation.mutate() }}
        className="space-y-4 max-w-lg"
      >
        <div>
          <label className="label">Username</label>
          <input value={user?.username || ''} className="input-field opacity-60 cursor-not-allowed" disabled />
        </div>
        <div>
          <label className="label">Display Name</label>
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            className="input-field"
            placeholder="Your display name"
          />
        </div>
        <div>
          <label className="label">Email</label>
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="input-field"
          />
        </div>
        <div className="flex items-center gap-3">
          <button type="submit" disabled={mutation.isPending} className="btn-primary">
            {mutation.isPending ? 'Saving...' : 'Save Changes'}
          </button>
          {saved && <span className="text-xs text-status-running">Saved</span>}
          {mutation.isError && (
            <span className="text-xs text-status-failed">{(mutation.error as Error).message}</span>
          )}
        </div>
      </form>
    </section>
  )
}

function ChangePasswordSection() {
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [success, setSuccess] = useState(false)

  const mutation = useMutation({
    mutationFn: () => api.changePassword({ currentPassword, newPassword }),
    onSuccess: () => {
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
      setSuccess(true)
      setError('')
      setTimeout(() => setSuccess(false), 3000)
    },
    onError: (err: Error) => {
      setError(err.message)
      setSuccess(false)
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    if (newPassword !== confirmPassword) {
      setError('Passwords do not match')
      return
    }
    if (newPassword.length < 8) {
      setError('Password must be at least 8 characters')
      return
    }
    mutation.mutate()
  }

  return (
    <section className="card p-6">
      <h3 className="section-title mb-5">Change Password</h3>
      <form onSubmit={handleSubmit} className="space-y-4 max-w-lg">
        <div>
          <label className="label">Current Password</label>
          <input
            type="password"
            value={currentPassword}
            onChange={(e) => setCurrentPassword(e.target.value)}
            className="input-field"
            required
          />
        </div>
        <div className="grid grid-cols-2 gap-4">
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
            <label className="label">Confirm New Password</label>
            <input
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              className="input-field"
              required
            />
          </div>
        </div>
        {error && (
          <p className="text-status-failed text-xs">{error}</p>
        )}
        <div className="flex items-center gap-3">
          <button type="submit" disabled={mutation.isPending} className="btn-primary">
            {mutation.isPending ? 'Changing...' : 'Change Password'}
          </button>
          {success && <span className="text-xs text-status-running">Password changed</span>}
        </div>
      </form>
    </section>
  )
}

function TeamsSection() {
  const queryClient = useQueryClient()
  const { data: teams, isLoading } = useQuery({ queryKey: ['teams'], queryFn: () => api.listTeams() })
  const [showCreate, setShowCreate] = useState(false)
  const [expandedTeam, setExpandedTeam] = useState<string | null>(null)

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deleteTeam(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['teams'] }),
  })

  return (
    <section className="card p-6">
      <div className="flex items-center justify-between mb-5">
        <h3 className="section-title">Teams</h3>
        <button onClick={() => setShowCreate(!showCreate)} className="btn-ghost text-xs">
          <span className="flex items-center gap-1.5">
            <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
            </svg>
            Create Team
          </span>
        </button>
      </div>

      {showCreate && <CreateTeamForm onClose={() => setShowCreate(false)} />}

      {isLoading && <Spinner />}

      {!isLoading && (!teams?.items || teams.items.length === 0) && (
        <p className="text-sm text-text-tertiary">No teams yet</p>
      )}

      <div className="space-y-2">
        {teams?.items?.map((team: any) => (
          <div key={team.id}>
            <div
              className="card-hover flex items-center justify-between px-5 py-4 group cursor-pointer"
              onClick={() => setExpandedTeam(expandedTeam === team.id ? null : team.id)}
            >
              <div className="flex items-center gap-4">
                <div className="w-9 h-9 rounded-lg bg-surface-3 flex items-center justify-center">
                  <svg className="w-4 h-4 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z" />
                  </svg>
                </div>
                <div>
                  <p className="text-sm font-medium text-text-primary">{team.displayName || team.name}</p>
                  <p className="text-xs text-text-tertiary font-mono mt-0.5">{team.name}</p>
                </div>
              </div>
              <div className="flex items-center gap-4">
                <span className="text-xs font-mono text-text-tertiary">
                  {team.memberCount ?? team.members?.length ?? 0} member{(team.memberCount ?? team.members?.length ?? 0) !== 1 ? 's' : ''}
                </span>
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    if (confirm(`Delete team "${team.name}"?`))
                      deleteMutation.mutate(team.id)
                  }}
                  className="text-xs text-text-tertiary hover:text-status-failed transition-colors opacity-0 group-hover:opacity-100"
                >
                  Delete
                </button>
                <svg
                  className={`w-4 h-4 text-text-tertiary transition-transform ${expandedTeam === team.id ? 'rotate-180' : ''}`}
                  fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
                >
                  <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
                </svg>
              </div>
            </div>

            {expandedTeam === team.id && (
              <TeamMembers teamId={team.id} />
            )}
          </div>
        ))}
      </div>
    </section>
  )
}

function CreateTeamForm({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [displayName, setDisplayName] = useState('')

  const mutation = useMutation({
    mutationFn: () => api.createTeam({ name, displayName: displayName || undefined }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['teams'] })
      onClose()
    },
  })

  return (
    <form
      onSubmit={(e) => { e.preventDefault(); mutation.mutate() }}
      className="bg-surface-1 border border-border rounded-lg p-4 space-y-4 animate-slide-up mb-4"
    >
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="label">Name</label>
          <input value={name} onChange={(e) => setName(e.target.value)} className="input-field" required placeholder="engineering" />
        </div>
        <div>
          <label className="label">Display Name</label>
          <input value={displayName} onChange={(e) => setDisplayName(e.target.value)} className="input-field" placeholder="Engineering Team" />
        </div>
      </div>
      <div className="flex gap-3">
        <button type="submit" disabled={mutation.isPending} className="btn-primary">
          {mutation.isPending ? 'Creating...' : 'Create Team'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
      </div>
      {mutation.isError && (
        <p className="text-status-failed text-xs">{(mutation.error as Error).message}</p>
      )}
    </form>
  )
}

function TeamMembers({ teamId }: { teamId: string }) {
  const queryClient = useQueryClient()
  const [userId, setUserId] = useState('')
  const [role, setRole] = useState('developer')

  const { data: team, isLoading: teamLoading } = useQuery({
    queryKey: ['team', teamId],
    queryFn: () => api.getTeam(teamId),
  })

  const { data: users } = useQuery({
    queryKey: ['users'],
    queryFn: () => api.listUsers(),
  })

  const members = team?.members || []
  const memberIds = new Set(members.map((m: any) => m.userId || m.id))
  const availableUsers = (users?.items || []).filter((u: any) => !memberIds.has(u.id))

  const addMutation = useMutation({
    mutationFn: () => api.addTeamMember(teamId, { userId, role }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['team', teamId] })
      queryClient.invalidateQueries({ queryKey: ['teams'] })
      setUserId('')
      setRole('developer')
    },
  })

  const removeMutation = useMutation({
    mutationFn: (uid: string) => api.removeTeamMember(teamId, uid),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['team', teamId] })
      queryClient.invalidateQueries({ queryKey: ['teams'] })
    },
  })

  return (
    <div className="ml-14 mr-5 mb-3 bg-surface-1 border border-border rounded-lg p-4 animate-slide-up">
      {teamLoading && <Spinner />}

      {!teamLoading && (
        <>
      <p className="text-xs font-semibold text-text-secondary uppercase tracking-wider mb-3">Members</p>

      {members.length === 0 && (
        <p className="text-xs text-text-tertiary mb-3">No members</p>
      )}

      <div className="space-y-1.5 mb-4">
        {members.map((m: any) => (
          <div key={m.userId || m.id} className="flex items-center justify-between py-1.5 group">
            <div className="flex items-center gap-2">
              <span className="text-sm text-text-primary">{m.username || m.userId || m.id}</span>
              <span className="text-[11px] font-mono bg-surface-3 text-text-tertiary px-2 py-0.5 rounded">{m.role}</span>
            </div>
            <button
              onClick={() => removeMutation.mutate(m.userId || m.id)}
              className="text-xs text-text-tertiary hover:text-status-failed transition-colors opacity-0 group-hover:opacity-100"
            >
              Remove
            </button>
          </div>
        ))}
      </div>

      <form
        onSubmit={(e) => { e.preventDefault(); addMutation.mutate() }}
        className="flex items-end gap-3"
      >
        <div className="flex-1">
          <label className="label">User</label>
          <select value={userId} onChange={(e) => setUserId(e.target.value)} className="input-field" required>
            <option value="">Select user...</option>
            {availableUsers.map((u: any) => (
              <option key={u.id} value={u.id}>
                {u.displayName || u.username}{u.email ? ` (${u.email})` : ''}
              </option>
            ))}
          </select>
        </div>
        <div className="w-36">
          <label className="label">Role</label>
          <select value={role} onChange={(e) => setRole(e.target.value)} className="input-field">
            <option value="admin">Admin</option>
            <option value="developer">Developer</option>
            <option value="viewer">Viewer</option>
          </select>
        </div>
        <button type="submit" disabled={addMutation.isPending || !userId} className="btn-primary whitespace-nowrap">
          {addMutation.isPending ? 'Adding...' : 'Add'}
        </button>
      </form>
      {addMutation.isError && (
        <p className="text-status-failed text-xs mt-2">{(addMutation.error as Error).message}</p>
      )}
        </>
      )}
    </div>
  )
}

function APIKeysSection() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [createdToken, setCreatedToken] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  const { data: tokens, isLoading } = useQuery({
    queryKey: ['apiTokens'],
    queryFn: () => api.listTokens(),
  })

  const revokeMutation = useMutation({
    mutationFn: (id: string) => api.revokeToken(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['apiTokens'] }),
  })

  const copyToken = () => {
    if (createdToken) {
      navigator.clipboard.writeText(createdToken)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  return (
    <section className="card p-6">
      <div className="flex items-center justify-between mb-5">
        <div>
          <h3 className="section-title">API Keys</h3>
          <p className="text-xs text-text-tertiary mt-1">Create keys to trigger deployments via CI/CD or scripts</p>
        </div>
        <button onClick={() => { setShowCreate(!showCreate); setCreatedToken(null) }} className="btn-ghost text-xs">
          <span className="flex items-center gap-1.5">
            <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
            </svg>
            Create Key
          </span>
        </button>
      </div>

      {createdToken && (
        <div className="bg-status-running/10 border border-status-running/20 rounded-lg p-4 mb-4 animate-slide-up">
          <p className="text-xs font-semibold text-status-running mb-2">API Key Created — copy it now, it won't be shown again</p>
          <div className="flex items-center gap-2">
            <code className="flex-1 text-xs font-mono bg-surface-3 text-text-primary px-3 py-2 rounded break-all select-all">{createdToken}</code>
            <button onClick={copyToken} className="btn-ghost text-xs shrink-0">
              {copied ? 'Copied!' : 'Copy'}
            </button>
          </div>
        </div>
      )}

      {showCreate && <CreateAPIKeyForm onClose={() => setShowCreate(false)} onCreated={(token) => { setCreatedToken(token); setShowCreate(false) }} />}

      {isLoading && <Spinner />}

      {!isLoading && (!tokens?.items || tokens.items.length === 0) && (
        <p className="text-sm text-text-tertiary">No API keys</p>
      )}

      <div className="space-y-2">
        {tokens?.items?.map((token: any) => (
          <div key={token.id} className="card-hover flex items-center justify-between px-5 py-4 group">
            <div className="flex items-center gap-4">
              <div className="w-9 h-9 rounded-lg bg-surface-3 flex items-center justify-center">
                <svg className="w-4 h-4 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
                </svg>
              </div>
              <div>
                <p className="text-sm font-medium text-text-primary">{token.name}</p>
                <div className="flex items-center gap-3 mt-1">
                  <div className="flex gap-1">
                    {(token.scopes || []).map((s: string) => (
                      <span key={s} className="text-[10px] font-mono bg-accent/10 text-accent px-1.5 py-0.5 rounded">{s}</span>
                    ))}
                  </div>
                  {token.lastUsedAt && (
                    <span className="text-[11px] text-text-tertiary">
                      Last used {new Date(token.lastUsedAt).toLocaleDateString()}
                    </span>
                  )}
                  {token.expiresAt && (
                    <span className={`text-[11px] ${new Date(token.expiresAt) < new Date() ? 'text-status-failed' : 'text-text-tertiary'}`}>
                      {new Date(token.expiresAt) < new Date() ? 'Expired' : `Expires ${new Date(token.expiresAt).toLocaleDateString()}`}
                    </span>
                  )}
                </div>
              </div>
            </div>
            <button
              onClick={() => {
                if (confirm(`Revoke API key "${token.name}"?`))
                  revokeMutation.mutate(token.id)
              }}
              className="text-xs text-text-tertiary hover:text-status-failed transition-colors opacity-0 group-hover:opacity-100"
            >
              Revoke
            </button>
          </div>
        ))}
      </div>
    </section>
  )
}

function CreateAPIKeyForm({ onClose, onCreated }: { onClose: () => void; onCreated: (token: string) => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState<string[]>(['deploy', 'read'])
  const [expiresIn, setExpiresIn] = useState('2160h') // 90 days

  const allScopes = ['deploy', 'read', 'write', 'admin']

  const toggleScope = (scope: string) => {
    setScopes((prev) =>
      prev.includes(scope) ? prev.filter((s) => s !== scope) : [...prev, scope]
    )
  }

  const mutation = useMutation({
    mutationFn: () => api.createToken({ name, scopes, expiresIn: expiresIn || undefined }),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['apiTokens'] })
      onCreated(data.token)
    },
  })

  return (
    <form
      onSubmit={(e) => { e.preventDefault(); mutation.mutate() }}
      className="bg-surface-1 border border-border rounded-lg p-4 space-y-4 animate-slide-up mb-4"
    >
      <div>
        <label className="label">Name</label>
        <input value={name} onChange={(e) => setName(e.target.value)} className="input-field" required placeholder="ci-deploy-key" />
      </div>
      <div>
        <label className="label">Scopes</label>
        <div className="flex gap-2 mt-1">
          {allScopes.map((scope) => (
            <button
              key={scope}
              type="button"
              onClick={() => toggleScope(scope)}
              className={`text-xs font-mono px-3 py-1.5 rounded-lg border transition-colors ${
                scopes.includes(scope)
                  ? 'bg-accent/10 border-accent/30 text-accent'
                  : 'bg-surface-3 border-border text-text-tertiary hover:text-text-secondary'
              }`}
            >
              {scope}
            </button>
          ))}
        </div>
        <p className="text-[11px] text-text-tertiary mt-2">
          <strong>deploy</strong> — trigger deployments, restarts, rollbacks &nbsp;
          <strong>read</strong> — read apps, projects &nbsp;
          <strong>write</strong> — create/update resources &nbsp;
          <strong>admin</strong> — full access
        </p>
      </div>
      <div>
        <label className="label">Expires In</label>
        <select value={expiresIn} onChange={(e) => setExpiresIn(e.target.value)} className="input-field w-48">
          <option value="720h">30 days</option>
          <option value="2160h">90 days</option>
          <option value="8760h">1 year</option>
          <option value="">Never</option>
        </select>
      </div>
      <div className="flex gap-3">
        <button type="submit" disabled={mutation.isPending || scopes.length === 0} className="btn-primary">
          {mutation.isPending ? 'Creating...' : 'Create API Key'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
      </div>
      {mutation.isError && (
        <p className="text-status-failed text-xs">{(mutation.error as Error).message}</p>
      )}
    </form>
  )
}

function UserManagementSection() {
  const queryClient = useQueryClient()
  const { data: users, isLoading } = useQuery({
    queryKey: ['users'],
    queryFn: () => api.listUsers(),
  })
  const [showRegister, setShowRegister] = useState(false)

  return (
    <section className="card p-6">
      <div className="flex items-center justify-between mb-5">
        <h3 className="section-title">Users</h3>
        <button onClick={() => setShowRegister(!showRegister)} className="btn-ghost text-xs">
          <span className="flex items-center gap-1.5">
            <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
            </svg>
            Add User
          </span>
        </button>
      </div>

      {showRegister && <RegisterUserForm onClose={() => { setShowRegister(false); queryClient.invalidateQueries({ queryKey: ['users'] }) }} />}

      {isLoading && <Spinner />}

      {!isLoading && (!users?.items || users.items.length === 0) && (
        <p className="text-sm text-text-tertiary">No users</p>
      )}

      <div className="space-y-2">
        {users?.items?.map((user: any) => (
          <div key={user.id} className="card-hover flex items-center justify-between px-5 py-4">
            <div className="flex items-center gap-4">
              <div className="w-9 h-9 rounded-lg bg-surface-3 flex items-center justify-center">
                <span className="text-sm font-semibold text-text-tertiary uppercase">{(user.username || '?')[0]}</span>
              </div>
              <div>
                <p className="text-sm font-medium text-text-primary">{user.displayName || user.username}</p>
                <p className="text-xs text-text-tertiary mt-0.5">{user.email}</p>
              </div>
            </div>
            <span className={`text-[11px] font-mono px-2 py-0.5 rounded ${
              user.role === 'admin' ? 'bg-accent/10 text-accent' :
              user.role === 'viewer' ? 'bg-surface-3 text-text-tertiary' :
              'bg-status-running/10 text-status-running'
            }`}>
              {user.role}
            </span>
          </div>
        ))}
      </div>
    </section>
  )
}

function RegisterUserForm({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient()
  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState('developer')

  const mutation = useMutation({
    mutationFn: () => api.register({ username, email, password, role } as any),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      onClose()
    },
  })

  return (
    <form
      onSubmit={(e) => { e.preventDefault(); mutation.mutate() }}
      className="bg-surface-1 border border-border rounded-lg p-4 space-y-4 animate-slide-up mb-4"
    >
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="label">Username</label>
          <input value={username} onChange={(e) => setUsername(e.target.value)} className="input-field" required placeholder="jdoe" />
        </div>
        <div>
          <label className="label">Email</label>
          <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} className="input-field" required placeholder="jdoe@example.com" />
        </div>
      </div>
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="label">Password</label>
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} className="input-field" required minLength={8} />
        </div>
        <div>
          <label className="label">Role</label>
          <select value={role} onChange={(e) => setRole(e.target.value)} className="input-field">
            <option value="admin">Admin</option>
            <option value="developer">Developer</option>
            <option value="viewer">Viewer</option>
          </select>
        </div>
      </div>
      <div className="flex gap-3">
        <button type="submit" disabled={mutation.isPending} className="btn-primary">
          {mutation.isPending ? 'Creating...' : 'Create User'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
      </div>
      {mutation.isError && (
        <p className="text-status-failed text-xs">{(mutation.error as Error).message}</p>
      )}
    </form>
  )
}

function RolesSection() {
  const roles = [
    {
      name: 'Admin',
      description: 'Full access to all resources, user management, and system configuration',
      permissions: ['Manage users & teams', 'Create/delete projects & apps', 'Deploy to all environments', 'Manage secrets', 'Create API keys', 'System configuration'],
      scope: 'Global',
    },
    {
      name: 'Developer',
      description: 'Can create and manage apps, deploy, and view resources',
      permissions: ['Create/edit apps', 'Deploy to assigned environments', 'View projects & apps', 'Manage own secrets', 'Create API keys (deploy, read, write)'],
      scope: 'Global',
    },
    {
      name: 'Viewer',
      description: 'Read-only access to projects, apps, and logs',
      permissions: ['View projects & apps', 'View logs & metrics', 'View deployment history'],
      scope: 'Global',
    },
    {
      name: 'Team Owner',
      description: 'Full control over team membership and settings',
      permissions: ['Add/remove members', 'Change member roles', 'Delete team', 'All team admin permissions'],
      scope: 'Team',
    },
    {
      name: 'Team Admin',
      description: 'Can manage team members and resources',
      permissions: ['Add/remove members', 'Change member roles', 'Manage team resources'],
      scope: 'Team',
    },
    {
      name: 'Team Member',
      description: 'Standard team membership with access to team resources',
      permissions: ['View team resources', 'Deploy team apps', 'View team secrets'],
      scope: 'Team',
    },
  ]

  return (
    <section className="card p-6">
      <h3 className="section-title mb-5">Roles & Permissions</h3>
      <div className="space-y-4">
        {roles.map((role) => (
          <div key={role.name} className="bg-surface-1 border border-border rounded-lg p-5">
            <div className="flex items-center gap-3 mb-2">
              <h4 className="text-sm font-medium text-text-primary">{role.name}</h4>
              <span className={`text-[10px] font-mono px-2 py-0.5 rounded ${
                role.scope === 'Global' ? 'bg-accent/10 text-accent' : 'bg-status-running/10 text-status-running'
              }`}>
                {role.scope}
              </span>
            </div>
            <p className="text-xs text-text-tertiary mb-3">{role.description}</p>
            <div className="flex flex-wrap gap-1.5">
              {role.permissions.map((perm) => (
                <span key={perm} className="text-[11px] font-mono bg-surface-3 text-text-secondary px-2 py-1 rounded">
                  {perm}
                </span>
              ))}
            </div>
          </div>
        ))}
      </div>
    </section>
  )
}

function Spinner() {
  return (
    <div className="flex items-center justify-center py-12">
      <div className="relative">
        <div className="w-8 h-8 rounded-lg bg-accent/10 border border-accent/20 flex items-center justify-center animate-glow-pulse">
          <div className="w-2.5 h-2.5 rounded bg-accent" />
        </div>
      </div>
    </div>
  )
}
