# apps/ui — PAM Frontend

React + TailAdmin UI for PAM. Built with Vite and TypeScript.

## Planned Pages

| Page | Path | Purpose |
|------|------|---------|
| Login | `/login` | LDAP username/password form |
| Dashboard | `/` | Overview: recent sessions, assigned asset count |
| My Assets | `/assets` | Access table with launch actions |
| Asset Detail | `/assets/:id` | Asset info, connection history |
| Sessions | `/sessions` | Session history with filters |
| Session Detail | `/sessions/:id` | Session replay (SSH text), metadata |
| Admin: Users | `/admin/users` | User/group/role management |
| Admin: Assets | `/admin/assets` | Asset CRUD, credential assignment |
| Admin: Policies | `/admin/policies` | Access assignment rules |
| Admin: Audit Log | `/admin/audit` | Full audit event log |

## Structure

```
src/
  pages/        Route-level page components
  components/   Shared UI components
  api/          API client module
  hooks/        React hooks
  types/        TypeScript types (from contracts)
```

## Development

```bash
# From repo root
make dev-ui

# Or directly
cd apps/ui && npm run dev
```

TailAdmin template integration is pending — will be set up during UI bootstrap phase.
