import { useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  adminAddUserGrant,
  adminAssignRole,
  adminGetUserDetail,
  adminGetUserEffectiveAccess,
  adminListAssets,
  adminListRoles,
  adminListUserGrants,
  adminRemoveRole,
  adminRemoveUserGrant,
  adminResetUserPassword,
  adminSetUserActive,
  adminUpdateUser,
} from '../api'
import type {
  AdminAsset,
  AdminEffectiveAccessItem,
  AdminGrant,
  AdminRole,
  AdminUserDetail,
} from '../types'
import { Badge, Button, Card, CardBody, CardHeader, EmptyRow, ErrorState, InfoRow, Input, LoadingState, PageHeader, Select, SuccessState, Table, Td, Th } from '../components/ui'

const SUPPORTED_ACTIONS = [
  { value: 'shell', label: 'Shell' },
  { value: 'sftp', label: 'SFTP' },
  { value: 'dbeaver', label: 'DBeaver' },
  { value: 'redis', label: 'Redis' },
]

export function AdminUserDetailPage() {
  const params = useParams<{ userID: string }>()
  const userID = params.userID || ''

  const [detail, setDetail] = useState<AdminUserDetail | null>(null)
  const [roles, setRoles] = useState<AdminRole[]>([])
  const [assets, setAssets] = useState<AdminAsset[]>([])
  const [grants, setGrants] = useState<AdminGrant[]>([])
  const [effective, setEffective] = useState<AdminEffectiveAccessItem[]>([])

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)

  const [selectedRole, setSelectedRole] = useState<string>('')
  const [selectedAssetID, setSelectedAssetID] = useState<string>('')
  const [selectedAction, setSelectedAction] = useState<string>('shell')

  const [editEmail, setEditEmail] = useState('')
  const [editDisplayName, setEditDisplayName] = useState('')
  const [savingProfile, setSavingProfile] = useState(false)
  const [newPassword, setNewPassword] = useState('')
  const [savingPassword, setSavingPassword] = useState(false)
  const [togglingActive, setTogglingActive] = useState(false)

  const loadData = async () => {
    if (userID === '') {
      setError('missing user id')
      return
    }

    setLoading(true)
    setError(null)
    try {
      const [detailResp, rolesResp, assetsResp, grantsResp, effectiveResp] = await Promise.all([
        adminGetUserDetail(userID),
        adminListRoles(),
        adminListAssets(),
        adminListUserGrants(userID),
        adminGetUserEffectiveAccess(userID),
      ])

      setDetail(detailResp)
      setEditEmail(detailResp.email || '')
      setEditDisplayName(detailResp.display_name || '')
      setRoles(rolesResp.items)
      setAssets(assetsResp.items)
      setGrants(grantsResp.items)
      setEffective(effectiveResp.items)

      if (assetsResp.items.length > 0) {
        setSelectedAssetID((prev) => {
          if (prev !== '') {
            return prev
          }
          return assetsResp.items[0].id
        })
      }

      const availableRole = rolesResp.items.find((role) => detailResp.roles.includes(role.name) === false)
      if (availableRole) {
        setSelectedRole(availableRole.name)
      } else {
        setSelectedRole('')
      }
    } catch (err) {
      const messageText = err instanceof Error ? err.message : 'failed to load user detail'
      setError(messageText)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void loadData()
  }, [userID])

  const assignableRoles = useMemo(() => {
    if (detail === null) {
      return [] as AdminRole[]
    }
    return roles.filter((role) => detail.roles.includes(role.name) === false)
  }, [detail, roles])

  const addRole = async () => {
    if (selectedRole === '') return
    setMessage(null)
    try {
      await adminAssignRole(userID, selectedRole)
      setMessage('Role assigned')
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to assign role')
    }
  }

  const removeRole = async (roleName: string) => {
    if (!window.confirm(`Remove role "${roleName}" from ${detail?.username || 'this user'}?`)) {
      return
    }
    setMessage(null)
    try {
      await adminRemoveRole(userID, roleName)
      setMessage('Role removed')
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to remove role')
    }
  }

  const addGrant = async () => {
    if (selectedAssetID === '' || selectedAction === '') return
    setMessage(null)
    try {
      await adminAddUserGrant(userID, selectedAssetID, selectedAction)
      setMessage('Grant added')
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to add grant')
    }
  }

  const removeGrant = async (assetID: string, action: string) => {
    if (!window.confirm(`Remove ${action} grant from this user?`)) {
      return
    }
    setMessage(null)
    try {
      await adminRemoveUserGrant(userID, assetID, action)
      setMessage('Grant removed')
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to remove grant')
    }
  }

  const saveProfile = async () => {
    setMessage(null)
    setError(null)
    setSavingProfile(true)
    try {
      await adminUpdateUser(userID, {
        email: editEmail.trim() || undefined,
        display_name: editDisplayName.trim() || undefined,
      })
      setMessage('Profile updated')
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to update profile')
    } finally {
      setSavingProfile(false)
    }
  }

  const toggleActive = async () => {
    if (!detail) return
    const next = !detail.is_active
    if (!window.confirm(`${next ? 'Activate' : 'Deactivate'} user "${detail.username}"?`)) return
    setMessage(null)
    setError(null)
    setTogglingActive(true)
    try {
      await adminSetUserActive(userID, next)
      setMessage(next ? 'User activated' : 'User deactivated')
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to change user status')
    } finally {
      setTogglingActive(false)
    }
  }

  const resetPassword = async () => {
    if (!newPassword.trim()) {
      setError('Password is required')
      return
    }
    setMessage(null)
    setError(null)
    setSavingPassword(true)
    try {
      await adminResetUserPassword(userID, newPassword)
      setNewPassword('')
      setMessage('Password reset successfully')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to reset password')
    } finally {
      setSavingPassword(false)
    }
  }

  return (
    <>
      <div className="mb-2 flex items-center gap-2 text-sm text-gray-500">
        <Link to="/admin/users" className="hover:text-gray-700">Users</Link>
        <span>/</span>
        <span className="text-gray-700">{detail?.username || userID || 'detail'}</span>
      </div>
      <PageHeader title="User Detail" />

      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {message && <div className="mb-4"><SuccessState message={message} /></div>}
      {loading && <LoadingState message="Loading user detail..." />}

      {!loading && !error && detail && (
        <div className="space-y-4">
          <Card>
            <CardHeader title="Profile" />
            <CardBody>
              <div className="grid gap-x-8 gap-y-1 sm:grid-cols-2 mb-4">
                <InfoRow label="Username" value={detail.username} />
                <InfoRow label="Auth Provider" value={<Badge color={detail.auth_provider === 'ldap' ? 'blue' : 'gray'}>{detail.auth_provider}</Badge>} />
                <InfoRow label="Status" value={
                  <Badge color={detail.is_active ? 'green' : 'red'}>
                    {detail.is_active ? 'Active' : 'Inactive'}
                  </Badge>
                } />
              </div>
              <div className="grid gap-4 sm:grid-cols-2 mb-4">
                <Input label="Email" value={editEmail} onChange={setEditEmail} placeholder="user@example.com" />
                <Input label="Display Name" value={editDisplayName} onChange={setEditDisplayName} placeholder="Jane Doe" />
              </div>
              <div className="flex gap-2">
                <Button disabled={savingProfile} onClick={() => void saveProfile()}>
                  {savingProfile ? 'Saving...' : 'Save Profile'}
                </Button>
                <Button
                  variant={detail.is_active ? 'danger' : 'primary'}
                  disabled={togglingActive}
                  onClick={() => void toggleActive()}
                >
                  {togglingActive ? 'Updating...' : detail.is_active ? 'Deactivate User' : 'Activate User'}
                </Button>
              </div>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Reset Password" />
            <CardBody>
              {detail.auth_provider === 'ldap' && (
                <p className="mb-3 text-sm text-amber-700">
                  This user is LDAP-managed. Local password reset may be ignored by LDAP authentication mode.
                </p>
              )}
              <div className="flex items-end gap-2">
                <div className="w-64">
                  <Input label="New Password" value={newPassword} onChange={setNewPassword} type="password" placeholder="min 8 characters" />
                </div>
                <Button disabled={savingPassword || detail.auth_provider === 'ldap'} onClick={() => void resetPassword()}>
                  {savingPassword ? 'Resetting...' : 'Reset Password'}
                </Button>
              </div>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Roles" />
            <CardBody>
              <div className="mb-4 flex items-end gap-2">
                <div className="w-48">
                  <Select
                    value={selectedRole}
                    onChange={setSelectedRole}
                    options={
                      assignableRoles.length === 0
                        ? [{ value: '', label: 'No available roles' }]
                        : assignableRoles.map((r) => ({ value: r.name, label: r.name }))
                    }
                  />
                </div>
                <Button size="sm" disabled={assignableRoles.length === 0} onClick={() => void addRole()}>
                  Add role
                </Button>
              </div>
              <div className="space-y-2">
                {detail.roles.map((roleName) => (
                  <div key={roleName} className="flex items-center justify-between rounded-lg border border-gray-200 px-4 py-2.5">
                    <div className="flex items-center gap-2">
                      <Badge color="indigo">{roleName}</Badge>
                    </div>
                    <Button size="sm" variant="danger" onClick={() => void removeRole(roleName)}>Remove</Button>
                  </div>
                ))}
                {detail.roles.length === 0 && (
                  <p className="py-4 text-center text-sm text-gray-400">No roles assigned.</p>
                )}
              </div>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Groups" />
            <CardBody>
              <div className="space-y-2">
                {detail.groups.map((group) => (
                  <div key={group.id} className="rounded-lg border border-gray-200 px-4 py-2.5">
                    <span className="text-sm font-medium text-gray-900">{group.name}</span>
                  </div>
                ))}
                {detail.groups.length === 0 && (
                  <p className="py-4 text-center text-sm text-gray-400">No groups assigned.</p>
                )}
              </div>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="User Grants" />
            <CardBody>
              <div className="mb-4 flex items-end gap-2">
                <div className="w-48">
                  <Select
                    label="Asset"
                    value={selectedAssetID}
                    onChange={setSelectedAssetID}
                    options={assets.map((a) => ({ value: a.id, label: a.name }))}
                  />
                </div>
                <div className="w-36">
                  <Select
                    label="Action"
                    value={selectedAction}
                    onChange={setSelectedAction}
                    options={SUPPORTED_ACTIONS}
                  />
                </div>
                <Button size="sm" disabled={assets.length === 0} onClick={() => void addGrant()}>
                  Add grant
                </Button>
              </div>
              <Table>
                <thead>
                  <tr>
                    <Th>Asset</Th>
                    <Th>Action</Th>
                    <Th>Effect</Th>
                    <Th>Remove</Th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {grants.map((grant) => (
                    <tr key={grant.asset_id + ':' + grant.action} className="hover:bg-gray-50">
                      <Td className="font-medium text-gray-900">{grant.asset_name}</Td>
                      <Td><Badge color="indigo">{grant.action}</Badge></Td>
                      <Td><Badge color={grant.effect === 'allow' ? 'green' : 'red'}>{grant.effect}</Badge></Td>
                      <Td>
                        <Button size="sm" variant="danger" onClick={() => void removeGrant(grant.asset_id, grant.action)}>
                          Remove
                        </Button>
                      </Td>
                    </tr>
                  ))}
                  {grants.length === 0 && <EmptyRow colSpan={4} message="No direct user grants." />}
                </tbody>
              </Table>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Effective Access" />
            <Table>
              <thead>
                <tr>
                  <Th>Asset</Th>
                  <Th>Actions</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {effective.map((item) => (
                  <tr key={item.asset_id} className="hover:bg-gray-50">
                    <Td className="font-medium text-gray-900">{item.asset_name}</Td>
                    <Td>
                      <div className="flex flex-wrap gap-1">
                        {item.actions.map((action) => (
                          <Badge key={action.action} color="indigo">
                            {action.action} ({action.sources.join(', ')})
                          </Badge>
                        ))}
                      </div>
                    </Td>
                  </tr>
                ))}
                {effective.length === 0 && <EmptyRow colSpan={2} message="No effective access found." />}
              </tbody>
            </Table>
          </Card>
        </div>
      )}
    </>
  )
}
