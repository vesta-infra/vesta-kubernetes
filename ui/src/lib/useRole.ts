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
