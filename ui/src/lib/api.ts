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

  updateEnvironment: (projectId: string, env: string, data: { branch?: string; autoDeploy?: boolean; requireApproval?: boolean; autoDeployPRs?: boolean }) =>
    request<any>(`/projects/${projectId}/environments/${env}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

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

  revealSecretValues: (secretId: string) =>
    request<{ id: string; name: string; values: Record<string, string> }>(`/secrets/${secretId}/reveal`),

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

  // Shared Secrets (project-scoped)
  createSharedSecret: (projectId: string, data: { name: string; data: Record<string, string>; environments?: string[] }) =>
    request<any>(`/projects/${projectId}/shared-secrets`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  listSharedSecrets: (projectId: string) =>
    request<{ items: any[]; total: number }>(`/projects/${projectId}/shared-secrets`),

  deleteSharedSecret: (projectId: string, name: string) =>
    request<void>(`/projects/${projectId}/shared-secrets/${name}`, { method: 'DELETE' }),

  bindSharedSecret: (appId: string, name: string, environment: string) =>
    request<any>(`/apps/${appId}/shared-secrets`, {
      method: 'POST',
      body: JSON.stringify({ name, environment }),
    }),

  unbindSharedSecret: (appId: string, name: string, environment?: string) =>
    request<void>(`/apps/${appId}/shared-secrets/${name}${environment ? `?environment=${encodeURIComponent(environment)}` : ''}`, { method: 'DELETE' }),

  listAppSharedSecrets: (appId: string) =>
    request<{ items: { name: string; environments: string[] }[]; total: number }>(`/apps/${appId}/shared-secrets`),

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

  // Audit Log
  listAuditLogs: (params?: { projectId?: string; appId?: string; action?: string; userId?: string; resourceType?: string; from?: string; to?: string; limit?: number; offset?: number }) => {
    const qs = new URLSearchParams()
    if (params?.projectId) qs.set('projectId', params.projectId)
    if (params?.appId) qs.set('appId', params.appId)
    if (params?.action) qs.set('action', params.action)
    if (params?.userId) qs.set('userId', params.userId)
    if (params?.resourceType) qs.set('resourceType', params.resourceType)
    if (params?.from) qs.set('from', params.from)
    if (params?.to) qs.set('to', params.to)
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    const q = qs.toString() ? `?${qs}` : ''
    return request<{ items: any[]; total: number }>(`/audit-logs${q}`)
  },

  // Activity Feed
  getActivityFeed: (params?: { projectId?: string; limit?: number; offset?: number }) => {
    const qs = new URLSearchParams()
    if (params?.projectId) qs.set('projectId', params.projectId)
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    const q = qs.toString() ? `?${qs}` : ''
    return request<{ items: any[]; total: number }>(`/activity${q}`)
  },

  // Webhook Deliveries
  listWebhookDeliveries: (params?: { provider?: string; status?: string; repository?: string; limit?: number; offset?: number }) => {
    const qs = new URLSearchParams()
    if (params?.provider) qs.set('provider', params.provider)
    if (params?.status) qs.set('status', params.status)
    if (params?.repository) qs.set('repository', params.repository)
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    const q = qs.toString() ? `?${qs}` : ''
    return request<{ items: any[]; total: number }>(`/webhook-deliveries${q}`)
  },

  // App Cloning
  cloneApp: (appId: string, data: { name: string; project?: string }) =>
    request<any>(`/apps/${appId}/clone`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // Sleep / Wake
  sleepApp: (appId: string) =>
    request<any>(`/apps/${appId}/sleep`, { method: 'POST' }),

  wakeApp: (appId: string) =>
    request<any>(`/apps/${appId}/wake`, { method: 'POST' }),

  // Environment Cloning
  cloneEnvironment: (projectId: string, envName: string, data: { name: string; branch?: string }) =>
    request<any>(`/projects/${projectId}/environments/${envName}/clone`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // Health Dashboard
  getHealthDashboard: (params?: { projectId?: string; teamId?: string }) => {
    const qs = new URLSearchParams()
    if (params?.projectId) qs.set('projectId', params.projectId)
    if (params?.teamId) qs.set('teamId', params.teamId)
    const q = qs.toString() ? `?${qs}` : ''
    return request<any>(`/health/dashboard${q}`)
  },

  // Builds
  triggerBuild: (appId: string, data: { environment: string; commitSha?: string; branch?: string; reason?: string }) =>
    request<any>(`/apps/${appId}/builds`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  listBuilds: (appId: string, params?: { status?: string; limit?: number; offset?: number }) => {
    const qs = new URLSearchParams()
    if (params?.status) qs.set('status', params.status)
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    const q = qs.toString() ? `?${qs}` : ''
    return request<{ items: any[]; total: number }>(`/apps/${appId}/builds${q}`)
  },

  getBuild: (appId: string, buildId: string) =>
    request<any>(`/apps/${appId}/builds/${buildId}`),

  getBuildLogs: (appId: string, buildId: string, tail?: number) => {
    const params = new URLSearchParams()
    if (tail) params.set('tail', String(tail))
    const q = params.toString() ? `?${params}` : ''
    return request<{ buildId: string; status: string; logs: string }>(`/apps/${appId}/builds/${buildId}/logs${q}`)
  },

  cancelBuild: (appId: string, buildId: string) =>
    request<any>(`/apps/${appId}/builds/${buildId}/cancel`, { method: 'POST' }),

  // Templates
  listTemplates: (params?: { category?: string; search?: string }) => {
    const qs = new URLSearchParams()
    if (params?.category) qs.set('category', params.category)
    if (params?.search) qs.set('search', params.search)
    const q = qs.toString() ? `?${qs}` : ''
    return request<{ items: any[]; total: number }>(`/templates${q}`)
  },

  deployTemplate: (templateId: string, data: { project: string; environments: string[]; name?: string; storageSize?: string; overrides?: Record<string, any> }) =>
    request<any>(`/templates/${templateId}/deploy`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // GitHub App
  getGitHubAppStatus: () =>
    request<{ configured: boolean; appId?: number; appName?: string; appSlug?: string; ownerLogin?: string; ownerType?: string; installations?: number }>('/settings/github-app'),

  getGitHubAppManifest: (data: { appName?: string; apiBaseUrl: string; organization?: string; uiBaseUrl?: string }) =>
    request<{ manifest: any; githubUrl: string; state: string }>('/github/manifest', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  listGitHubAppInstallations: () =>
    request<{ installations: any[] }>('/settings/github-app/installations'),

  deleteGitHubApp: () =>
    request<{ status: string }>('/settings/github-app', { method: 'DELETE' }),

  // Git helpers
  listRepoBranches: (repo: string) =>
    request<{ branches: string[] }>(`/git/branches?repo=${encodeURIComponent(repo)}`),

  listAccessibleRepos: () =>
    request<{ repos: { full_name: string; private: boolean }[] }>('/git/repos'),
}
