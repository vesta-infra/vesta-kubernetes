export type LogDrainType =
  | 'http' | 'loki' | 'syslog' | 'elasticsearch' | 'datadog' | 's3' | 'openobserve' | 'forward'

export interface LogDrain {
  name: string
  type: LogDrainType
  displayName?: string
  description?: string
  project?: string
  /** Several projects. Unioned with `project`, so a drain can name both. */
  projects?: string[]
  app?: string
  environment?: string
  enabled: boolean
  excludeApps?: string[]
  /** Carries secret references, never values. */
  config: Record<string, any>
  ready: boolean
  reason?: string
  scope?: string
  recordsDelivered: number
  errors: number
  lastDeliveryAt?: string
}

export interface LogDrainPayload {
  name?: string
  type: LogDrainType
  displayName?: string
  description?: string
  project?: string
  /** Several projects. Sent instead of `project` by the form. */
  projects?: string[]
  app?: string
  environment?: string
  enabled?: boolean
  excludeApps?: string[]
  config?: Record<string, any>
  configRaw?: string
  /** Write-only. Never returned by the API. */
  credentials?: Record<string, string>
}

export type MiddlewareType =
  | 'rateLimit' | 'basicAuth' | 'ipAllowList' | 'headers'
  | 'stripPrefix' | 'compress' | 'retry' | 'circuitBreaker' | 'buffering' | 'raw'

export interface Middleware {
  name: string
  type: MiddlewareType
  displayName?: string
  description?: string
  project?: string
  app?: string
  environment?: string
  config: Record<string, any>
  ready: boolean
  reason?: string
  appliedCount: number
  appliedNamespaces?: string[]
}

export interface BasicAuthUser {
  username: string
  /** Write-only. Blank on an existing account means "leave the stored password alone". */
  password: string
}

export interface MiddlewarePayload {
  name?: string
  type: MiddlewareType
  displayName?: string
  description?: string
  project?: string
  config?: Record<string, any>
  /** For type "raw": a middleware pasted as YAML or JSON, parsed server-side. */
  configRaw?: string
}

const BASE = '/api/v1'


/** A second factor registered to the current user. */
export type MFAEnrollment = {
  method: 'totp' | 'webauthn' | 'backup'
  id?: string
  name?: string
  createdAt: string
  lastUsedAt?: string | null
}

export type MFAStatus = {
  enrollments: MFAEnrollment[]
  methods: ('totp' | 'webauthn' | 'backup')[]
  enabled: boolean
  required: boolean
  backupCodesLeft: number
  totpAvailable: boolean
  requireAdminPolicy: boolean
}



export type UIDomainSettings = {
  host: string
  tls: boolean
  clusterIssuer: string
  ingressClassName: string
}

export type UIDomainStatus = {
  configured: boolean
  settings: UIDomainSettings
  certReady: boolean
  certMessage: string
  namespace: string
  /** The --set flags that make a change survive the next `helm upgrade`. */
  helmValues: string[]
}

export type ComponentVersion = {
  component: string
  image: string
  tag: string
  ready: boolean
}

export type SystemVersion = {
  api: { version: string; commit: string; date: string; goVersion: string; platform: string }
  components: ComponentVersion[]
  namespace: string
}

export type UpdateStatus = {
  current: string
  latest: string
  updateAvailable: boolean
  checkedAt: string
  checkEnabled: boolean
  isRelease: boolean
  releaseNotesUrl: string
}

export type UpdateRecord = {
  id: string
  fromVersion: string
  toVersion: string
  jobName: string
  status: 'running' | 'succeeded' | 'failed' | 'unknown'
  message: string
  startedAt: string
  finishedAt?: string | null
}

export type ReauthGrant = {
  grantId: string
  method: 'password' | 'webauthn'
  expiresAt: string
}

export type Passkey = {
  id: string
  name: string
  createdAt: string
  lastUsedAt?: string | null
  transports?: string[]
}

/**
 * What POST /auth/login and the verify endpoints return.
 *
 * Login is no longer guaranteed to hand back a session: it may instead return a
 * short-lived token that reaches only the 2FA endpoints. Callers must branch on
 * mfaRequired / mfaEnrollmentRequired before treating `token` as a session.
 */
export type AuthResult = {
  token: string
  expiresAt: string
  user?: { id: string; username: string; email: string; displayName: string; role: string }
  mfaRequired?: boolean
  mfaEnrollmentRequired?: boolean
  methods?: ('totp' | 'webauthn' | 'backup')[]
  reason?: string
  totpAvailable?: boolean
}

/**
 * An SSL certificate provider — a cert-manager ClusterIssuer.
 *
 * `managed` is false for issuers created outside Vesta (with kubectl, or by an older
 * install). Those stay selectable, because hiding them would make a working setup look
 * empty, but the UI does not offer to edit them: their shape is the admin's and this form
 * models only what Vesta writes.
 */
export type SSLProvider = {
  name: string
  kind: string
  email?: string
  acmeServer?: string
  solver?: string
  dnsProvider?: string
  ingressClass?: string
  dnsZones?: string[]
  ready: boolean
  status: string
  statusReason?: string
  managed: boolean
  isDefault: boolean
}

/** Credentials are write-only: they are sent here and never returned by any read. */
export type SSLProviderInput = {
  name: string
  kind: string
  email?: string
  acmeServer?: string
  eabKeyId?: string
  eabHmacKey?: string
  ingressClass?: string
  dnsProvider?: string
  dnsConfig?: Record<string, string>
  dnsCredentials?: Record<string, string>
  dnsZones?: string[]
  caSecretName?: string
}

/** A sealed project bundle. Everything but `recipient` is opaque without the target's key. */
export type VestaBundle = {
  vestaBundle: number
  exportedAt: string
  recipient: string
  ephemeralPublicKey: string
  nonce: string
  ciphertext: string
}

function getHeaders(): HeadersInit {
  const token = localStorage.getItem('vesta-token')
  const headers: HeadersInit = { 'Content-Type': 'application/json' }
  if (token) headers['Authorization'] = `Bearer ${token}`
  return headers
}

/**
 * Calls that change a user's own factors carry a single-use re-authentication grant.
 *
 * The grant is spent server-side on success, so it is never reused: each destructive
 * action needs its own confirmation.
 */
async function request<T>(path: string, options?: RequestInit & { reauthGrant?: string }): Promise<T> {
  const { reauthGrant, ...init } = options || {}
  const headers: Record<string, string> = { ...(getHeaders() as Record<string, string>) }
  if (reauthGrant) headers['X-Vesta-Reauth'] = reauthGrant

  const res = await fetch(`${BASE}${path}`, {
    headers,
    ...init,
  })
  if (!res.ok) {
    // A 401 normally means the session is gone, so clear it and start over. The 2FA
    // endpoints are the exception: there a 401 means "that code was wrong", and the
    // caller is mid-challenge holding a token that is still perfectly valid. Redirecting
    // would throw away the challenge and send the user back to the password screen for a
    // single mistyped digit, so those screens render their own errors instead.
    if (res.status === 401 && !path.startsWith('/auth/mfa/')) {
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

export interface GitConnection {
  id: string
  provider: 'github' | 'gitlab' | 'bitbucket'
  displayName: string
  baseUrl?: string
  host: string
  account?: string
  externalId?: string
  metadata?: Record<string, string>
  isLegacy?: boolean
  createdAt: string
  updatedAt: string
  /** Where to grant access to more repositories; absent when the provider has no such flow. */
  installUrl?: string
  /** The path this connection's webhooks should POST to. */
  webhookPath: string
}

export interface RegistryTestResult {
  ok: boolean
  error?: string
  /** What was typed. */
  registry: string
  /** The key kubelet will actually look for when pulling. */
  authsKey: string
  apiBase: string
  flavor: string
  /** False when the stored value will not match any image's host. */
  normalized: boolean
}

export interface AccessibleRepo {
  full_name: string
  provider: string
  host: string
  private: boolean
  defaultBranch?: string
  connectionId: string
  connectionName: string
}

export interface MyPermissions {
  global: string
  projects: Record<string, string>
  /** Keyed "project/environment". */
  environments: Record<string, string>
  /** False while memberships are recorded but not enforced. */
  enforced: boolean
  /** What the caller effectively holds where they have no membership. */
  fallback: string
  actions: string[]
}

export interface AccessImpact {
  userId: string
  username: string
  email: string
  globalRole: string
  projectCount: number
  envCount: number
}

export type AddonType = 'postgres' | 'mysql' | 'redis' | 'mongodb'

export interface Addon {
  name: string
  type: AddonType
  version?: string
  environment?: string
  size?: string
  storage?: string
  ready: boolean
  reason?: string
  phase?: string
  secretName?: string
  createdAt: string
}

// Cost reporting.
//
// Money is computed server-side at read time from stored reservations and the current rate
// card, so a response always says which rates produced it and whether they are the built-in
// estimate rather than this cluster's real prices.
export interface Cost {
  cpu: number
  memory: number
  storage: number
  total: number
  currency: string
}

export interface CostEntry {
  key: string
  environment?: string
  kind?: string
  cost: Cost
  projectedMonthly: Cost
  // Absent when metrics-server is not installed. The gap between reserved and used is the
  // actionable number, so it is optional rather than defaulted to zero -- reporting an
  // unknown as 0% efficient would invent a finding.
  cpuEfficiency?: number
  memoryEfficiency?: number
}

export interface CostReport {
  window: string
  entries: CostEntry[]
  total: Cost
  projectedMonthly: Cost
  rates: {
    cpuCoreHour: number
    memoryGiBHour: number
    storageGiBMonth: number
    currency: string
  }
  estimated: boolean
}

export interface EnvironmentQuota {
  environment: string
  quota?: {
    enforce?: boolean
    requestsCpu?: string
    requestsMemory?: string
    limitsCpu?: string
    limitsMemory?: string
    storageTotal?: string
    maxPods?: number
  }
  // What the operator observed. `exceeds` means enforcing this quota would already refuse
  // work, which is the thing worth knowing BEFORE turning it on: Kubernetes does not apply
  // a quota retroactively, it refuses the next admission.
  status?: {
    enforced?: boolean
    wouldExceed?: boolean
    reason?: string
    committed?: Record<string, string>
    used?: Record<string, string>
  }
}

export interface SecurityPosture {
  profile: 'legacy' | 'baseline' | 'restricted'
  // Scope given to secrets created without one. Applies to NEW secrets only; existing
  // credentials keep the scope they have, because narrowing one an app already pulls with
  // would break that app's deploys with nothing to point at.
  defaultSecretScope?: 'global' | 'project'
  networkIsolation: {
    enabled: boolean
    trustedNamespaces?: string[]
    metricsPort?: number
  }
  // Per environment, what the operator actually observed. `enabled` and `enforced` are
  // different questions: NetworkPolicy is enforced by the cluster's network plugin, not by
  // Kubernetes, so a cluster running one that ignores it accepts every policy and filters
  // nothing.
  observed?: Array<{
    environment: string
    enabled?: boolean
    enforced?: boolean
    enforcementKnown?: boolean
    policyCount?: number
    note?: string
  }>
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
    request<AuthResult>('/auth/login', {
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

  acceptInvite: (token: string, password: string) =>
    request<{ token: string; user: any }>('/auth/accept-invite', {
      method: 'POST',
      body: JSON.stringify({ token, password }),
    }),

  // User
  getMyPermissions: () => request<MyPermissions>('/users/me/permissions'),

  getRBACSettings: () =>
    request<{ enforced: boolean; users: AccessImpact[]; wouldLoseAllAccess: AccessImpact[] }>('/settings/rbac'),

  setRBACEnforcement: (enforced: boolean) =>
    request<{ enforced: boolean }>('/settings/rbac', { method: 'PUT', body: JSON.stringify({ enforced }) }),

  listProjectEnvMembers: (projectId: string) =>
    request<{ items: { projectId: string; environment: string; userId: string; username?: string; role: string }[] }>(
      `/projects/${projectId}/env-members`),

  setProjectEnvRole: (projectId: string, environment: string, userId: string, role: string) =>
    request<any>(`/projects/${projectId}/environments/${encodeURIComponent(environment)}/members/${userId}`,
      { method: 'PUT', body: JSON.stringify({ role }) }),

  removeProjectEnvRole: (projectId: string, environment: string, userId: string) =>
    request<any>(`/projects/${projectId}/environments/${encodeURIComponent(environment)}/members/${userId}`,
      { method: 'DELETE' }),

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

  // System version and self-update
  getSystemVersion: () =>
    request<SystemVersion>('/system/version'),

  getUpdateStatus: () =>
    request<UpdateStatus>('/system/update'),

  checkForUpdates: () =>
    request<UpdateStatus>('/system/update/check', { method: 'POST' }),

  setUpdateSettings: (checkEnabled: boolean) =>
    request<{ checkEnabled: boolean }>('/system/update/settings', {
      method: 'PUT', body: JSON.stringify({ checkEnabled }),
    }),

  // Requires a re-auth grant: pointing Vesta's own deployments at another version is
  // close to cluster-admin.
  triggerUpdate: (version: string, reauthGrant: string) =>
    request<{ status: string; version: string; job: string; note: string }>('/system/update', {
      method: 'POST', body: JSON.stringify({ version }), reauthGrant,
    }),

  getUpdateProgress: () =>
    request<{ current: UpdateRecord | null; history: UpdateRecord[] }>('/system/update/status'),

  // Two-factor authentication
  mfaStatus: () =>
    request<MFAStatus>('/auth/mfa/status'),

  mfaVerify: (code: string) =>
    request<AuthResult>('/auth/mfa/verify', { method: 'POST', body: JSON.stringify({ code }) }),

  // reauthGrant is required only when the account already holds a factor; the first one
  // is exempt, which is what keeps mandatory enrollment at login possible.
  totpEnroll: (reauthGrant?: string) =>
    request<{ secret: string; otpauthUrl: string; qrDataUri: string; expiresAt: string }>(
      '/auth/mfa/totp/enroll', { method: 'POST', reauthGrant }),

  totpConfirm: (code: string) =>
    request<{ confirmed: boolean; backupCodes: string[] }>(
      '/auth/mfa/totp/confirm', { method: 'POST', body: JSON.stringify({ code }) }),

  totpDisable: (reauthGrant: string) =>
    request<void>('/auth/mfa/totp', { method: 'DELETE', reauthGrant }),

  listPasskeys: () =>
    request<{ items: Passkey[]; total: number }>('/auth/mfa/webauthn/credentials'),

  renamePasskey: (id: string, name: string) =>
    request<void>(`/auth/mfa/webauthn/credentials/${id}`, { method: 'PUT', body: JSON.stringify({ name }) }),

  deletePasskey: (id: string, reauthGrant: string) =>
    request<void>(`/auth/mfa/webauthn/credentials/${id}`, { method: 'DELETE', reauthGrant }),

  passkeyRegisterBegin: (reauthGrant?: string) =>
    request<{ sessionId: string; publicKey: any }>('/auth/mfa/webauthn/register/begin', { method: 'POST', reauthGrant }),

  passkeyRegisterFinish: (sessionId: string, name: string, credential: any) =>
    request<{ id: string; name: string; backupCodes: string[] | null }>(
      `/auth/mfa/webauthn/register/finish?sessionId=${encodeURIComponent(sessionId)}&name=${encodeURIComponent(name)}`,
      { method: 'POST', body: JSON.stringify(credential) }),

  passkeyAuthBegin: () =>
    request<{ sessionId: string; publicKey: any }>('/auth/mfa/webauthn/authenticate/begin', { method: 'POST' }),

  passkeyAuthFinish: (sessionId: string, credential: any) =>
    request<AuthResult>(
      `/auth/mfa/webauthn/authenticate/finish?sessionId=${encodeURIComponent(sessionId)}`,
      { method: 'POST', body: JSON.stringify(credential) }),

  regenerateBackupCodes: (reauthGrant: string) =>
    request<{ backupCodes: string[] }>('/auth/mfa/backup-codes', { method: 'POST', reauthGrant }),

  // Re-authentication: proving it is still you before a change that weakens your account.
  reauthPassword: (password: string) =>
    request<ReauthGrant>('/auth/reauth/password', { method: 'POST', body: JSON.stringify({ password }) }),

  reauthPasskeyBegin: () =>
    request<{ sessionId: string; publicKey: any }>('/auth/reauth/webauthn/begin', { method: 'POST' }),

  reauthPasskeyFinish: (sessionId: string, credential: any) =>
    request<ReauthGrant>(`/auth/reauth/webauthn/finish?sessionId=${encodeURIComponent(sessionId)}`, {
      method: 'POST', body: JSON.stringify(credential),
    }),

  resetUserMFA: (userId: string, reauthGrant: string) =>
    request<{ reset: boolean; user: string; mustReenroll: boolean }>(`/users/${userId}/mfa`, {
      method: 'DELETE', reauthGrant,
    }),

  getMFAPolicy: () =>
    request<{ requireAdmin: boolean }>('/settings/mfa-policy'),

  updateMFAPolicy: (requireAdmin: boolean) =>
    request<{ requireAdmin: boolean }>('/settings/mfa-policy', {
      method: 'PUT', body: JSON.stringify({ requireAdmin }),
    }),

  // Project transfer between Vesta instances
  getInstanceIdentity: () =>
    request<{ publicKey: string; fingerprint: string }>('/instance/identity'),

  exportProject: (id: string, recipientPublicKey: string) =>
    request<VestaBundle>(`/projects/${id}/export`, {
      method: 'POST',
      body: JSON.stringify({ recipientPublicKey }),
    }),

  importProject: (bundle: VestaBundle, as?: string) =>
    request<{ project: string; importedAs: boolean; created: Record<string, number> }>('/projects/import', {
      method: 'POST',
      body: JSON.stringify({ bundle, ...(as ? { as } : {}) }),
    }),

  deleteProject: (id: string) =>
    request<void>(`/projects/${id}`, { method: 'DELETE' }),

  // Project Members
  listAddons: (projectId: string) =>
    request<{ items: Addon[]; total: number }>(`/projects/${projectId}/addons`),

  createAddon: (projectId: string, data: {
    name: string; type: AddonType; version?: string; environment?: string
    size?: string; storage?: string; deletionPolicy?: string
  }) =>
    request<any>(`/projects/${projectId}/addons`, { method: 'POST', body: JSON.stringify(data) }),

  deleteAddon: (projectId: string, name: string) =>
    request<any>(`/projects/${projectId}/addons/${name}?force=true`, { method: 'DELETE' }),

  getAddonCredentials: (projectId: string, name: string, environment: string) =>
    request<{ name: string; environment: string; credentials: Record<string, string> }>(
      `/projects/${projectId}/addons/${name}/credentials?environment=${encodeURIComponent(environment)}`),

  bindAddon: (appId: string, addon: string, environments?: string[]) =>
    request<any>(`/apps/${appId}/addons`,
      { method: 'POST', body: JSON.stringify({ addon, environments }) }),

  unbindAddon: (appId: string, addon: string) =>
    request<any>(`/apps/${appId}/addons/${addon}`, { method: 'DELETE' }),

  listProjectMembers: (projectId: string) =>
    request<{ items: any[]; total: number }>(`/projects/${projectId}/members`),
  addProjectMember: (projectId: string, data: { userId: string; role?: string }) =>
    request<any>(`/projects/${projectId}/members`, { method: 'POST', body: JSON.stringify(data) }),
  removeProjectMember: (projectId: string, userId: string) =>
    request<void>(`/projects/${projectId}/members/${userId}`, { method: 'DELETE' }),

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

  // Environment Variables (per app+environment, non-secret)
  createAppEnvVars: (appId: string, env: string, data: { data: Record<string, string> }) =>
    request<any>(`/apps/${appId}/envs/${env}/envvars`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  listAppEnvVars: (appId: string, env: string) =>
    request<{ items: { name: string; keys: string[]; values: Record<string, string> }[]; total: number }>(`/apps/${appId}/envs/${env}/envvars`),

  deleteAppEnvVarKey: (appId: string, env: string, key: string) =>
    request<void>(`/apps/${appId}/envs/${env}/envvars/${encodeURIComponent(key)}`, { method: 'DELETE' }),

  // Secrets (per app+environment)
  createAppEnvSecret: (appId: string, env: string, data: { data: Record<string, string> }) =>
    request<any>(`/apps/${appId}/envs/${env}/secrets`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  listAppEnvSecrets: (appId: string, env: string) =>
    request<{ items: any[]; total: number }>(`/apps/${appId}/envs/${env}/secrets`),

  revealAppEnvSecretValues: (appId: string, env: string) =>
    request<{ id: string; name: string; values: Record<string, string> }>(`/apps/${appId}/envs/${env}/secrets/reveal`),

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
  createRegistrySecret: (data: {
    name: string; registry: string; username: string; password: string; flavor?: string
    // Omitted, the instance's default applies -- global unless an admin changed it.
    scope?: 'global' | 'project'
    project?: string
  }) =>
    request<any>('/secrets/registry', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  listRegistrySecrets: () =>
    request<{ items: any[]; total: number }>('/secrets/registry'),

  deleteRegistrySecret: (name: string) =>
    request<void>(`/secrets/registry/${name}`, { method: 'DELETE' }),

  // Browsing a registry through a stored credential, so images and tags are picked rather
  // than typed. These report errors instead of returning an empty list: a picker that
  // cannot tell "nothing here" from "the call failed" leaves the user retyping.
  listRegistryRepositories: (name: string) =>
    request<{ repositories: string[]; flavor: string }>(
      `/secrets/registry/${encodeURIComponent(name)}/repositories`),

  listRegistryTags: (name: string, repository: string) =>
    request<{ tags: string[] }>(
      `/secrets/registry/${encodeURIComponent(name)}/tags?repository=${encodeURIComponent(repository)}`),

  testRegistrySecret: (name: string) =>
    request<RegistryTestResult>(
      `/secrets/registry/${encodeURIComponent(name)}/test`, { method: 'POST' }),

  // Shared Secrets (project-scoped)
  createSharedSecret: (projectId: string, data: { name: string; data: Record<string, string>; environments?: string[] }) =>
    request<any>(`/projects/${projectId}/shared-secrets`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  listSharedSecrets: (projectId: string) =>
    request<{ items: any[]; total: number }>(`/projects/${projectId}/shared-secrets`),

  updateSharedSecret: (projectId: string, name: string, data: { data?: Record<string, string>; deleteKeys?: string[] }) =>
    request<any>(`/projects/${projectId}/shared-secrets/${name}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  revealSharedSecret: (projectId: string, name: string) =>
    request<{ id: string; name: string; values: Record<string, string> }>(`/projects/${projectId}/shared-secrets/${name}/reveal`),

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

  // Costs
  getProjectCosts: (projectId: string, window = '30d', groupBy: 'app' | 'environment' = 'app') =>
    request<CostReport>(`/projects/${projectId}/costs?window=${encodeURIComponent(window)}&groupBy=${encodeURIComponent(groupBy)}`),

  // Grouped by environment by default: on an app page the useful breakdown is which
  // environment the money is going to, not a single row restating the app's own name.
  getAppCosts: (appId: string, window = '30d', groupBy: 'app' | 'environment' = 'environment') =>
    request<CostReport>(`/apps/${appId}/costs?window=${encodeURIComponent(window)}&groupBy=${encodeURIComponent(groupBy)}`),

  // Security posture
  getSecurityPosture: () =>
    request<SecurityPosture>('/settings/security'),

  setSecurityPosture: (posture: Partial<SecurityPosture>) =>
    request<{ status: string; note: string }>('/settings/security', {
      method: 'PUT',
      body: JSON.stringify(posture),
    }),

  // Quotas
  getEnvironmentQuota: (projectId: string, env: string) =>
    request<EnvironmentQuota>(`/projects/${projectId}/environments/${encodeURIComponent(env)}/quota`),

  setEnvironmentQuota: (projectId: string, env: string, quota: EnvironmentQuota['quota']) =>
    request<{ environment: string; status: string }>(`/projects/${projectId}/environments/${encodeURIComponent(env)}/quota`, {
      method: 'PUT',
      body: JSON.stringify(quota ?? {}),
    }),

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

  // Alert rules
  createAlertRule: (projectId: string, data: any) =>
    request<any>(`/projects/${projectId}/alerts`, { method: 'POST', body: JSON.stringify(data) }),
  listAlertRules: (projectId: string) =>
    request<{ items: any[]; total: number }>(`/projects/${projectId}/alerts`),
  updateAlertRule: (projectId: string, ruleId: string, data: { enabled: boolean }) =>
    request<any>(`/projects/${projectId}/alerts/${ruleId}`, { method: 'PUT', body: JSON.stringify(data) }),
  deleteAlertRule: (projectId: string, ruleId: string) =>
    request<any>(`/projects/${projectId}/alerts/${ruleId}`, { method: 'DELETE' }),

  // Dependencies
  getAppDependencies: (projectId: string) =>
    request<{ nodes: any[]; edges: any[] }>(`/projects/${projectId}/dependencies`),

  // Scheduled deployments
  createScheduledDeployment: (projectId: string, data: any) =>
    request<any>(`/projects/${projectId}/scheduled-deployments`, { method: 'POST', body: JSON.stringify(data) }),
  listScheduledDeployments: (projectId: string) =>
    request<{ items: any[]; total: number }>(`/projects/${projectId}/scheduled-deployments`),
  cancelScheduledDeployment: (projectId: string, deploymentId: string) =>
    request<any>(`/projects/${projectId}/scheduled-deployments/${deploymentId}`, { method: 'DELETE' }),

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

  // Sleep / Wake / Stop / Start
  sleepApp: (appId: string) =>
    request<any>(`/apps/${appId}/sleep`, { method: 'POST' }),

  wakeApp: (appId: string) =>
    request<any>(`/apps/${appId}/wake`, { method: 'POST' }),

  stopApp: (appId: string) =>
    request<any>(`/apps/${appId}/stop`, { method: 'POST' }),

  startApp: (appId: string) =>
    request<any>(`/apps/${appId}/start`, { method: 'POST' }),

  // Cronjob management
  triggerCronJob: (appId: string, name: string, environment: string) =>
    request<any>(`/apps/${appId}/cronjobs/${name}/trigger`, {
      method: 'POST',
      body: JSON.stringify({ environment }),
    }),

  getCronJobStatuses: (appId: string, environment?: string) => {
    const qs = environment ? `?environment=${environment}` : ''
    return request<any>(`/apps/${appId}/cronjobs/status${qs}`)
  },

  // Pod file browser
  listPodFiles: (appId: string, environment: string, pod: string, path: string, container?: string) => {
    const qs = new URLSearchParams({ environment, pod, path })
    if (container) qs.set('container', container)
    return request<any>(`/apps/${appId}/files?${qs}`)
  },
  readPodFile: (appId: string, environment: string, pod: string, path: string, container?: string) => {
    const qs = new URLSearchParams({ environment, pod, path })
    if (container) qs.set('container', container)
    return request<any>(`/apps/${appId}/files/read?${qs}`)
  },
  writePodFile: (appId: string, data: { environment: string; pod: string; path: string; content: string; container?: string }) =>
    request<any>(`/apps/${appId}/files/write`, { method: 'POST', body: JSON.stringify(data) }),

  // Rate limiting
  getRateLimits: (appId: string, environment: string) =>
    request<any>(`/apps/${appId}/rate-limits?environment=${environment}`),
  updateRateLimits: (appId: string, data: { environment: string; limits: Record<string, string> }) =>
    request<any>(`/apps/${appId}/rate-limits`, { method: 'PUT', body: JSON.stringify(data) }),

  listLogDrains: (project?: string) =>
    request<{ drains: LogDrain[] }>(`/log-drains${project ? `?project=${encodeURIComponent(project)}` : ''}`),
  getLogDrain: (name: string) => request<LogDrain>(`/log-drains/${name}`),
  createLogDrain: (data: LogDrainPayload) =>
    request<LogDrain>('/log-drains', { method: 'POST', body: JSON.stringify(data) }),
  updateLogDrain: (name: string, data: LogDrainPayload) =>
    request<LogDrain>(`/log-drains/${name}`, { method: 'PUT', body: JSON.stringify(data) }),
  deleteLogDrain: (name: string) =>
    request<any>(`/log-drains/${name}`, { method: 'DELETE' }),

  listMiddlewares: (project?: string) =>
    request<{ middlewares: Middleware[] }>(`/middlewares${project ? `?project=${encodeURIComponent(project)}` : ''}`),
  getMiddleware: (name: string) => request<Middleware>(`/middlewares/${name}`),
  createMiddleware: (data: MiddlewarePayload) =>
    request<Middleware>('/middlewares', { method: 'POST', body: JSON.stringify(data) }),
  updateMiddleware: (name: string, data: MiddlewarePayload) =>
    request<Middleware>(`/middlewares/${name}`, { method: 'PUT', body: JSON.stringify(data) }),
  deleteMiddleware: (name: string, force = false) =>
    request<any>(`/middlewares/${name}${force ? '?force=true' : ''}`, { method: 'DELETE' }),

  getAppMiddlewares: (appId: string, environment: string) =>
    request<{ middlewares: string[]; inherited: boolean; environment: string }>(
      `/apps/${appId}/middlewares?environment=${encodeURIComponent(environment)}`),
  updateAppMiddlewares: (appId: string, data: { environment: string; middlewares: string[]; inherit?: boolean }) =>
    request<any>(`/apps/${appId}/middlewares`, { method: 'PUT', body: JSON.stringify(data) }),

  // Environment Cloning
  cloneEnvironment: (projectId: string, envName: string, data: { name: string; branch?: string }) =>
    request<any>(`/projects/${projectId}/environments/${envName}/clone`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // Diagnostics -- why an app is not healthy
  getAppDiagnostics: (appId: string, environment?: string) => {
    const qs = environment ? `?environment=${encodeURIComponent(environment)}` : ''
    return request<any>(`/apps/${appId}/diagnostics${qs}`)
  },

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
  // Git connections. More than one may exist, across providers.
  listGitConnections: (provider?: string) =>
    request<{ items: GitConnection[]; total: number }>(
      `/settings/git-connections${provider ? `?provider=${encodeURIComponent(provider)}` : ''}`),

  createGitConnection: (data: {
    provider: 'gitlab' | 'bitbucket'
    displayName?: string
    baseUrl?: string
    account?: string
    token: string
    webhookSecret?: string
  }) =>
    request<{ id: string; provider: string; displayName: string; host: string; webhookPath: string }>(
      '/settings/git-connections', { method: 'POST', body: JSON.stringify(data) }),

  deleteGitConnection: (id: string) =>
    request<any>(`/settings/git-connections/${id}`, { method: 'DELETE' }),

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

  // SSL certificate providers (cert-manager ClusterIssuers)
  getCertManagerStatus: () =>
    request<{ installed: boolean; namespace: string }>('/settings/ssl-providers/status'),

  // The dashboard's own hostname and certificate
  getUIDomain: () =>
    request<UIDomainStatus>('/settings/ui-domain'),

  setUIDomain: (settings: UIDomainSettings) =>
    request<{ settings: UIDomainSettings; previous: UIDomainSettings; helmValues: string[]; note: string }>(
      '/settings/ui-domain', { method: 'PUT', body: JSON.stringify(settings) }),

  listSSLProviders: () =>
    request<{ providers: SSLProvider[]; certManagerInstalled: boolean; default: string }>('/settings/ssl-providers'),

  createSSLProvider: (data: SSLProviderInput) =>
    request<{ name: string; kind: string }>('/settings/ssl-providers', {
      method: 'POST', body: JSON.stringify(data),
    }),

  updateSSLProvider: (name: string, data: SSLProviderInput) =>
    request<{ name: string; kind: string }>(`/settings/ssl-providers/${encodeURIComponent(name)}`, {
      method: 'PUT', body: JSON.stringify(data),
    }),

  deleteSSLProvider: (name: string, force = false) =>
    request<{ status: string; warning?: string }>(
      `/settings/ssl-providers/${encodeURIComponent(name)}${force ? '?force=true' : ''}`,
      { method: 'DELETE' },
    ),

  setDefaultSSLProvider: (name: string) =>
    request<{ default: string }>('/settings/ssl-providers/default', {
      method: 'PUT', body: JSON.stringify({ name }),
    }),

  // Git helpers
  listRepoBranches: (repo: string, opts?: { provider?: string; host?: string; connectionId?: string }) =>
    request<{ branches: string[] }>(
      `/git/branches?repo=${encodeURIComponent(repo)}` +
      (opts?.provider ? `&provider=${encodeURIComponent(opts.provider)}` : '') +
      (opts?.host ? `&host=${encodeURIComponent(opts.host)}` : '') +
      (opts?.connectionId ? `&connectionId=${encodeURIComponent(opts.connectionId)}` : '')),

  listAccessibleRepos: () =>
    request<{ repos: AccessibleRepo[]; problems?: { connectionName: string; error: string }[] }>('/git/repos'),
}
