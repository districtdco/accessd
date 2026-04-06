import { useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { adminListUsers } from "../api"
import { useAuth } from "../auth"
import type { AdminUser } from "../types"

export function AdminUsersPage() {
  const { user, logout } = useAuth()
  const [items, setItems] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      setLoading(true)
      setError(null)
      try {
        const response = await adminListUsers()
        if (cancelled === false) {
          setItems(response.items)
        }
      } catch (err) {
        if (cancelled === false) {
          const message = err instanceof Error ? err.message : "failed to load users"
          setError(message)
        }
      } finally {
        if (cancelled === false) {
          setLoading(false)
        }
      }
    }

    void load()
    return () => {
      cancelled = true
    }
  }, [])

  return (
    <main className="page-shell">
      <header className="topbar">
        <div>
          <h1>Admin · Users</h1>
          <p className="muted">
            Signed in as <strong>{user?.username}</strong>
          </p>
        </div>
        <div className="actions-inline">
          <Link to="/">My Access</Link>
          <Link to="/admin/dashboard">Dashboard</Link>
          <Link to="/sessions">My Sessions</Link>
          <Link to="/admin/assets">Assets</Link>
          <Link to="/admin/sessions">Sessions</Link>
          <button onClick={() => void logout()}>Logout</button>
        </div>
      </header>

      {loading ? <p>Loading users...</p> : null}
      {error === null ? null : <p className="error">{error}</p>}

      {loading === false && error === null ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Username</th>
                <th>Email</th>
                <th>Roles</th>
                <th>Status</th>
                <th>Detail</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.id}>
                  <td>{item.username}</td>
                  <td>{item.email || "-"}</td>
                  <td>{item.roles.join(", ") || "-"}</td>
                  <td>{item.is_active ? "active" : "inactive"}</td>
                  <td>
                    <Link to={"/admin/users/" + item.id}>Open</Link>
                  </td>
                </tr>
              ))}
              {items.length === 0 ? (
                <tr>
                  <td colSpan={5} className="muted">
                    No users found.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      ) : null}
    </main>
  )
}
