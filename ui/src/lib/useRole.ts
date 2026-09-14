import { useQuery } from '@tanstack/react-query'
import { api } from './api'

/**
 * What the signed-in user may do.
 *
 * This used to read a single global role out of localStorage, which could only ever answer
 * "admin, developer or viewer" and knew nothing about projects — so a page had no way to
 * show a project maintainer their own project while hiding one they have no part in.
 *
 * The contract is unchanged and worth restating: this is only ever used to hide UI that
 * would not work anyway. The server refuses the actions it gates independently, so a stale
 * or missing answer here is a cosmetic problem, never a security one.
 */

export type Role = 'owner' | 'maintainer' | 'deployer' | 'viewer' | 'admin' | ''

const RANK: Record<string, number> = {
  '': 0, viewer: 1, deployer: 2, maintainer: 3, owner: 4, admin: 5,
}

/** Mirrors rbac.Action on the server. */
export type Action = 'read' | 'deploy' | 'write' | 'secrets' | 'admin'

const REQUIRED: Record<Action, string> = {
  read: 'viewer',
  deploy: 'deployer',
  // Secrets sit above deploy: exec and pod file access grant the same thing by another
  // route, so they share this level.
  write: 'maintainer',
  secrets: 'maintainer',
  admin: 'owner',
}

export function usePermissions() {
  const { data } = useQuery({
    queryKey: ['my-permissions'],
    queryFn: () => api.getMyPermissions(),
    staleTime: 60_000,
  })
  return data
}

/** The role the user holds for a project, or an environment within it. */
export function useEffectiveRole(projectId?: string, environment?: string): Role {
  const perms = usePermissions()
  if (!perms) return ''
  if (perms.global === 'admin') return 'admin'

  // While enforcement is off, memberships are recorded but not consulted — so showing the
  // membership-derived role here would hide things the server would in fact allow.
  if (!perms.enforced) return (perms.fallback || '') as Role
  if (!projectId) return ''

  if (environment) {
    const envRole = perms.environments[`${projectId}/${environment}`]
    // An environment override replaces the project role in both directions. Absent means
    // inherit, not none.
    if (envRole) return envRole as Role
  }
  return (perms.projects[projectId] || '') as Role
}

/** Whether the user may perform an action at a scope. */
export function useCan(action: Action, projectId?: string, environment?: string): boolean {
  const role = useEffectiveRole(projectId, environment)
  return (RANK[role] ?? 0) >= (RANK[REQUIRED[action]] ?? 99)
}

/**
 * The global role, still read synchronously from the login cache.
 *
 * Kept because a handful of places need an answer before any query resolves — the sidebar
 * decides what to render on first paint. It says nothing about projects.
 */
export function useUserRole(): string {
  try {
    const stored = localStorage.getItem('vesta-user')
    if (stored) return JSON.parse(stored).role || 'viewer'
  } catch { /* ignore */ }
  return 'viewer'
}

export function isViewer(): boolean {
  try {
    const stored = localStorage.getItem('vesta-user')
    if (stored) return JSON.parse(stored).role === 'viewer'
  } catch { /* ignore */ }
  return true
}

/** Whether the user owns a project. Replaces the old members-list scan. */
export function useIsProjectOwner(projectId: string | undefined): boolean {
  const role = useEffectiveRole(projectId)
  return role === 'owner' || role === 'admin'
}

export function useCurrentUsername(): string {
  try {
    const stored = localStorage.getItem('vesta-user')
    if (stored) return JSON.parse(stored).username || ''
  } catch { /* ignore */ }
  return ''
}
