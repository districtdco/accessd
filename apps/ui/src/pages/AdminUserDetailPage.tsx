import { useEffect, useMemo, useState } from "react"
import { Link, useParams } from "react-router-dom"
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
} from "../api"
import { useAuth } from "../auth"
import type {
  AdminAsset,
  AdminEffectiveAccessItem,
  AdminGrant,
  AdminRole,
  AdminUserDetail,
} from "../types"

const SUPPORTED_ACTIONS: Array<"shell" | "sftp" | "dbeaver" | "redis"> = [
  "shell",
  "sftp",
  "dbeaver",
  "redis",
]

export function AdminUserDetailPage() {
  const { user, logout } = useAuth()
  const params = useParams<{ userID: string }>()
  const userID = params.userID || ""

  const [detail, setDetail] = useState<AdminUserDetail | null>(null)
  const [roles, setRoles] = useState<AdminRole[]>([])
  const [assets, setAssets] = useState<AdminAsset[]>([])
  const [grants, setGrants] = useState<AdminGrant[]>([])
  const [effective, setEffective] = useState<AdminEffectiveAccessItem[]>([])

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)

  const [selectedRole, setSelectedRole] = useState<string>("")
  const [selectedAssetID, setSelectedAssetID] = useState<string>("")
  const [selectedAction, setSelectedAction] = useState<string>("shell")

  const loadData = async () => {
    if (userID === "") {
      setError("missing user id")
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
      setRoles(rolesResp.items)
      setAssets(assetsResp.items)
      setGrants(grantsResp.items)
      setEffective(effectiveResp.items)

      if (assetsResp.items.length > 0) {
        setSelectedAssetID((prev) => {
          if (prev !== "") {
            return prev
          }
          return assetsResp.items[0].id
        })
      }

      const availableRole = rolesResp.items.find((role) => detailResp.roles.includes(role.name) === false)
      if (availableRole) {
        setSelectedRole(availableRole.name)
      } else {
        setSelectedRole("")
      }
    } catch (err) {
      const messageText = err instanceof Error ? err.message : "failed to load user detail"
      setError(messageText)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void loadData()
    // userID is the route key for this page.
  }, [userID])

  const assignableRoles = useMemo(() => {
    if (detail === null) {
      return [] as AdminRole[]
    }
    return roles.filter((role) => detail.roles.includes(role.name) === false)
  }, [detail, roles])

  const addRole = async () => {
    if (selectedRole === "") {
      return
    }
    setMessage(null)
    try {
      await adminAssignRole(userID, selectedRole)
      setMessage("role assigned")
      await loadData()
    } catch (err) {
      const messageText = err instanceof Error ? err.message : "failed to assign role"
      setError(messageText)
    }
  }

  const removeRole = async (roleName: string) => {
    setMessage(null)
    try {
      await adminRemoveRole(userID, roleName)
      setMessage("role removed")
      await loadData()
    } catch (err) {
      const messageText = err instanceof Error ? err.message : "failed to remove role"
      setError(messageText)
    }
  }

  const addGrant = async () => {
    if (selectedAssetID === "" || selectedAction === "") {
      return
    }
    setMessage(null)
    try {
      await adminAddUserGrant(userID, selectedAssetID, selectedAction)
      setMessage("grant added")
      await loadData()
    } catch (err) {
      const messageText = err instanceof Error ? err.message : "failed to add grant"
      setError(messageText)
    }
  }

  const removeGrant = async (assetID: string, action: string) => {
    setMessage(null)
    try {
      await adminRemoveUserGrant(userID, assetID, action)
      setMessage("grant removed")
      await loadData()
    } catch (err) {
      const messageText = err instanceof Error ? err.message : "failed to remove grant"
      setError(messageText)
    }
  }

  return (
    <main className="page-shell">
      <header className="topbar">
        <div>
          <h1>Admin · User Detail</h1>
          <p className="muted">
            Signed in as <strong>{user?.username}</strong>
          </p>
        </div>
        <div className="actions-inline">
          <Link to="/">My Access</Link>
          <Link to="/admin/dashboard">Dashboard</Link>
          <Link to="/sessions">My Sessions</Link>
          <Link to="/admin/users">Users</Link>
          <Link to="/admin/assets">Assets</Link>
          <Link to="/admin/sessions">Sessions</Link>
          <button onClick={() => void logout()}>Logout</button>
        </div>
      </header>

      {loading ? <p>Loading user detail...</p> : null}
      {error === null ? null : <p className="error">{error}</p>}
      {message === null ? null : <p className="status">{message}</p>}

      {loading === false && error === null && detail !== null ? (
        <>
          <section className="section-block card">
            <h2>User</h2>
            <p>
              <strong>Username:</strong> {detail.username}
            </p>
            <p>
              <strong>Email:</strong> {detail.email || "-"}
            </p>
            <p>
              <strong>Display Name:</strong> {detail.display_name || "-"}
            </p>
            <p>
              <strong>Status:</strong> {detail.is_active ? "active" : "inactive"}
            </p>
          </section>

          <section className="section-block card">
            <h2>Roles</h2>
            <div className="actions-inline">
              <select value={selectedRole} onChange={(e) => setSelectedRole(e.target.value)}>
                {assignableRoles.length === 0 ? <option value="">No available roles</option> : null}
                {assignableRoles.map((role) => (
                  <option key={role.id} value={role.name}>
                    {role.name}
                  </option>
                ))}
              </select>
              <button onClick={() => void addRole()} disabled={assignableRoles.length === 0}>
                Add role
              </button>
            </div>
            <ul className="simple-list">
              {detail.roles.map((roleName) => (
                <li key={roleName}>
                  <span>{roleName}</span>
                  <button onClick={() => void removeRole(roleName)}>Remove</button>
                </li>
              ))}
              {detail.roles.length === 0 ? <li className="muted">No roles assigned.</li> : null}
            </ul>
          </section>

          <section className="section-block card">
            <h2>Groups</h2>
            <ul className="simple-list">
              {detail.groups.map((group) => (
                <li key={group.id}>
                  <span>{group.name}</span>
                </li>
              ))}
              {detail.groups.length === 0 ? <li className="muted">No groups assigned.</li> : null}
            </ul>
          </section>

          <section className="section-block card">
            <h2>User Grants</h2>
            <div className="actions-inline">
              <select value={selectedAssetID} onChange={(e) => setSelectedAssetID(e.target.value)}>
                {assets.map((asset) => (
                  <option key={asset.id} value={asset.id}>
                    {asset.name}
                  </option>
                ))}
              </select>
              <select value={selectedAction} onChange={(e) => setSelectedAction(e.target.value)}>
                {SUPPORTED_ACTIONS.map((action) => (
                  <option key={action} value={action}>
                    {action}
                  </option>
                ))}
              </select>
              <button onClick={() => void addGrant()} disabled={assets.length === 0}>
                Add grant
              </button>
            </div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Asset</th>
                    <th>Action</th>
                    <th>Effect</th>
                    <th>Remove</th>
                  </tr>
                </thead>
                <tbody>
                  {grants.map((grant) => (
                    <tr key={grant.asset_id + ":" + grant.action}>
                      <td>{grant.asset_name}</td>
                      <td>{grant.action}</td>
                      <td>{grant.effect}</td>
                      <td>
                        <button onClick={() => void removeGrant(grant.asset_id, grant.action)}>Remove</button>
                      </td>
                    </tr>
                  ))}
                  {grants.length === 0 ? (
                    <tr>
                      <td colSpan={4} className="muted">
                        No direct user grants.
                      </td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </section>

          <section className="section-block card">
            <h2>Effective Access</h2>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Asset</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {effective.map((item) => (
                    <tr key={item.asset_id}>
                      <td>{item.asset_name}</td>
                      <td>
                        {item.actions
                          .map((action) => action.action + " (" + action.sources.join(", ") + ")")
                          .join("; ")}
                      </td>
                    </tr>
                  ))}
                  {effective.length === 0 ? (
                    <tr>
                      <td colSpan={2} className="muted">
                        No effective access found.
                      </td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </section>
        </>
      ) : null}
    </main>
  )
}
