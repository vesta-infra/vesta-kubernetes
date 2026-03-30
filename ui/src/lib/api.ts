const BASE = '/api/v1'

function getHeaders(): HeadersInit {
  const token = localStorage.getItem('vesta-token')
  const headers: HeadersInit = { 'Content-Type': 'application/json' }
  if (token) headers['Authorization'] = `Bearer ${token}`
  return headers
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: getHeaders(),
    ...options,
  })
  if (!res.ok) {
    if (res.status === 401) {
      localStorage.removeItem('vesta-token')
      localStorage.removeItem('vesta-user')
      window.location.href = '/login'
    }
    const err = await res.json().catch(() => ({ message: res.statusText }))
    throw new Error(err.message || res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

export const api = {
  // Setup
  setupStatus: () =>
    request<{ needsSetup: boolean }>('/setup/status'),

  setup: (data: { username: string; email: string; password: string; teamName: string }) =>
    request<{ token: string }>('/setup', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // Auth
  login: (username: string, password: string) =>
    request<{ token: string }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),

  register: (data: { username: string; email: string; password: string }) =>
    request<{ token: string }>('/auth/register', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  forgotPasswordStatus: () =>
    request<{ available: boolean }>('/auth/forgot-password/status'),

  forgotPassword: (email: string) =>
    request<{ message: string }>('/auth/forgot-password', {
      method: 'POST',
      body: JSON.stringify({ email }),
    }),

  resetPassword: (token: string, newPassword: string) =>
    request<{ message: string }>('/auth/reset-password', {
      method: 'POST',
      body: JSON.stringify({ token, newPassword }),
    }),

  // User
  getCurrentUser: () =>
    request<{ id: string; username: string; email: string; displayName: string; role: string; teamIds: string[] }>('/users/me'),

  updateProfile: (data: { displayName?: string; email?: string }) =>
    request<any>('/users/me', {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  changePassword: (data: { currentPassword: string; newPassword: string }) =>
    request<void>('/users/me/password', {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  // Teams
  listTeams: () =>
    request<{ items: any[]; total: number }>('/teams'),

  createTeam: (data: { name: string; displayName?: string }) =>
    request<any>('/teams', { method: 'POST', body: JSON.stringify(data) }),

  getTeam: (id: string) =>
    request<any>(`/teams/${id}`),

  updateTeam: (id: string, data: { displayName?: string }) =>
    request<any>(`/teams/${id}`, { method: 'PUT', body: JSON.stringify(data) }),

  deleteTeam: (id: string) =>
    request<void>(`/teams/${id}`, { method: 'DELETE' }),

  addTeamMember: (teamId: string, data: { userId: string; role: string }) =>
    request<any>(`/teams/${teamId}/members`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  removeTeamMember: (teamId: string, userId: string) =>
    request<void>(`/teams/${teamId}/members/${userId}`, { method: 'DELETE' }),

  // Projects
  listProjects: () =>
    request<{ items: any[]; total: number }>('/projects'),

  createProject: (data: { name: string; displayName?: string; team: string }) =>
    request<any>('/projects', { method: 'POST', body: JSON.stringify(data) }),

  getProject: (id: string) =>
    request<any>(`/projects/${id}`),

  updateProject: (id: string, data: any) =>
    request<any>(`/projects/${id}`, { method: 'PUT', body: JSON.stringify(data) }),

  deleteProject: (id: string) =>
    request<void>(`/projects/${id}`, { method: 'DELETE' }),

  // Environments
  listEnvironments: (projectId: string) =>
    request<{ items: any[]; total: number }>(`/projects/${projectId}/environments`),

  createEnvironment: (projectId: string, data: { name: string; branch?: string; autoDeploy?: boolean; requireApproval?: boolean }) =>
    request<any>(`/projects/${projectId}/environments`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  deleteEnvironment: (projectId: string, env: string) =>
    request<void>(`/projects/${projectId}/environments/${env}`, { method: 'DELETE' }),

  // Apps
  createApp: (projectId: string, data: any) =>
    request<any>(`/projects/${projectId}/apps`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  listApps: (params?: Record<string, string>) => {
    const qs = params ? '?' + new URLSearchParams(params).toString() : ''
    return request<{ items: any[]; total: number }>(`/apps${qs}`)
  },

  listProjectApps: (projectId: string) =>
    request<{ items: any[]; total: number }>(`/projects/${projectId}/apps`),

  getApp: (appId: string) =>
    request<any>(`/apps/${appId}`),

  updateApp: (appId: string, data: any) =>
    request<any>(`/apps/${appId}`, { method: 'PUT', body: JSON.stringify(data) }),

  deleteApp: (appId: string) =>
    request<void>(`/apps/${appId}`, { method: 'DELETE' }),

  // Deploy
  deploy: (appId: string, data: { tag: string; environment: string; reason?: string; commitSHA?: string }) =>
    request<any>(`/apps/${appId}/deploy`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  rollback: (appId: string, version: number, environment: string) =>
    request<any>(`/apps/${appId}/rollback`, {
      method: 'POST',
      body: JSON.stringify({ version, environment }),
    }),

  listDeployments: (appId: string) =>
    request<{ items: any[]; total: number }>(`/apps/${appId}/deployments`),

  restart: (appId: string, environment: string) =>
    request<any>(`/apps/${appId}/restart`, {
      method: 'POST',
      body: JSON.stringify({ environment }),
    }),

  scale: (appId: string, replicas: number) =>
    request<any>(`/apps/${appId}/scale`, {
      method: 'POST',
      body: JSON.stringify({ replicas }),
    }),

  // Secrets (per app+environment)
  createAppEnvSecret: (appId: string, env: string, data: { data: Record<string, string> }) =>
    request<any>(`/apps/${appId}/envs/${env}/secrets`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  listAppEnvSecrets: (appId: string, env: string) =>
    request<{ items: any[]; total: number }>(`/apps/${appId}/envs/${env}/secrets`),

  deleteAppEnvSecretKey: (appId: string, env: string, key: string) =>
    request<void>(`/apps/${appId}/envs/${env}/secrets/${encodeURIComponent(key)}`, { method: 'DELETE' }),

  // Secrets (global)
  listSecrets: (params?: Record<string, string>) => {
    const qs = params ? '?' + new URLSearchParams(params).toString() : ''
    return request<{ items: any[]; total: number }>(`/secrets${qs}`)
  },

  updateSecret: (secretId: string, data: { data?: Record<string, string> }) =>
    request<any>(`/secrets/${secretId}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  deleteSecret: (secretId: string) =>
    request<void>(`/secrets/${secretId}`, { method: 'DELETE' }),

  // Registry Secrets (image pull secrets)
  createRegistrySecret: (data: { name: string; registry: string; username: string; password: string }) =>
    request<any>('/secrets/registry', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  listRegistrySecrets: () =>
    request<{ items: any[]; total: number }>('/secrets/registry'),

  deleteRegistrySecret: (name: string) =>
    request<void>(`/secrets/registry/${name}`, { method: 'DELETE' }),

  // Logs
  getAppLogs: (appId: string, environment: string, opts?: { tail?: number; pod?: string; container?: string; previous?: boolean }) => {
    const params = new URLSearchParams({ environment })
    if (opts?.tail) params.set('tail', String(opts.tail))
    if (opts?.pod) params.set('pod', opts.pod)
    if (opts?.container) params.set('container', opts.container)
    if (opts?.previous) params.set('previous', 'true')
    return request<any>(`/apps/${appId}/logs?${params}`)
  },

  // Metrics
  getAppMetrics: (appId: string, environment: string) =>
    request<any>(`/apps/${appId}/metrics?environment=${encodeURIComponent(environment)}`),

  getPrometheusMetrics: (appId: string, environment: string, metric: string, range: string = '1h') =>
    request<any>(`/apps/${appId}/metrics/prometheus?environment=${encodeURIComponent(environment)}&metric=${encodeURIComponent(metric)}&range=${encodeURIComponent(range)}`),

  getPrometheusStatus: () =>
    request<{ available: boolean; prometheusUrl: string; availableMetrics: string[]; httpAvailable: boolean }>('/metrics/prometheus/status'),

  // API Tokens
  listTokens: () =>
    request<{ items: any[]; total: number }>('/auth/tokens'),

  createToken: (data: { name: string; scopes?: string[]; expiresIn?: string }) =>
    request<{ id: string; name: string; token: string; scopes: string[]; expiresAt?: string }>('/auth/tokens', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  revokeToken: (id: string) =>
    request<void>(`/auth/tokens/${id}`, { method: 'DELETE' }),

  // Users (admin)
  listUsers: () =>
    request<{ items: any[]; total: number }>('/users'),

  // Pod Sizes
  listPodSizes: () =>
    request<{ items: any[]; total: number }>('/pod-sizes'),

  // Notifications
  listNotificationChannels: (projectId: string) =>
    request<{ items: any[]; total: number }>(`/projects/${projectId}/notifications`),

  createNotificationChannel: (projectId: string, data: { name: string; type: string; config: Record<string, any>; events: string[] }) =>
    request<any>(`/projects/${projectId}/notifications`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  updateNotificationChannel: (projectId: string, channelId: string, data: { name?: string; config?: Record<string, any>; events?: string[]; enabled?: boolean }) =>
    request<any>(`/projects/${projectId}/notifications/${channelId}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  deleteNotificationChannel: (projectId: string, channelId: string) =>
    request<void>(`/projects/${projectId}/notifications/${channelId}`, { method: 'DELETE' }),

  testNotificationChannel: (projectId: string, channelId: string) =>
    request<{ status: string }>(`/projects/${projectId}/notifications/${channelId}/test`, { method: 'POST' }),

  listNotificationHistory: (projectId: string, limit?: number) => {
    const params = new URLSearchParams()
    if (limit) params.set('limit', String(limit))
    const qs = params.toString() ? `?${params}` : ''
    return request<{ items: any[]; total: number }>(`/projects/${projectId}/notifications/history${qs}`)
  },
}
