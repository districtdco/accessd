import type {
  AdminAssetsResponse,
  AdminAssetCredentialListResponse,
  AdminAssetDetail,
  AdminAuditRecentResponse,
  AdminAuditEventsResponse,
  AdminAuditEventDetailResponse,
  AdminEffectiveAccessResponse,
  AdminGrantsResponse,
  AdminRolesResponse,
  AdminSummaryResponse,
  AdminUserDetail,
  AdminUsersResponse,
  ConnectorDBeaverLaunchRequest,
  ConnectorRedisLaunchRequest,
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
}

const API_BASE = '/api'
const CONNECTOR_BASE = import.meta.env.VITE_CONNECTOR_BASE ?? '/connector'

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

export async function getMyAccess(): Promise<MyAccessResponse> {
  return requestJSON('/access/my', { method: 'GET' })
}

export async function createSessionLaunch(
  body: LaunchSessionRequest,
): Promise<LaunchSessionResponse> {
  return requestJSON('/sessions/launch', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function handoffShellToConnector(
  body: ConnectorShellLaunchRequest,
): Promise<{ tokenCopied: boolean; hint?: string }> {
  const response = await fetch(`${CONNECTOR_BASE}/launch/shell`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body),
  })

  if (!response.ok) {
    let message = `connector handoff failed (${response.status})`
    try {
      const payload = (await response.json()) as APIError
      if (payload.error) {
        message = payload.error
        if (payload.hint) {
          message += ` (${payload.hint})`
        }
      }
    } catch {
      // ignore
    }
    throw new Error(message)
  }

  const payload = (await response.json()) as {
    token_copied?: boolean
    instructions?: string
  }
  return {
    tokenCopied: payload.token_copied === true,
    hint: payload.instructions,
  }
}

export async function handoffDBeaverToConnector(
  body: ConnectorDBeaverLaunchRequest,
): Promise<{ hint?: string; diagnostics?: Record<string, unknown> }> {
  const response = await fetch(`${CONNECTOR_BASE}/launch/dbeaver`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body),
  })

  if (!response.ok) {
    let message = `connector handoff failed (${response.status})`
    try {
      const payload = (await response.json()) as APIError
      if (payload.error) {
        message = payload.error
        if (payload.hint) {
          message += ` (${payload.hint})`
        }
      }
    } catch {
      // ignore
    }
    throw new Error(message)
  }

  const payload = (await response.json()) as {
    instructions?: string
    diagnostics?: Record<string, unknown>
  }
  return { hint: payload.instructions, diagnostics: payload.diagnostics }
}

export async function handoffRedisToConnector(
  body: ConnectorRedisLaunchRequest,
): Promise<{ hint?: string; diagnostics?: Record<string, unknown> }> {
  const response = await fetch(`${CONNECTOR_BASE}/launch/redis`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body),
  })

  if (!response.ok) {
    let message = `connector handoff failed (${response.status})`
    try {
      const payload = (await response.json()) as APIError
      if (payload.error) {
        message = payload.error
        if (payload.hint) {
          message += ` (${payload.hint})`
        }
      }
    } catch {
      // ignore
    }
    throw new Error(message)
  }

  const payload = (await response.json()) as {
    instructions?: string
    diagnostics?: Record<string, unknown>
  }
  return { hint: payload.instructions, diagnostics: payload.diagnostics }
}

export async function handoffSFTPToConnector(
  body: ConnectorSFTPLaunchRequest,
): Promise<{ hint?: string; diagnostics?: Record<string, unknown> }> {
  const response = await fetch(`${CONNECTOR_BASE}/launch/sftp`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body),
  })

  if (!response.ok) {
    let message = `connector handoff failed (${response.status})`
    try {
      const payload = (await response.json()) as APIError
      if (payload.error) {
        message = payload.error
        if (payload.hint) {
          message += ` (${payload.hint})`
        }
      }
    } catch {
      // ignore
    }
    throw new Error(message)
  }

  const payload = (await response.json()) as {
    instructions?: string
    diagnostics?: Record<string, unknown>
  }
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
