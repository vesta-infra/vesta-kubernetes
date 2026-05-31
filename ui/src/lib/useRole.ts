import { useQuery } from '@tanstack/react-query'
import { api } from './api'

export function useUserRole(): string {
  try {
    const stored = localStorage.getItem('vesta-user')
    if (stored) {
      const parsed = JSON.parse(stored)
      return parsed.role || 'viewer'
    }
  } catch { /* ignore */ }
  return 'viewer'
}

export function isViewer(): boolean {
  try {
    const stored = localStorage.getItem('vesta-user')
    if (stored) {
      const parsed = JSON.parse(stored)
      return parsed.role === 'viewer'
    }
  } catch { /* ignore */ }
  return true
}

/** Returns true if the current user is a project owner for the given projectId. */
export function useIsProjectOwner(projectId: string | undefined): boolean {
  const { data: me } = useQuery({
    queryKey: ['currentUser'],
    queryFn: () => api.getCurrentUser(),
    staleTime: 60_000,
  })
  const { data: members } = useQuery({
    queryKey: ['projectMembers', projectId],
    queryFn: () => api.listProjectMembers(projectId!),
    enabled: !!projectId && !!me,
    staleTime: 30_000,
  })
  if (!me || !members) return false
  return members.items.some((m: any) => m.userId === me.id && m.role === 'owner')
}
