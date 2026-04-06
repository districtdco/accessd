export type User = {
  id: string
  username: string
  email?: string
  display_name?: string
  roles: string[]
}

export type AccessPoint = {
  asset_id: string
  asset_name: string
  asset_type: 'linux_vm' | 'database' | 'redis'
  host: string
  port: number
  endpoint: string
  allowed_actions: string[]
}

export type MyAccessResponse = {
  items: AccessPoint[]
}

export type LaunchSessionRequest = {
  asset_id: string
  action: 'shell' | 'sftp' | 'dbeaver' | 'redis'
}

export type ShellLaunchConnection = {
  proxy_host: string
  proxy_port: number
  username: string
  token: string
  expires_at: string
}

export type DBeaverLaunchConnection = {
  engine: string
  host: string
  port: number
  database?: string
  username: string
  password: string
  ssl_mode?: string
  expires_at: string
}

export type SFTPLaunchConnection = {
  host: string
  port: number
  username: string
  password: string
  path?: string
  expires_at: string
}

export type RedisLaunchConnection = {
  redis_host: string
  redis_port: number
  redis_username?: string
  redis_password: string
  redis_database?: number
  redis_tls?: boolean
  redis_insecure_skip_verify_tls?: boolean
  expires_at: string
}

export type LaunchSessionResponse = {
  session_id: string
  launch_type: 'shell' | 'sftp' | 'dbeaver' | 'redis'
  launch: ShellLaunchConnection | SFTPLaunchConnection | DBeaverLaunchConnection | RedisLaunchConnection
}

export type ConnectorShellLaunchRequest = {
  session_id: string
  asset_id: string
  asset_name: string
  launch: ShellLaunchConnection
}

export type ConnectorDBeaverLaunchRequest = {
  session_id: string
  asset_id: string
  asset_name: string
  launch: DBeaverLaunchConnection
}

export type ConnectorSFTPLaunchRequest = {
  session_id: string
  asset_id: string
  asset_name: string
  launch: SFTPLaunchConnection
}

export type ConnectorRedisLaunchRequest = {
  session_id: string
  asset_id: string
  asset_name: string
  launch: RedisLaunchConnection
}

export type AdminUser = {
  id: string
  username: string
  email?: string
  display_name?: string
  is_active: boolean
  roles: string[]
}

export type AdminUsersResponse = {
  items: AdminUser[]
}

export type AdminRole = {
  id: string
  name: string
  description?: string
}

export type AdminRolesResponse = {
  items: AdminRole[]
}

export type AdminGroup = {
  id: string
  name: string
  description?: string
  member_count?: number
}

export type AdminGroupsResponse = {
  items: AdminGroup[]
}

export type AdminUserDetail = {
  id: string
  username: string
  email?: string
  display_name?: string
  is_active: boolean
  roles: string[]
  groups: AdminGroup[]
}

export type AdminAsset = {
  id: string
  name: string
  asset_type: 'linux_vm' | 'database' | 'redis'
  host: string
  port: number
  endpoint: string
  grant_count: number
  credential_count: number
}

export type AdminAssetsResponse = {
  items: AdminAsset[]
}

export type AdminCredentialSummary = {
  id: string
  credential_type: 'password' | 'ssh_key' | 'db_password'
  username?: string
  algorithm: string
  key_id: string
  metadata: Record<string, unknown>
  created_at: string
  updated_at: string
  last_rotated_at?: string
  secret_available: boolean
}

export type AdminAssetDetail = {
  id: string
  name: string
  asset_type: 'linux_vm' | 'database' | 'redis'
  host: string
  port: number
  endpoint: string
  metadata: Record<string, unknown>
  credentials: AdminCredentialSummary[]
}

export type AdminAssetCredentialListResponse = {
  items: AdminCredentialSummary[]
}

export type AdminGrant = {
  subject_type: 'user' | 'group'
  subject_id: string
  subject_name: string
  asset_id: string
  asset_name: string
  action: 'shell' | 'sftp' | 'dbeaver' | 'redis'
  effect: 'allow' | 'deny'
  created_at: string
}

export type AdminGrantsResponse = {
  items: AdminGrant[]
}

export type AdminEffectiveAction = {
  action: 'shell' | 'sftp' | 'dbeaver' | 'redis'
  sources: string[]
}

export type AdminEffectiveAccessItem = {
  asset_id: string
  asset_name: string
  actions: AdminEffectiveAction[]
}

export type AdminEffectiveAccessResponse = {
  items: AdminEffectiveAccessItem[]
}

export type SessionSummaryUser = {
  id: string
  username: string
}

export type SessionSummaryAsset = {
  id: string
  name: string
  asset_type: 'linux_vm' | 'database' | 'redis'
}

export type SessionLifecycleSummary = {
  started: boolean
  ended: boolean
  failed?: boolean
  shell_started?: boolean
  connector_requested?: boolean
  connector_succeeded?: boolean
  connector_failed?: boolean
  event_count?: number
  first_event_at?: string
  last_event_at?: string
}

export type SessionSummary = {
  session_id: string
  user: SessionSummaryUser
  asset: SessionSummaryAsset
  action: 'shell' | 'sftp' | 'dbeaver' | 'redis'
  launch_type: string
  status: 'pending' | 'active' | 'completed' | 'failed' | 'terminated' | 'expired'
  started_at?: string
  ended_at?: string
  created_at: string
  duration_seconds?: number
  lifecycle: SessionLifecycleSummary
}

export type SessionListResponse = {
  items: SessionSummary[]
}

export type SessionDetail = {
  session_id: string
  user: SessionSummaryUser
  asset: SessionSummaryAsset
  action: 'shell' | 'sftp' | 'dbeaver' | 'redis'
  launch_type: string
  protocol: string
  status: 'pending' | 'active' | 'completed' | 'failed' | 'terminated' | 'expired'
  launched_via: string
  started_at?: string
  ended_at?: string
  created_at: string
  duration_seconds?: number
  lifecycle: SessionLifecycleSummary
}

export type SessionEventUser = {
  id?: string
  username?: string
}

export type SessionEvent = {
  id: number
  event_type: string
  event_time: string
  actor_user?: SessionEventUser
  payload: Record<string, unknown>
  transcript?: {
    direction: 'in' | 'out'
    stream?: string
    size?: number
    text?: string
  }
}

export type SessionEventsResponse = {
  items: SessionEvent[]
  next_after_id?: number
}

export type SessionReplayChunk = {
  event_id: number
  event_time: string
  direction: 'in' | 'out'
  stream?: string
  size?: number
  text: string
}

export type SessionReplayResponse = {
  session_id: string
  supported: boolean
  approximate: boolean
  items: SessionReplayChunk[]
  next_after_id?: number
}

export type AdminSummaryMetricSet = {
  recent_sessions: number
  active_sessions: number
  failed_sessions: number
  shell_launches: number
  dbeaver_launches: number
}

export type AdminSummaryActionCount = {
  action: string
  count: number
}

export type AdminSummaryResponse = {
  window_days: number
  generated_at: string
  metrics: AdminSummaryMetricSet
  by_action: AdminSummaryActionCount[]
}

export type AdminAuditAsset = {
  id: string
  name?: string
  asset_type?: 'linux_vm' | 'database' | 'redis'
}

export type AdminAuditSession = {
  id: string
  action?: string
  status?: string
  created_at?: string
}

export type AdminAuditItem = {
  id: number
  event_time: string
  event_type: string
  action?: string
  outcome?: string
  actor_user?: SessionEventUser
  asset?: AdminAuditAsset
  session?: AdminAuditSession
  session_id?: string
  metadata: Record<string, unknown>
}

export type AdminAuditRecentResponse = {
  items: AdminAuditItem[]
}

export type AdminAuditEventsResponse = {
  items: AdminAuditItem[]
}

export type AdminAuditEventDetailResponse = {
  item: AdminAuditItem
}
