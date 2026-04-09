import type {
  AdminAssetsResponse,
  AdminAssetCredentialListResponse,
  AdminAssetDetail,
  AdminAuditRecentResponse,
  AdminAuditEventsResponse,
  AdminAuditEventDetailResponse,
  AdminEffectiveAccessResponse,
  AdminGrantsResponse,
  AdminLDAPSettings,
  AdminLDAPSyncRun,
  AdminLDAPSyncRunsResponse,
  AdminLDAPTestResult,
  AdminRolesResponse,
  AdminSummaryResponse,
  AdminUser,
  AdminUserDetail,
  AdminUsersResponse,
  ConnectorDBeaverLaunchRequest,
  ConnectorBootstrapTokenResponse,
  ConnectorRedisLaunchRequest,
  ConnectorReleaseMetadata,
  ConnectorReleaseVersionsResponse,
  ConnectorShellLaunchRequest,
  ConnectorSFTPLaunchRequest,
  LaunchSessionRequest,
  LaunchSessionResponse,
  MyAccessResponse,
  SessionDetail,
  SessionEventsResponse,
  SessionReplayResponse,
  SessionListResponse,
  User,
} from './types'

type APIError = {
  error?: string
  code?: string
  hint?: string
  details?: string
}

const API_BASE = '/api'
const CONNECTOR_BASE = import.meta.env.VITE_CONNECTOR_BASE ?? 'https://127.0.0.1:9494'
const CONNECTOR_TOKEN_REQUIRED = parseConnectorTokenRequired(import.meta.env.VITE_CONNECTOR_TOKEN_REQUIRED)

function normalizeConnectorBase(raw: string | undefined): string {
  return (raw ?? '').trim().replace(/\/+$/, '')
}

function connectorBaseCandidates(): string[] {
  const candidates = [
    normalizeConnectorBase(CONNECTOR_BASE),
    'https://127.0.0.1:9494',
    'https://localhost:9494',
    'http://127.0.0.1:9494',
    'http://localhost:9494',
  ].filter((v) => v !== '')
  return [...new Set(candidates)]
}

function shouldTryNextConnectorBase(status: number): boolean {
  return status === 404 || status === 502 || status === 503 || status === 504
}

export class ConnectorHandoffError extends Error {
  code?: string
  hint?: string
  details?: string
  status: number

  constructor(message: string, status: number, code?: string, hint?: string, details?: string) {
    super(message)
    this.name = 'ConnectorHandoffError'
    this.status = status
    this.code = code
    this.hint = hint
    this.details = details
  }
}

function parseConnectorTokenRequired(raw: unknown): boolean {
  if (typeof raw !== 'string') {
    return true
  }
  const normalized = raw.trim().toLowerCase()
  if (normalized === '' || normalized === 'true' || normalized === '1' || normalized === 'yes') {
    return true
  }
  if (normalized === 'false' || normalized === '0' || normalized === 'no') {
    return false
  }
  return true
}

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
  })

  if (!response.ok) {
    let message = `request failed (${response.status})`
    try {
      const body = (await response.json()) as APIError
      if (body.error) {
        message = body.error
      }
    } catch {
      // ignore
    }
    throw new Error(message)
  }

  if (response.status === 204) {
    return undefined as T
  }

  return (await response.json()) as T
}

export async function login(username: string, password: string): Promise<void> {
  await requestJSON('/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  })
}

export async function logout(): Promise<void> {
  await requestJSON('/auth/logout', { method: 'POST' })
}

export async function getMe(): Promise<User> {
  return requestJSON('/me', { method: 'GET' })
}

export async function changeMyPassword(currentPassword: string, newPassword: string): Promise<void> {
  await requestJSON('/auth/password', {
    method: 'PUT',
    body: JSON.stringify({
      current_password: currentPassword,
      new_password: newPassword,
    }),
  })
}

export async function getMyAccess(): Promise<MyAccessResponse> {
  return requestJSON('/access/my', { method: 'GET' })
}

export async function getConnectorReleaseMetadata(): Promise<ConnectorReleaseMetadata> {
  return requestJSON('/connector/releases/latest', { method: 'GET' })
}

export async function getConnectorReleaseVersions(): Promise<ConnectorReleaseVersionsResponse> {
  return requestJSON('/connector/releases', { method: 'GET' })
}

export async function issueConnectorBootstrapToken(origin: string): Promise<ConnectorBootstrapTokenResponse> {
  return requestJSON('/connector/bootstrap/issue', {
    method: 'POST',
    body: JSON.stringify({ origin }),
  })
}

export async function createSessionLaunch(
  body: LaunchSessionRequest,
): Promise<LaunchSessionResponse> {
  return requestJSON('/sessions/launch', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export function connectorTokenForHandoff(session: LaunchSessionResponse): string {
  const token = (session.connector_token ?? '').trim()
  if (CONNECTOR_TOKEN_REQUIRED && token === '') {
    throw new Error(
      'Launch response is missing connector_token. Refusing connector handoff because connector token validation is required.',
    )
  }
  return token
}

async function connectorLaunchRequest<TResponse>(
  path: string,
  body: unknown,
): Promise<TResponse> {
  let response: Response | null = null
  let lastError: unknown = null

  for (const base of connectorBaseCandidates()) {
    try {
      response = await fetch(`${base}${path}`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(body),
      })
      if (response.ok || !shouldTryNextConnectorBase(response.status)) {
        break
      }
      response = null
    } catch (err) {
      lastError = err
    }
  }

  if (!response) {
    const hint = lastError instanceof Error ? `: ${lastError.message}` : ''
    throw new Error(`connector handoff failed (network/CORS)${hint}`)
  }

  if (!response.ok) {
    let message = `connector handoff failed (${response.status})`
    let code: string | undefined
    let hint: string | undefined
    let details: string | undefined
    try {
      const payload = (await response.json()) as APIError
      if (payload.error) {
        message = payload.error
      }
      code = payload.code
      hint = payload.hint
      details = payload.details
    } catch {
      // ignore
    }
    if (response.status === 403) {
      message = `${message}. Connector rejected launch authorization (403); verify connector secret alignment and connector_token forwarding.`
    }
    throw new ConnectorHandoffError(message, response.status, code, hint, details)
  }

  return (await response.json()) as TResponse
}

export async function getConnectorVersion(): Promise<string> {
  let response: Response | null = null
  let lastError: unknown = null

  for (const base of connectorBaseCandidates()) {
    try {
      const resp = await fetch(`${base}/version`, { method: 'GET' })
      if (resp.ok) {
        response = resp
        break
      }
      if (!shouldTryNextConnectorBase(resp.status)) {
        response = resp
        break
      }
    } catch (err) {
      lastError = err
    }
  }

  if (!response) {
    const hint = lastError instanceof Error ? `: ${lastError.message}` : ''
    throw new Error(`connector version check failed (network/CORS)${hint}`)
  }
  if (!response.ok) {
    throw new Error(`connector version check failed (${response.status})`)
  }
  const payload = await response.json() as { version?: string }
  const version = (payload.version ?? '').trim()
  if (!version) {
    throw new Error('connector version response missing version')
  }
  return version
}

export async function handoffShellToConnector(
  body: ConnectorShellLaunchRequest,
): Promise<{ hint?: string }> {
  const payload = await connectorLaunchRequest<{
    instructions?: string
  }>('/launch/shell', body)
  return {
    hint: payload.instructions,
  }
}

export async function handoffDBeaverToConnector(
  body: ConnectorDBeaverLaunchRequest,
): Promise<{ hint?: string; diagnostics?: Record<string, unknown> }> {
  const payload = await connectorLaunchRequest<{
    instructions?: string
    diagnostics?: Record<string, unknown>
  }>('/launch/dbeaver', body)
  return { hint: payload.instructions, diagnostics: payload.diagnostics }
}

export async function handoffRedisToConnector(
  body: ConnectorRedisLaunchRequest,
): Promise<{ hint?: string; diagnostics?: Record<string, unknown> }> {
  const payload = await connectorLaunchRequest<{
    instructions?: string
    diagnostics?: Record<string, unknown>
  }>('/launch/redis', body)
  return { hint: payload.instructions, diagnostics: payload.diagnostics }
}

export async function handoffSFTPToConnector(
  body: ConnectorSFTPLaunchRequest,
): Promise<{ hint?: string; diagnostics?: Record<string, unknown> }> {
  const payload = await connectorLaunchRequest<{
    instructions?: string
    diagnostics?: Record<string, unknown>
  }>('/launch/sftp', body)
  return { hint: payload.instructions, diagnostics: payload.diagnostics }
}

export async function recordSessionEvent(
  sessionID: string,
  eventType: 'connector_launch_requested' | 'connector_launch_succeeded' | 'connector_launch_failed',
  metadata?: Record<string, unknown>,
): Promise<void> {
  await requestJSON(`/sessions/${sessionID}/events`, {
    method: 'POST',
    body: JSON.stringify({
      event_type: eventType,
      metadata,
    }),
  })
}

export async function adminListUsers(): Promise<AdminUsersResponse> {
  return requestJSON('/admin/users', { method: 'GET' })
}

export async function adminGetUserDetail(userID: string): Promise<AdminUserDetail> {
  return requestJSON(`/admin/users/${userID}`, { method: 'GET' })
}

export async function adminListRoles(): Promise<AdminRolesResponse> {
  return requestJSON('/admin/roles', { method: 'GET' })
}

export async function adminAssignRole(userID: string, roleName: string): Promise<void> {
  await requestJSON(`/admin/users/${userID}/roles`, {
    method: 'POST',
    body: JSON.stringify({ role_name: roleName }),
  })
}

export async function adminRemoveRole(userID: string, roleName: string): Promise<void> {
  await requestJSON(`/admin/users/${userID}/roles/${encodeURIComponent(roleName)}`, {
    method: 'DELETE',
  })
}

export async function adminCreateUser(body: {
  username: string
  password: string
  email?: string
  display_name?: string
}): Promise<AdminUser> {
  return requestJSON('/admin/users', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function adminUpdateUser(userID: string, body: {
  email?: string
  display_name?: string
}): Promise<void> {
  await requestJSON(`/admin/users/${userID}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}

export async function adminSetUserActive(userID: string, isActive: boolean): Promise<void> {
  await requestJSON(`/admin/users/${userID}/active`, {
    method: 'PUT',
    body: JSON.stringify({ is_active: isActive }),
  })
}

export async function adminResetUserPassword(userID: string, password: string): Promise<void> {
  await requestJSON(`/admin/users/${userID}/password`, {
    method: 'PUT',
    body: JSON.stringify({ password }),
  })
}

export async function adminDeleteAsset(assetID: string): Promise<void> {
  await requestJSON(`/admin/assets/${assetID}`, { method: 'DELETE' })
}

export async function adminListAssets(): Promise<AdminAssetsResponse> {
  return requestJSON('/admin/assets', { method: 'GET' })
}

export async function adminCreateAsset(body: {
  name: string
  asset_type: 'linux_vm' | 'database' | 'redis'
  host: string
  port: number
  metadata?: Record<string, unknown>
}): Promise<void> {
  await requestJSON('/admin/assets', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function adminGetAssetDetail(assetID: string): Promise<AdminAssetDetail> {
  return requestJSON(`/admin/assets/${assetID}`, { method: 'GET' })
}

export async function adminUpdateAsset(
  assetID: string,
  body: {
    name: string
    asset_type: 'linux_vm' | 'database' | 'redis'
    host: string
    port: number
    metadata?: Record<string, unknown>
  },
): Promise<void> {
  await requestJSON(`/admin/assets/${assetID}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}

export async function adminListAssetCredentials(assetID: string): Promise<AdminAssetCredentialListResponse> {
  return requestJSON(`/admin/assets/${assetID}/credentials`, { method: 'GET' })
}

export async function adminUpsertAssetCredential(
  assetID: string,
  credentialType: 'password' | 'ssh_key' | 'db_password',
  body: {
    username?: string
    secret: string
    metadata?: Record<string, unknown>
  },
): Promise<void> {
  await requestJSON(`/admin/assets/${assetID}/credentials/${encodeURIComponent(credentialType)}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}

export async function adminListAssetGrants(assetID: string): Promise<AdminGrantsResponse> {
  return requestJSON(`/admin/assets/${assetID}/grants`, { method: 'GET' })
}

export async function adminListUserGrants(userID: string): Promise<AdminGrantsResponse> {
  return requestJSON(`/admin/users/${userID}/grants`, { method: 'GET' })
}

export async function adminAddUserGrant(userID: string, assetID: string, action: string): Promise<void> {
  await requestJSON(`/admin/users/${userID}/grants`, {
    method: 'POST',
    body: JSON.stringify({ asset_id: assetID, action }),
  })
}

export async function adminRemoveUserGrant(userID: string, assetID: string, action: string): Promise<void> {
  await requestJSON(`/admin/users/${userID}/grants/${assetID}/${encodeURIComponent(action)}`, {
    method: 'DELETE',
  })
}

export async function adminGetUserEffectiveAccess(userID: string): Promise<AdminEffectiveAccessResponse> {
  return requestJSON(`/admin/users/${userID}/effective-access`, { method: 'GET' })
}

type UpsertLDAPSettingsBody = {
  provider_mode: 'local' | 'ldap' | 'hybrid'
  enabled: boolean
  host: string
  port: number
  url: string
  base_dn: string
  bind_dn: string
  bind_password: string
  keep_existing_password: boolean
  keep_existing_ca_cert_pem: boolean
  user_search_filter: string
  sync_user_filter: string
  username_attribute: string
  display_name_attribute: string
  surname_attribute: string
  email_attribute: string
  ssh_key_attribute: string
  avatar_attribute: string
  group_search_base_dn: string
  group_search_filter: string
  group_name_attribute: string
  group_role_mapping: string
  ca_cert_pem: string
  use_tls: boolean
  start_tls: boolean
  insecure_skip_verify: boolean
  deactivate_missing_users: boolean
}

export async function adminGetLDAPSettings(): Promise<AdminLDAPSettings> {
  return requestJSON('/admin/ldap/settings', { method: 'GET' })
}

export async function adminUpsertLDAPSettings(body: UpsertLDAPSettingsBody): Promise<AdminLDAPSettings> {
  return requestJSON('/admin/ldap/settings', {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}

export async function adminTestLDAPConnection(body: UpsertLDAPSettingsBody): Promise<AdminLDAPTestResult> {
  return requestJSON('/admin/ldap/test', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function adminTriggerLDAPSync(): Promise<AdminLDAPSyncRun> {
  return requestJSON('/admin/ldap/sync', { method: 'POST' })
}

export async function adminListLDAPSyncRuns(limit = 25): Promise<AdminLDAPSyncRunsResponse> {
  return requestJSON(`/admin/ldap/sync-runs${toQueryString({ limit })}`, { method: 'GET' })
}

type SessionListFilters = {
  status?: string
  action?: string
  asset_type?: string
  user_id?: string
  asset_id?: string
  from?: string
  to?: string
  limit?: number
  after_id?: number
  window_days?: number
}

function toQueryString(filters: SessionListFilters): string {
  const params = new URLSearchParams()
  Object.entries(filters).forEach(([key, value]) => {
    if (value === undefined || value === null || value === '') {
      return
    }
    params.set(key, String(value))
  })
  const text = params.toString()
  return text ? `?${text}` : ''
}

export async function getMySessions(filters: SessionListFilters = {}): Promise<SessionListResponse> {
  return requestJSON(`/sessions/my${toQueryString(filters)}`, { method: 'GET' })
}

export async function getAdminSessions(filters: SessionListFilters = {}): Promise<SessionListResponse> {
  return requestJSON(`/admin/sessions${toQueryString(filters)}`, { method: 'GET' })
}

export async function getAdminSessionsActive(limit = 50): Promise<SessionListResponse> {
  return requestJSON(`/admin/sessions/active${toQueryString({ limit })}`, { method: 'GET' })
}

export async function getAdminSummary(windowDays = 7): Promise<AdminSummaryResponse> {
  return requestJSON(`/admin/summary${toQueryString({ window_days: windowDays })}`, { method: 'GET' })
}

export async function getAdminAuditRecent(limit = 50): Promise<AdminAuditRecentResponse> {
  return requestJSON(`/admin/audit/recent${toQueryString({ limit })}`, { method: 'GET' })
}

type AdminAuditFilters = {
  event_type?: string
  user_id?: string
  asset_id?: string
  session_id?: string
  action?: string
  from?: string
  to?: string
  limit?: number
}

export async function getAdminAuditEvents(filters: AdminAuditFilters = {}): Promise<AdminAuditEventsResponse> {
  return requestJSON(`/admin/audit/events${toQueryString(filters)}`, { method: 'GET' })
}

export async function getAdminAuditEventDetail(eventID: number): Promise<AdminAuditEventDetailResponse> {
  return requestJSON(`/admin/audit/events/${eventID}`, { method: 'GET' })
}

export async function getSessionDetail(sessionID: string): Promise<SessionDetail> {
  return requestJSON(`/sessions/${sessionID}`, { method: 'GET' })
}

export async function getSessionEvents(
  sessionID: string,
  filters: { after_id?: number; limit?: number } = {},
): Promise<SessionEventsResponse> {
  const query = toQueryString({
    after_id: filters.after_id,
    limit: filters.limit,
  })
  return requestJSON(`/sessions/${sessionID}/events${query}`, { method: 'GET' })
}

export async function getSessionReplay(
  sessionID: string,
  filters: { after_id?: number; limit?: number } = {},
): Promise<SessionReplayResponse> {
  const query = toQueryString({
    after_id: filters.after_id,
    limit: filters.limit,
  })
  return requestJSON(`/sessions/${sessionID}/replay${query}`, { method: 'GET' })
}
