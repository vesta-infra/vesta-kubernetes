import { Outlet, NavLink, useNavigate, useLocation } from 'react-router-dom'
import { useEffect, useState } from 'react'
import { useUserRole } from '../lib/useRole'
import { api } from '../lib/api'

const mainNavItems = [
  {
    to: '/',
    label: 'Dashboard',
    icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M4 5a1 1 0 011-1h4a1 1 0 011 1v5a1 1 0 01-1 1H5a1 1 0 01-1-1V5zm10 0a1 1 0 011-1h4a1 1 0 011 1v3a1 1 0 01-1 1h-4a1 1 0 01-1-1V5zm-10 9a1 1 0 011-1h4a1 1 0 011 1v5a1 1 0 01-1 1H5a1 1 0 01-1-1v-5zm10-2a1 1 0 011-1h4a1 1 0 011 1v7a1 1 0 01-1 1h-4a1 1 0 01-1-1v-7z" />
      </svg>
    ),
  },
  {
    to: '/projects',
    label: 'Projects',
    icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
      </svg>
    ),
  },
  {
    to: '/apps',
    label: 'Apps',
    icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
      </svg>
    ),
  },
  {
    to: '/secrets',
    label: 'Secrets',
    icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
      </svg>
    ),
  },
]

const bottomNavItems = [
  {
    to: '/settings',
    label: 'Settings',
    icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
        <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
      </svg>
    ),
  },
]

const allNavItems = [...mainNavItems, ...bottomNavItems]

export default function Layout() {
  const navigate = useNavigate()
  const location = useLocation()
  const [user, setUser] = useState<{ username?: string; email?: string } | null>(null)
  const [roleOverride, setRoleOverride] = useState<string | null>(null)
  const storedRole = useUserRole()
  const role = roleOverride ?? storedRole

  useEffect(() => {
    const token = localStorage.getItem('vesta-token')
    if (!token) {
      navigate('/login')
      return
    }
    const stored = localStorage.getItem('vesta-user')
    if (stored) {
      try { setUser(JSON.parse(stored)) } catch { /* ignore */ }
    }
    // Refresh user profile from API to ensure role is up to date
    api.getCurrentUser()
      .then((u) => {
        const updated = { username: u.username, email: u.email, role: u.role }
        setUser(updated)
        setRoleOverride(u.role)
        localStorage.setItem('vesta-user', JSON.stringify(updated))
      })
      .catch(() => { /* token may be invalid, auth guard will handle */ })
  }, [navigate])

  const handleLogout = () => {
    localStorage.removeItem('vesta-token')
    localStorage.removeItem('vesta-user')
    navigate('/login')
  }

  const pageTitle = allNavItems.find(
    (item) => item.to === '/' ? location.pathname === '/' : location.pathname.startsWith(item.to)
  )?.label || 'Vesta'

  return (
    <div className="min-h-screen flex bg-surface-0">
      {/* Sidebar */}
      <aside className="w-[220px] bg-surface-1/50 backdrop-blur-xl border-r border-border flex flex-col shrink-0 relative">
        {/* Ambient sidebar glow */}
        <div className="absolute top-0 left-0 w-full h-40 bg-gradient-to-b from-accent/[0.03] to-transparent pointer-events-none" />

        {/* Logo */}
        <div className="relative px-5 py-6 border-b border-border/60">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-accent/20 to-accent/5 border border-accent/20 flex items-center justify-center shadow-glow-sm">
              <div className="w-2.5 h-2.5 rounded-[3px] bg-accent shadow-[0_0_8px_rgba(245,158,11,0.4)]" />
            </div>
            <div>
              <h1 className="text-[15px] font-bold text-text-primary tracking-tight font-body">Vesta</h1>
              <p className="text-[9px] font-mono text-text-tertiary uppercase tracking-[0.2em] mt-px">kubernetes</p>
            </div>
          </div>
        </div>

        {/* Main nav */}
        <nav className="flex-1 px-3 py-5 space-y-1 relative">
          {mainNavItems.filter(item => !(role === 'viewer' && (item.to === '/secrets'))).map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === '/'}
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2.5 rounded-lg text-[13px] font-medium transition-all duration-200 group relative ${
                  isActive
                    ? 'bg-accent/[0.08] text-accent shadow-[inset_0_0_0_1px_rgba(245,158,11,0.12)]'
                    : 'text-text-secondary hover:text-text-primary hover:bg-surface-3/50'
                }`
              }
            >
              {({ isActive }) => (
                <>
                  {isActive && (
                    <div className="absolute left-0 top-1/2 -translate-y-1/2 w-[3px] h-5 rounded-r-full bg-accent shadow-[0_0_8px_rgba(245,158,11,0.3)]" />
                  )}
                  <span className={isActive ? 'text-accent' : 'text-text-tertiary group-hover:text-text-secondary transition-colors'}>
                    {item.icon}
                  </span>
                  {item.label}
                </>
              )}
            </NavLink>
          ))}
        </nav>

        {/* Bottom nav */}
        <div className="px-3 pb-2 space-y-1">
          {bottomNavItems.filter(item => !(role === 'viewer' && item.to === '/settings')).map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2.5 rounded-lg text-[13px] font-medium transition-all duration-200 group relative ${
                  isActive
                    ? 'bg-accent/[0.08] text-accent shadow-[inset_0_0_0_1px_rgba(245,158,11,0.12)]'
                    : 'text-text-secondary hover:text-text-primary hover:bg-surface-3/50'
                }`
              }
            >
              {({ isActive }) => (
                <>
                  {isActive && (
                    <div className="absolute left-0 top-1/2 -translate-y-1/2 w-[3px] h-5 rounded-r-full bg-accent shadow-[0_0_8px_rgba(245,158,11,0.3)]" />
                  )}
                  <span className={isActive ? 'text-accent' : 'text-text-tertiary group-hover:text-text-secondary transition-colors'}>
                    {item.icon}
                  </span>
                  {item.label}
                </>
              )}
            </NavLink>
          ))}
        </div>

        {/* User section */}
        <div className="relative px-3 py-4 border-t border-border/60 space-y-2">
          {user?.username && (
            <div className="px-3 py-2 bg-surface-2/50 rounded-lg">
              <div className="flex items-center gap-2.5">
                <div className="w-6 h-6 rounded-full bg-accent/10 border border-accent/20 flex items-center justify-center text-[10px] font-bold text-accent">
                  {user.username.charAt(0).toUpperCase()}
                </div>
                <div className="min-w-0 flex-1">
                  <p className="text-xs font-medium text-text-primary truncate">{user.username}</p>
                  {user.email && (
                    <p className="text-[10px] text-text-tertiary truncate">{user.email}</p>
                  )}
                </div>
              </div>
            </div>
          )}
          <button
            onClick={handleLogout}
            className="flex items-center gap-3 w-full px-3 py-2 text-[13px] text-text-tertiary hover:text-status-failed rounded-lg transition-all duration-200 hover:bg-status-failed/5"
          >
            <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1" />
            </svg>
            Sign out
          </button>
        </div>
      </aside>

      {/* Main content */}
      <main className="flex-1 overflow-auto relative">
        {/* Ambient background effects */}
        <div className="fixed top-0 right-0 w-[600px] h-[600px] bg-gradient-radial from-accent/[0.02] via-transparent to-transparent pointer-events-none" />

        <header className="sticky top-0 z-10 bg-surface-0/70 backdrop-blur-2xl border-b border-border/50 px-8 py-5">
          <div className="flex items-center justify-between max-w-6xl mx-auto">
            <h2 className="page-title">{pageTitle}</h2>
            <div className="flex items-center gap-2">
              <span className="text-[10px] font-mono text-text-tertiary/60 tracking-wider">v0.1.18</span>
              <div className="w-1.5 h-1.5 rounded-full bg-status-running shadow-[0_0_6px_rgba(34,197,94,0.4)] animate-glow-pulse" />
            </div>
          </div>
        </header>
        <div className="max-w-6xl mx-auto px-8 py-8 animate-fade-in">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
