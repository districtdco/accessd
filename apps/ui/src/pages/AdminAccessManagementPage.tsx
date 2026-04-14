import { useEffect, useMemo, useRef, useState } from 'react'
import {
  adminAddUserGrant,
  adminGetUserEffectiveAccess,
  adminListAssets,
  adminListUserGrants,
  adminListUsers,
  adminRemoveUserGrant,
} from '../api'
import type { AdminAsset, AdminEffectiveAccessItem, AdminGrant, AdminUser } from '../types'
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  Checkbox,
  EmptyRow,
  ErrorState,
  LoadingState,
  PageHeader,
  PaginationControls,
  SuccessState,
  Table,
  TabNav,
  Td,
  Th,
} from '../components/ui'

const actionCatalog = ['shell', 'sftp', 'dbeaver', 'redis'] as const

function isActionAllowedForAssetType(action: string, assetType: string): boolean {
  if (assetType === 'linux_vm') {
    return action === 'shell' || action === 'sftp'
  }
  if (assetType === 'database') {
    return action === 'dbeaver'
  }
  if (assetType === 'redis') {
    return action === 'redis'
  }
  return false
}

export function AdminAccessManagementPage() {
  const [tab, setTab] = useState<'direct' | 'effective'>('direct')
  const [users, setUsers] = useState<AdminUser[]>([])
  const [assets, setAssets] = useState<AdminAsset[]>([])
  const [selectedUserID, setSelectedUserID] = useState('')
  const [userSearch, setUserSearch] = useState('')
  const [userPickerOpen, setUserPickerOpen] = useState(false)
  const [assetSearch, setAssetSearch] = useState('')
  const [grants, setGrants] = useState<AdminGrant[]>([])
  const [effective, setEffective] = useState<AdminEffectiveAccessItem[]>([])
  const [selectedAssetIDs, setSelectedAssetIDs] = useState<string[]>([])
  const [selectedActions, setSelectedActions] = useState<string[]>(['shell'])
  const [loading, setLoading] = useState(true)
  const [detailsLoading, setDetailsLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [directPage, setDirectPage] = useState(1)
  const [effectivePage, setEffectivePage] = useState(1)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)
  const pageSize = 10
  const userPickerRef = useRef<HTMLDivElement | null>(null)

  const loadLists = async () => {
    setLoading(true)
    setError(null)
    try {
      const [usersResp, assetsResp] = await Promise.all([adminListUsers(), adminListAssets()])
      setUsers(usersResp.items)
      setAssets(assetsResp.items)
      setSelectedUserID((prev) => (usersResp.items.some((item) => item.id === prev) ? prev : ''))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load admin data')
    } finally {
      setLoading(false)
    }
  }

  const loadUserAccess = async (userID: string) => {
    if (!userID) return
    setDetailsLoading(true)
    setError(null)
    try {
      const [grantsResp, effectiveResp] = await Promise.all([
        adminListUserGrants(userID),
        adminGetUserEffectiveAccess(userID),
      ])
      setGrants(grantsResp.items)
      setEffective(effectiveResp.items)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load user access')
    } finally {
      setDetailsLoading(false)
    }
  }

  useEffect(() => {
    void loadLists()
  }, [])

  useEffect(() => {
    if (selectedUserID) {
      void loadUserAccess(selectedUserID)
    }
  }, [selectedUserID])

  useEffect(() => {
    if (!userPickerOpen) return
    const handleClickOutside = (event: MouseEvent) => {
      if (userPickerRef.current && !userPickerRef.current.contains(event.target as Node)) {
        setUserPickerOpen(false)
      }
    }
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setUserPickerOpen(false)
      }
    }
    window.addEventListener('mousedown', handleClickOutside)
    window.addEventListener('keydown', handleEscape)
    return () => {
      window.removeEventListener('mousedown', handleClickOutside)
      window.removeEventListener('keydown', handleEscape)
    }
  }, [userPickerOpen])

  useEffect(() => {
    setDirectPage(1)
    setEffectivePage(1)
  }, [selectedUserID, grants.length, effective.length])

  const filteredUsers = useMemo(() => {
    const q = userSearch.trim().toLowerCase()
    if (!q) return users
    return users.filter((item) => {
      return item.username.toLowerCase().includes(q)
        || (item.display_name || '').toLowerCase().includes(q)
        || (item.email || '').toLowerCase().includes(q)
    })
  }, [userSearch, users])

  const selectedUser = useMemo(() => users.find((item) => item.id === selectedUserID) || null, [users, selectedUserID])

  const selectedUserLabel = selectedUser
    ? `${selectedUser.username}${selectedUser.is_active ? '' : ' (inactive)'}`
    : 'Select a user'

  const filteredAssets = useMemo(() => {
    const q = assetSearch.trim().toLowerCase()
    const rows = assets.filter((item) => {
      if (!q) return true
      return `${item.name} ${item.host} ${item.asset_type}`.toLowerCase().includes(q)
    })
    return rows.sort((a, b) => a.name.localeCompare(b.name))
  }, [assets, assetSearch])

  const selectedAssets = useMemo(() => {
    if (selectedAssetIDs.length === 0) {
      return [] as AdminAsset[]
    }
    const idSet = new Set(selectedAssetIDs)
    return assets.filter((asset) => idSet.has(asset.id))
  }, [assets, selectedAssetIDs])

  const actionCompatibility = useMemo(() => {
    return actionCatalog.map((action) => {
      const supportedCount = selectedAssets.filter((asset) => isActionAllowedForAssetType(action, asset.asset_type)).length
      return {
        action,
        supportedCount,
        totalSelected: selectedAssets.length,
        selectable: selectedAssets.length === 0 ? true : supportedCount > 0,
      }
    })
  }, [selectedAssets])

  useEffect(() => {
    if (selectedAssets.length === 0) {
      return
    }
    setSelectedActions((prev) => {
      const next = prev.filter((action) => selectedAssets.some((asset) => isActionAllowedForAssetType(action, asset.asset_type)))
      return next
    })
  }, [selectedAssets])

  const directTotalPages = Math.max(1, Math.ceil(grants.length / pageSize))
  const directCurrentPage = Math.min(directPage, directTotalPages)
  const pagedGrants = useMemo(() => grants.slice((directCurrentPage - 1) * pageSize, directCurrentPage * pageSize), [grants, directCurrentPage])
  const effectiveTotalPages = Math.max(1, Math.ceil(effective.length / pageSize))
  const effectiveCurrentPage = Math.min(effectivePage, effectiveTotalPages)
  const pagedEffective = useMemo(() => effective.slice((effectiveCurrentPage - 1) * pageSize, effectiveCurrentPage * pageSize), [effective, effectiveCurrentPage])

  const isAssetChecked = (assetID: string) => selectedAssetIDs.includes(assetID)

  const toggleAsset = (assetID: string) => {
    setSelectedAssetIDs((prev) => {
      if (prev.includes(assetID)) {
        return prev.filter((id) => id !== assetID)
      }
      return [...prev, assetID]
    })
  }

  const toggleAction = (action: string) => {
    setSelectedActions((prev) => {
      if (prev.includes(action)) {
        const next = prev.filter((value) => value !== action)
        return next.length > 0 ? next : prev
      }
      return [...prev, action]
    })
  }

  const selectVisibleAssets = () => {
    setSelectedAssetIDs((prev) => {
      const set = new Set(prev)
      filteredAssets.forEach((asset) => set.add(asset.id))
      return [...set]
    })
  }

  const clearAssetSelection = () => {
    setSelectedAssetIDs([])
  }

  const applyGrants = async () => {
    if (!selectedUserID || selectedAssetIDs.length === 0 || selectedActions.length === 0) {
      setError('Select a user, at least one server, and at least one action')
      return
    }
    setSaving(true)
    setError(null)
    setMessage(null)

    const operations: Promise<void>[] = []
    for (const assetID of selectedAssetIDs) {
      const asset = assets.find((item) => item.id === assetID)
      if (!asset) {
        continue
      }
      for (const action of selectedActions) {
        if (!isActionAllowedForAssetType(action, asset.asset_type)) {
          continue
        }
        operations.push(adminAddUserGrant(selectedUserID, assetID, action))
      }
    }
    if (operations.length === 0) {
      setSaving(false)
      setError('Selected actions are not compatible with selected server types')
      return
    }

    const results = await Promise.allSettled(operations)
    const successCount = results.filter((item) => item.status === 'fulfilled').length
    const failCount = results.length - successCount
    if (failCount === 0) {
      setMessage(`Applied ${successCount} grant${successCount === 1 ? '' : 's'} successfully`)
    } else {
      setError(`Applied ${successCount} grants, ${failCount} failed`)
    }

    await loadUserAccess(selectedUserID)
    setSaving(false)
  }

  const removeGrant = async (assetID: string, action: string) => {
    if (!selectedUser) return
    if (!window.confirm(`Remove ${action} access on this asset for ${selectedUser.username}?`)) return
    setSaving(true)
    setError(null)
    setMessage(null)
    try {
      await adminRemoveUserGrant(selectedUserID, assetID, action)
      setMessage('Access grant removed')
      await loadUserAccess(selectedUserID)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to remove grant')
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <PageHeader title="Access Management" />

      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {message && <div className="mb-4"><SuccessState message={message} /></div>}

      {loading ? (
        <LoadingState message="Loading users and assets..." />
      ) : (
        <div className="space-y-4">
          <Card>
            <CardHeader title="Grant Builder">
              <span className="text-xs text-gray-500">Apply access to one user across multiple servers in one action.</span>
            </CardHeader>
            <CardBody>
              <div className="grid gap-4 md:grid-cols-3">
                <div ref={userPickerRef} className="relative md:col-span-2">
                  <span className="mb-1 block text-sm font-medium text-gray-700">User</span>
                  <button
                    type="button"
                    onClick={() => setUserPickerOpen((prev) => !prev)}
                    className="flex w-full items-center justify-between rounded-lg border border-gray-300 px-3 py-2 text-left text-sm text-gray-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                  >
                    <span className="truncate">{selectedUserLabel}</span>
                    <span className="ml-2 text-gray-400" aria-hidden>▾</span>
                  </button>
                  {userPickerOpen && (
                    <div className="absolute z-20 mt-2 w-full rounded-lg border border-gray-200 bg-white p-2 shadow-lg">
                      <input
                        type="text"
                        autoFocus
                        value={userSearch}
                        onChange={(e) => setUserSearch(e.target.value)}
                        placeholder="Search username, email, display name"
                        className="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 placeholder-gray-400 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                      />
                      <div className="mt-2 max-h-52 overflow-auto rounded-lg border border-gray-100">
                        {filteredUsers.length > 0 ? (
                          <ul className="py-1">
                            {filteredUsers.map((item) => {
                              const isSelected = item.id === selectedUserID
                              return (
                                <li key={item.id}>
                                  <button
                                    type="button"
                                    onClick={() => {
                                      setSelectedUserID(item.id)
                                      setUserPickerOpen(false)
                                    }}
                                    className={`flex w-full items-center justify-between px-3 py-2 text-left text-sm ${
                                      isSelected ? 'bg-indigo-50 text-indigo-700' : 'text-gray-700 hover:bg-gray-50'
                                    }`}
                                  >
                                    <span className="truncate">
                                      {item.username}
                                      {!item.is_active ? ' (inactive)' : ''}
                                    </span>
                                    <span className="ml-2 text-xs text-gray-500">{item.auth_provider}</span>
                                  </button>
                                </li>
                              )
                            })}
                          </ul>
                        ) : (
                          <p className="px-3 py-3 text-sm text-gray-500">No matching users</p>
                        )}
                      </div>
                    </div>
                  )}
                </div>
                <div className="rounded-lg border border-gray-200 bg-gray-50 px-3 py-2">
                  <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">Selected User</p>
                  <p className="mt-1 text-sm font-medium text-gray-900">{selectedUser?.username || '-'}</p>
                  <div className="mt-1 flex items-center gap-2">
                    <Badge color={selectedUser?.auth_provider === 'ldap' ? 'blue' : 'gray'}>{selectedUser?.auth_provider || '-'}</Badge>
                    <Badge color={selectedUser?.is_active ? 'green' : 'red'}>{selectedUser?.is_active ? 'active' : 'inactive'}</Badge>
                  </div>
                </div>
              </div>

              <div className="mt-6 grid gap-4 lg:grid-cols-[2fr_1fr]">
                <div className="rounded-lg border border-gray-200">
                  <div className="flex flex-wrap items-center justify-between gap-2 border-b border-gray-200 px-3 py-2">
                    <p className="text-sm font-semibold text-gray-800">Servers</p>
                    <div className="flex gap-2">
                      <Button size="sm" variant="secondary" onClick={selectVisibleAssets}>Select Visible</Button>
                      <Button size="sm" variant="ghost" onClick={clearAssetSelection}>Clear</Button>
                    </div>
                  </div>
                  <div className="p-3">
                    <label className="block">
                      <span className="mb-1 block text-sm font-medium text-gray-700">Search servers</span>
                      <input
                        type="text"
                        value={assetSearch}
                        onChange={(e) => setAssetSearch(e.target.value)}
                        placeholder="name, host, type"
                        className="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 placeholder-gray-400 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                      />
                    </label>
                    <div className="mt-3 max-h-56 overflow-auto rounded-lg border border-gray-100">
                      <table className="w-full text-sm">
                        <thead>
                          <tr className="bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-500">
                            <th className="px-3 py-2">Select</th>
                            <th className="px-3 py-2">Server</th>
                            <th className="px-3 py-2">Type</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y divide-gray-100">
                          {filteredAssets.map((asset) => (
                            <tr key={asset.id} className="hover:bg-gray-50">
                              <td className="px-3 py-2">
                                <input
                                  type="checkbox"
                                  checked={isAssetChecked(asset.id)}
                                  onChange={() => toggleAsset(asset.id)}
                                  className="h-4 w-4 rounded border-gray-300 text-indigo-600 focus:ring-indigo-500"
                                />
                              </td>
                              <td className="px-3 py-2">
                                <div className="font-medium text-gray-900">{asset.name}</div>
                                <div className="text-xs text-gray-500">{asset.endpoint}</div>
                              </td>
                              <td className="px-3 py-2"><Badge>{asset.asset_type}</Badge></td>
                            </tr>
                          ))}
                          {filteredAssets.length === 0 && (
                            <tr>
                              <td className="px-3 py-4 text-center text-sm text-gray-400" colSpan={3}>No servers found.</td>
                            </tr>
                          )}
                        </tbody>
                      </table>
                    </div>
                  </div>
                </div>

                <div className="rounded-lg border border-gray-200 p-3">
                  <p className="text-sm font-semibold text-gray-800">Allowed Actions</p>
                  <div className="mt-3 space-y-2">
                    {actionCompatibility.map((item) => (
                      <Checkbox
                        key={item.action}
                        label={item.action}
                        checked={selectedActions.includes(item.action)}
                        disabled={!item.selectable}
                        onChange={() => toggleAction(item.action)}
                        hint={item.totalSelected > 0 ? `${item.supportedCount}/${item.totalSelected} selected servers support this action` : 'Select servers to scope compatible actions'}
                      />
                    ))}
                  </div>
                  <div className="mt-4 rounded-lg bg-gray-50 p-3 text-xs text-gray-600">
                    <p>{selectedAssetIDs.length} server(s) selected</p>
                    <p>{selectedActions.length} action(s) selected</p>
                    <p className="mt-1">{selectedAssetIDs.length * selectedActions.length} grant operations will be applied.</p>
                  </div>
                  <div className="mt-4">
                    <Button disabled={saving || !selectedUserID} onClick={() => void applyGrants()}>
                      {saving ? 'Applying...' : 'Apply Grants'}
                    </Button>
                  </div>
                </div>
              </div>
            </CardBody>
          </Card>

          <TabNav
            tabs={[
              { id: 'direct', label: 'Direct Grants' },
              { id: 'effective', label: 'Effective Access' },
            ]}
            active={tab}
            onChange={(id) => setTab(id as 'direct' | 'effective')}
          />

          {detailsLoading ? (
            <LoadingState message="Loading user access..." />
          ) : tab === 'direct' ? (
            <Card>
              <Table>
                <thead>
                  <tr>
                    <Th>Asset</Th>
                    <Th>Action</Th>
                    <Th>Effect</Th>
                    <Th>Created</Th>
                    <Th>Actions</Th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {pagedGrants.map((grant) => (
                    <tr key={`${grant.asset_id}:${grant.action}`} className="hover:bg-gray-50">
                      <Td className="font-medium text-gray-900">{grant.asset_name}</Td>
                      <Td><Badge color="indigo">{grant.action}</Badge></Td>
                      <Td><Badge color={grant.effect === 'allow' ? 'green' : 'red'}>{grant.effect}</Badge></Td>
                      <Td>{new Date(grant.created_at).toLocaleString()}</Td>
                      <Td>
                        <Button size="sm" variant="danger" onClick={() => void removeGrant(grant.asset_id, grant.action)}>Remove</Button>
                      </Td>
                    </tr>
                  ))}
                  {pagedGrants.length === 0 && <EmptyRow colSpan={5} message="No direct grants assigned to this user." />}
                </tbody>
              </Table>
              <PaginationControls
                page={directCurrentPage}
                totalPages={directTotalPages}
                totalItems={grants.length}
                pageSize={pageSize}
                onPageChange={setDirectPage}
              />
            </Card>
          ) : (
            <Card>
              <Table>
                <thead>
                  <tr>
                    <Th>Asset</Th>
                    <Th>Allowed Actions</Th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {pagedEffective.map((item) => (
                    <tr key={item.asset_id} className="hover:bg-gray-50">
                      <Td className="font-medium text-gray-900">{item.asset_name}</Td>
                      <Td>
                        <div className="flex flex-wrap gap-1">
                          {item.actions.map((action) => (
                            <Badge key={`${item.asset_id}:${action.action}`} color="indigo">
                              {action.action} via {action.sources.join(', ')}
                            </Badge>
                          ))}
                        </div>
                      </Td>
                    </tr>
                  ))}
                  {pagedEffective.length === 0 && <EmptyRow colSpan={2} message="No effective access for this user." />}
                </tbody>
              </Table>
              <PaginationControls
                page={effectiveCurrentPage}
                totalPages={effectiveTotalPages}
                totalItems={effective.length}
                pageSize={pageSize}
                onPageChange={setEffectivePage}
              />
            </Card>
          )}
        </div>
      )}
    </>
  )
}
