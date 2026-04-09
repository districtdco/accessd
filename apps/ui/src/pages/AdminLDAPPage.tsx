import { useEffect, useMemo, useState } from 'react'
import {
  adminGetLDAPSettings,
  adminListLDAPSyncRuns,
  adminTestLDAPConnection,
  adminTriggerLDAPSync,
  adminUpsertLDAPSettings,
} from '../api'
import type { AdminLDAPSettings, AdminLDAPSyncRun } from '../types'
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  Checkbox,
  EmptyRow,
  ErrorState,
  Input,
  LoadingState,
  PageHeader,
  PaginationControls,
  Select,
  SuccessState,
  Table,
  TabNav,
  Td,
  TextArea,
  Th,
} from '../components/ui'

const providerModeOptions = [
  { value: 'local', label: 'Local only' },
  { value: 'ldap', label: 'LDAP only' },
  { value: 'hybrid', label: 'Hybrid (LDAP + Local fallback)' },
]

const securityProtocolOptions = [
  { value: 'ldap', label: 'LDAP' },
  { value: 'ldaps', label: 'LDAPS' },
  { value: 'starttls', label: 'StartTLS' },
]

type FormState = {
  provider_mode: 'local' | 'ldap' | 'hybrid'
  security_protocol: 'ldap' | 'ldaps' | 'starttls'
  enabled: boolean
  host: string
  port: string
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

function deriveSecurityProtocol(settings: AdminLDAPSettings): 'ldap' | 'ldaps' | 'starttls' {
  if (settings.start_tls) return 'starttls'
  if (settings.use_tls || settings.url.trim().toLowerCase().startsWith('ldaps://')) return 'ldaps'
  return 'ldap'
}

function settingsToForm(settings: AdminLDAPSettings): FormState {
  return {
    provider_mode: settings.provider_mode,
    security_protocol: deriveSecurityProtocol(settings),
    enabled: settings.enabled,
    host: settings.host,
    port: String(settings.port || 389),
    url: settings.url,
    base_dn: settings.base_dn,
    bind_dn: settings.bind_dn,
    bind_password: '',
    keep_existing_password: settings.has_bind_password,
    keep_existing_ca_cert_pem: settings.has_ca_cert_pem,
    user_search_filter: settings.user_search_filter,
    sync_user_filter: settings.sync_user_filter,
    username_attribute: settings.username_attribute,
    display_name_attribute: settings.display_name_attribute,
    surname_attribute: settings.surname_attribute,
    email_attribute: settings.email_attribute,
    ssh_key_attribute: settings.ssh_key_attribute,
    avatar_attribute: settings.avatar_attribute,
    group_search_base_dn: settings.group_search_base_dn,
    group_search_filter: settings.group_search_filter,
    group_name_attribute: settings.group_name_attribute,
    group_role_mapping: settings.group_role_mapping,
    ca_cert_pem: '',
    use_tls: settings.use_tls,
    start_tls: settings.start_tls,
    insecure_skip_verify: settings.insecure_skip_verify,
    deactivate_missing_users: settings.deactivate_missing_users,
  }
}

function formToRequest(form: FormState) {
  const securityProtocol = form.security_protocol
  const useTLS = securityProtocol === 'ldaps'
  const startTLS = securityProtocol === 'starttls'
  return {
    ...form,
    port: Number(form.port),
    use_tls: useTLS,
    start_tls: startTLS,
  }
}

export function AdminLDAPPage() {
  const [tab, setTab] = useState<'configuration' | 'sync'>('configuration')
  const [settings, setSettings] = useState<AdminLDAPSettings | null>(null)
  const [form, setForm] = useState<FormState | null>(null)
  const [syncRuns, setSyncRuns] = useState<AdminLDAPSyncRun[]>([])
  const [loading, setLoading] = useState(true)
  const [syncLoading, setSyncLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [triggeringSync, setTriggeringSync] = useState(false)
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [syncPage, setSyncPage] = useState(1)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)
  const [testMessage, setTestMessage] = useState<string | null>(null)

  const loadSettings = async () => {
    setLoading(true)
    setError(null)
    try {
      const response = await adminGetLDAPSettings()
      setSettings(response)
      setForm(settingsToForm(response))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load ldap settings')
    } finally {
      setLoading(false)
    }
  }

  const loadSyncRuns = async () => {
    setSyncLoading(true)
    try {
      const response = await adminListLDAPSyncRuns(50)
      setSyncRuns(response.items)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load ldap sync runs')
    } finally {
      setSyncLoading(false)
    }
  }

  useEffect(() => {
    void loadSettings()
    void loadSyncRuns()
  }, [])

  useEffect(() => {
    setSyncPage(1)
  }, [syncRuns.length])

  const syncPageSize = 10
  const syncTotalPages = Math.max(1, Math.ceil(syncRuns.length / syncPageSize))
  const syncCurrentPage = Math.min(syncPage, syncTotalPages)
  const pagedSyncRuns = useMemo(() => {
    return syncRuns.slice((syncCurrentPage - 1) * syncPageSize, syncCurrentPage * syncPageSize)
  }, [syncRuns, syncCurrentPage])

  const formEnabled = form?.enabled ?? false
  const configStatus = useMemo(() => {
    if (!settings) return 'Unknown'
    if (!settings.enabled) return 'Disabled'
    return settings.has_bind_password ? 'Configured' : 'Needs bind password'
  }, [settings])

  const updateField = <K extends keyof FormState>(key: K, value: FormState[K]) => {
    setForm((current) => {
      if (!current) return current
      return { ...current, [key]: value }
    })
  }

  const save = async () => {
    if (!form) return
    setSaving(true)
    setError(null)
    setMessage(null)
    setTestMessage(null)
    try {
      const saved = await adminUpsertLDAPSettings(formToRequest(form))
      setSettings(saved)
      setForm(settingsToForm(saved))
      setMessage('LDAP settings saved')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to save ldap settings')
    } finally {
      setSaving(false)
    }
  }

  const testConnection = async () => {
    if (!form) return
    setTesting(true)
    setError(null)
    setTestMessage(null)
    try {
      const result = await adminTestLDAPConnection(formToRequest(form))
      setTestMessage(result.message)
      if (!result.bind_ok) {
        setError(result.message)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'ldap test failed')
    } finally {
      setTesting(false)
    }
  }

  const triggerSync = async () => {
    setTriggeringSync(true)
    setError(null)
    setMessage(null)
    try {
      await adminTriggerLDAPSync()
      setMessage('LDAP sync completed successfully')
      await loadSyncRuns()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'ldap sync failed')
      await loadSyncRuns()
    } finally {
      setTriggeringSync(false)
    }
  }

  return (
    <>
      <PageHeader title="Directory & LDAP">
        {settings && (
          <Badge color={settings.enabled ? 'green' : 'gray'}>
            {configStatus}
          </Badge>
        )}
      </PageHeader>

      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {message && <div className="mb-4"><SuccessState message={message} /></div>}

      <TabNav
        tabs={[
          { id: 'configuration', label: 'Configuration' },
          { id: 'sync', label: 'Sync' },
        ]}
        active={tab}
        onChange={(id) => setTab(id as 'configuration' | 'sync')}
      />

      {loading && <LoadingState message="Loading LDAP settings..." />}

      {!loading && form && tab === 'configuration' && (
        <Card>
          <CardHeader title="LDAP Connection & Attribute Mapping" />
          <CardBody>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <Select label="Auth Provider Mode" value={form.provider_mode} onChange={(v) => updateField('provider_mode', v as FormState['provider_mode'])} options={providerModeOptions} />
              <Select label="Security Protocol" value={form.security_protocol} onChange={(v) => updateField('security_protocol', v as FormState['security_protocol'])} options={securityProtocolOptions} />
              <Checkbox
                label="Enable LDAP in admin config"
                checked={form.enabled}
                onChange={(v) => updateField('enabled', v)}
                hint="When disabled, sync and LDAP settings are retained but not used."
              />
            </div>

            <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <Input label="LDAP Host" value={form.host} onChange={(v) => updateField('host', v)} placeholder="ldap.example.com" />
              <Input label="LDAP Port" value={form.port} onChange={(v) => updateField('port', v)} placeholder="389" />
              <Input label="User Search Base DN" value={form.base_dn} onChange={(v) => updateField('base_dn', v)} placeholder="dc=example,dc=com" />
              <Input label="Bind DN" value={form.bind_dn} onChange={(v) => updateField('bind_dn', v)} placeholder="cn=svc_ldap,ou=Service,dc=example,dc=com" />
              <Input
                label={settings?.has_bind_password ? 'Bind Password (optional update)' : 'Bind Password'}
                value={form.bind_password}
                onChange={(v) => {
                  updateField('bind_password', v)
                  if (v.trim().length > 0) {
                    updateField('keep_existing_password', false)
                  } else if (settings?.has_bind_password) {
                    updateField('keep_existing_password', true)
                  }
                }}
                type="password"
                placeholder={settings?.has_bind_password ? 'Leave blank to keep existing secret' : 'Enter bind password'}
              />
            </div>

            {settings?.has_bind_password && (
              <div className="mt-2">
                <Checkbox
                  label="Clear stored bind password on save"
                  checked={!form.keep_existing_password && form.bind_password.trim().length === 0}
                  onChange={(v) => {
                    if (v) {
                      updateField('bind_password', '')
                      updateField('keep_existing_password', false)
                      return
                    }
                    updateField('keep_existing_password', true)
                  }}
                />
              </div>
            )}

            <div className="mt-6 grid gap-4 sm:grid-cols-2">
              <Input label="User Filter" value={form.user_search_filter} onChange={(v) => updateField('user_search_filter', v)} placeholder="(&(objectClass=user)(sAMAccountName={{username}}))" />
              <Input label="Username Attribute" value={form.username_attribute} onChange={(v) => updateField('username_attribute', v)} placeholder="sAMAccountName" />
              <Input label="First/Display Name Attribute" value={form.display_name_attribute} onChange={(v) => updateField('display_name_attribute', v)} placeholder="cn" />
              <Input label="Surname Attribute" value={form.surname_attribute} onChange={(v) => updateField('surname_attribute', v)} placeholder="sn" />
              <Input label="Email Attribute" value={form.email_attribute} onChange={(v) => updateField('email_attribute', v)} placeholder="mail" />
            </div>

            <div className="mt-6">
              <Button variant="secondary" onClick={() => setShowAdvanced((v) => !v)}>
                {showAdvanced ? 'Hide Advanced Settings' : 'Show Advanced Settings'}
              </Button>
            </div>

            {showAdvanced && (
              <div className="mt-4 grid gap-4">
                <TextArea
                  label={settings?.has_ca_cert_pem ? 'CA Certificate PEM (optional update)' : 'CA Certificate PEM (optional)'}
                  value={form.ca_cert_pem}
                  onChange={(v) => {
                    updateField('ca_cert_pem', v)
                    if (v.trim().length > 0) {
                      updateField('keep_existing_ca_cert_pem', false)
                    } else if (settings?.has_ca_cert_pem) {
                      updateField('keep_existing_ca_cert_pem', true)
                    }
                  }}
                  rows={4}
                  placeholder={settings?.has_ca_cert_pem ? 'Leave blank to keep existing CA certificate' : '-----BEGIN CERTIFICATE----- ...'}
                />
                {settings?.has_ca_cert_pem && (
                  <Checkbox
                    label="Clear stored CA certificate on save"
                    checked={!form.keep_existing_ca_cert_pem && form.ca_cert_pem.trim().length === 0}
                    onChange={(v) => {
                      if (v) {
                        updateField('ca_cert_pem', '')
                        updateField('keep_existing_ca_cert_pem', false)
                        return
                      }
                      updateField('keep_existing_ca_cert_pem', true)
                    }}
                  />
                )}
                <Checkbox
                  label="Skip TLS cert verification"
                  checked={form.insecure_skip_verify}
                  onChange={(v) => updateField('insecure_skip_verify', v)}
                  hint="Use only for controlled lab environments."
                />
                <Checkbox
                  label="Deactivate users missing from LDAP on sync"
                  checked={form.deactivate_missing_users}
                  onChange={(v) => updateField('deactivate_missing_users', v)}
                  hint="Users are deactivated (not deleted), preserving session/audit history."
                />
              </div>
            )}

            <div className="mt-4">
              <p className="text-sm text-gray-600">
                Test connection uses the same effective bind credentials as sync, including stored secrets when password fields are left blank.
              </p>
            </div>

            {testMessage && <div className="mt-4"><SuccessState message={testMessage} /></div>}

            <div className="mt-6 flex flex-wrap gap-2">
              <Button disabled={testing || !formEnabled} variant="secondary" onClick={() => void testConnection()}>
                {testing ? 'Testing...' : 'Test Connection'}
              </Button>
              <Button disabled={saving} onClick={() => void save()}>
                {saving ? 'Saving...' : 'Save Settings'}
              </Button>
            </div>
          </CardBody>
        </Card>
      )}

      {tab === 'sync' && (
        <div className="space-y-4">
          <Card>
            <CardHeader title="Manual LDAP Sync" />
            <CardBody>
              <p className="text-sm text-gray-600">
                Run a one-time sync to create/update LDAP-backed users and deactivate users who no longer match directory criteria.
                Historical sessions and audit events remain intact.
              </p>
              <div className="mt-4">
                <Button disabled={triggeringSync} onClick={() => void triggerSync()}>
                  {triggeringSync ? 'Syncing...' : 'Run Sync Now'}
                </Button>
              </div>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Sync History" />
            {syncLoading ? (
              <LoadingState message="Loading sync runs..." />
            ) : (
              <>
                <Table>
                  <thead>
                    <tr>
                      <Th>Started</Th>
                      <Th>Status</Th>
                      <Th>Discovered</Th>
                      <Th>Created</Th>
                      <Th>Updated</Th>
                      <Th>Reactivated</Th>
                      <Th>Deactivated</Th>
                      <Th>Error</Th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {pagedSyncRuns.map((run) => (
                      <tr key={run.id} className="hover:bg-gray-50">
                        <Td>{new Date(run.started_at).toLocaleString()}</Td>
                        <Td>
                          <Badge color={run.status === 'success' ? 'green' : run.status === 'failed' ? 'red' : 'yellow'}>
                            {run.status}
                          </Badge>
                        </Td>
                        <Td>{run.summary.discovered}</Td>
                        <Td>{run.summary.created}</Td>
                        <Td>{run.summary.updated}</Td>
                        <Td>{run.summary.reactivated}</Td>
                        <Td>{run.summary.deactivated}</Td>
                        <Td className="max-w-[360px] whitespace-normal break-words">{run.error || '-'}</Td>
                      </tr>
                    ))}
                    {pagedSyncRuns.length === 0 && <EmptyRow colSpan={8} message="No sync runs yet." />}
                  </tbody>
                </Table>
                <PaginationControls
                  page={syncCurrentPage}
                  totalPages={syncTotalPages}
                  totalItems={syncRuns.length}
                  pageSize={syncPageSize}
                  onPageChange={setSyncPage}
                />
              </>
            )}
          </Card>
        </div>
      )}
    </>
  )
}
