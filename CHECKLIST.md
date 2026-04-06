# PAM v1 — Build Checklist

## Current Status

**Phase**: Phase 2 in progress — backend foundation spine implemented (startup, DB wiring, migrations, health/version), business features pending.

**Last updated**: 2026-04-06

---

## Phase 1: Repo / Monorepo Foundation

- [x] Create monorepo directory structure (`apps/api`, `apps/ui`, `apps/connector`, `packages/contracts`)
- [x] Initialize Go module for `apps/api`
- [x] Initialize Go module for `apps/connector`
- [ ] Initialize React + Vite project in `apps/ui` with TailAdmin *(shell created, npm install + TailAdmin integration pending)*
- [x] Set up `Makefile` with targets: `build`, `dev`, `test`, `lint`, `migrate`
- [x] Create `docker-compose.yml` with PostgreSQL (LDAP service can be added later for provider integration tests)
- [x] Add `.gitignore` for Go, Node, IDE files
- [ ] Set up basic CI (lint + test) — GitHub Actions or equivalent

## Phase 2: Backend Bootstrap

- [x] Set up Go HTTP server with graceful shutdown (`apps/api/cmd/server`)
- [ ] Implement config loading (env vars + config file) *(env loading done; config file loading deferred)*
- [x] Set up PostgreSQL connection pool (`pgxpool`)
- [x] Set up in-repo SQL migration runner (`migrate up`, `migrate status`)
- [x] Create initial migration: `users`, `groups`, `user_groups`, `assets`, `asset_protocols`, `access_grants`, `credentials`, `sessions`, `session_events`, `audit_events`
- [x] Add auth/RBAC migration: `roles`, `user_roles`, `auth_sessions`, local auth columns on `users`
- [x] Implement structured logging (`slog`)
- [x] Implement request logging middleware
- [ ] Implement CORS middleware
- [x] Implement health check endpoints (`GET /health/live`, `GET /health/ready`)
- [x] Implement version endpoint (`GET /version`)
- [ ] Write first integration test (health check against real DB)
- [ ] Add `updated_at` trigger strategy (DB trigger vs app-managed writes)
- [ ] Add migration test coverage in CI (fresh DB + idempotency checks)

## Phase 3: UI Bootstrap

- [ ] Install TailAdmin template and integrate with Vite + React
- [ ] Set up React Router with auth-guarded routes
- [ ] Implement auth context (JWT storage, refresh, logout)
- [ ] Create layout shell (sidebar, header, content area)
- [ ] Create login page (form only — backend integration in Phase 5)
- [ ] Set up API client module (`apps/ui/src/api/`)
- [ ] Confirm dev proxy from Vite to Go backend works

## Phase 4: Shared Contracts

- [x] Define initial OpenAPI spec in `packages/contracts/api.yaml`
- [ ] Cover auth, users, assets, policies, sessions endpoints
- [x] Add health/version endpoints and foundational route tags (`auth`, `assets`, `access`, `sessions`, `audit`)
- [ ] Generate TypeScript types from OpenAPI (or maintain manually)
- [x] Document contract update workflow in README

## Phase 5: Authentication Providers (Dev First)

- [x] Implement local auth provider in `auth` module (development/POC mode)
- [x] Implement `POST /auth/login` endpoint
- [x] Implement `POST /auth/logout` endpoint
- [x] Implement secure HTTP-only cookie sessions (server-controlled)
- [x] Implement auth middleware for protected routes
- [x] Implement `GET /me` endpoint
- [x] Seed default development admin user
- [x] Keep authorization provider-agnostic (users/groups/roles/access grants unchanged by auth provider choice)
- [x] Add auth provider interface (`local` now, `ldap` later)
- [ ] Add LDAP provider integration task (deferred, same provider interface)
- [ ] Minimal LDAP attribute sync on login (username, email, display name, group membership) *(deferred with LDAP provider)*
- [ ] Wire login page to backend
- [ ] Handle auth errors in UI (invalid creds, provider unavailable, token expired)
- [ ] Write tests: successful login, bad password, logout, expired session, provider-switch compatibility

## Phase 6: User / Group / Role Model

- [ ] Implement user CRUD API (`GET/POST/PUT /users`, `GET /users/:id`)
- [ ] Implement group CRUD API
- [ ] Implement role assignment API (`POST /users/:id/roles`)
- [ ] Implement group membership API (`POST /groups/:id/members`)
- [x] Implement RBAC middleware foundation (role checks in backend, used by `/admin/ping`)
- [ ] Build admin users page (list, create, edit, assign roles/groups)
- [ ] Build admin groups page
- [ ] Write tests: CRUD operations, RBAC enforcement, group sync

## Phase 7: Asset Inventory

- [x] Create migration: `assets` table *(included in foundational migration `000001_core_v1.up.sql`)*
- [ ] Implement asset CRUD API (`GET/POST/PUT/DELETE /assets`)
- [ ] Asset types: `ssh`, `database`, `redis`, `sftp`
- [ ] Asset fields: name, type, host, port, engine (for DB), description, status
- [ ] Build admin assets page (list, create, edit, delete)
- [ ] Build "my assets" page (user-facing, filtered by policy — placeholder until policy is done)
- [ ] Write tests: CRUD, validation, type-specific fields

## Phase 8: Access Policy / Assignments

- [ ] Create migration: `access_policies` table
- [ ] Implement policy CRUD API
- [ ] Policy model: subject (user or group) → asset → allowed actions
- [ ] Implement policy evaluation function: `CanAccess(userID, assetID, action) → bool`
- [ ] Apply policy filter to `GET /assets/mine` endpoint
- [ ] Build admin policies page (assign users/groups to assets)
- [ ] Update "my assets" page to show only policy-allowed assets with permitted actions
- [ ] Write tests: policy creation, evaluation (direct user, group membership, no access, multiple policies)

## Phase 9: Credential Storage

- [x] Create migration: `credentials` table *(included in foundational migration `000001_core_v1.up.sql`)*
- [ ] Implement AES-256-GCM encryption/decryption in vault module
- [ ] Implement `PAM_VAULT_KEY` loading from environment
- [ ] Implement credential CRUD (admin-only, API never returns plaintext)
- [ ] Credential types: `ssh_password`, `ssh_keypair`, `db_password`, `redis_password`, `sftp_password`
- [ ] Build admin credential management in asset detail page
- [ ] Audit log credential access events
- [ ] Write tests: encrypt/decrypt roundtrip, key missing error, credential CRUD, no plaintext in API responses

## Phase 10: SSH Proxy / Shell Path

This is the critical path — highest-value v1 feature.

- [ ] Implement SSH server using `golang.org/x/crypto/ssh`
- [ ] Accept connections on configurable port (e.g., 2222)
- [ ] Authenticate incoming connections via keyboard-interactive auth (session token passed through challenge-response)
- [ ] Map session token → user + asset + policy
- [ ] Retrieve target SSH credential from vault
- [ ] Establish upstream SSH connection to target host
- [ ] Relay bidirectional I/O (PTY, shell, stdin/stdout/stderr)
- [ ] Record full session stream in asciicast v2 format
- [ ] Compress and store recording to filesystem
- [ ] Create session record in DB (status: active → completed/disconnected)
- [ ] Handle disconnection, timeout, and error cases gracefully
- [ ] Implement `POST /sessions/launch` for SSH assets
- [ ] Write connector SSH launch logic:
  - [ ] macOS/Linux: spawn `ssh` in native terminal pointed at PAM proxy
  - [ ] Windows: spawn PuTTY pointed at PAM proxy
- [ ] End-to-end test: login → select SSH asset → launch → connect → type commands → disconnect → verify recording

## Phase 11: File Transfer Path

- [ ] Implement SFTP relay server (using `pkg/sftp` or similar)
- [ ] Accept connections, authenticate via session token
- [ ] Connect upstream to target SFTP using stored credentials
- [ ] Relay SFTP operations
- [ ] Log file operations: upload, download, delete, rename (path, size, timestamp)
- [ ] Create session record for SFTP sessions
- [ ] Implement `POST /sessions/launch` for SFTP assets
- [ ] Connector: launch FileZilla (macOS/Linux) or WinSCP (Windows) pointed at PAM SFTP endpoint
- [ ] Test: upload file, download file, verify audit log entries

## Phase 12: DB Broker / DBeaver Path

- [ ] Implement TCP proxy for database connections
- [ ] Allocate ephemeral port per session (or multiplex on single port with session routing)
- [ ] Authenticate session, retrieve DB credential from vault
- [ ] Proxy TCP traffic to target database
- [ ] Log connection events: open, close, duration, database name
- [ ] Implement `POST /sessions/launch` for database assets
- [ ] Connector: launch DBeaver with connection parameters (host=pam, port=ephemeral, db=target_db)
- [ ] Connector creates temporary local DBeaver connection profile from launch payload
- [ ] Clean up temporary connection material after session ends
- [ ] Test: launch DBeaver, connect through PAM proxy, run query, verify audit log

## Phase 13: Redis Path

- [ ] Implement TCP proxy for Redis (RESP protocol)
- [ ] Authenticate via session token, connect upstream with stored Redis password
- [ ] Log connection events
- [ ] Implement `POST /sessions/launch` for Redis assets
- [ ] Connector: launch terminal with `redis-cli` pointed at PAM proxy
- [ ] Test: connect, run commands, verify audit log
- [ ] Parse RESP protocol to log individual commands (include only if simple within proxy design; otherwise defer)

## Phase 14: Audit + Session Storage

- [x] Create migration: `audit_events` table *(included in foundational migration `000001_core_v1.up.sql`)*
- [x] Create migration: `sessions` table *(included in foundational migration `000001_core_v1.up.sql`)*
- [ ] Implement audit event writer (called from all modules)
- [ ] Event types: `login`, `logout`, `session_start`, `session_end`, `policy_check`, `credential_access`, `file_operation`, `admin_action`
- [ ] Implement audit query API with filters (date range, user, event type, asset)
- [ ] Implement session list API with filters
- [ ] Implement session detail API (metadata + recording download)
- [ ] Ensure audit writes are non-blocking (don't slow down proxy traffic)
- [ ] Add indexes for query performance
- [ ] Write tests: audit event creation, query filters, session lifecycle

## Phase 15: Session Review UI

- [ ] Build session history page (table with filters: user, asset, date, type, status)
- [ ] Build session detail page with metadata panel
- [ ] Implement SSH session text replay (asciicast player — xterm.js + asciinema-player or custom)
- [ ] Display SFTP session file operation log
- [ ] Display DB/Redis session connection timeline
- [ ] Build admin audit log page with full event search
- [ ] Test replay with real recorded sessions

## Phase 16: Connector / Launcher

- [ ] Define connector ↔ API protocol (REST endpoints for launch instructions)
- [ ] Implement connector authentication (JWT-based, login flow)
- [ ] Implement connector config (PAM server URL, stored token)
- [ ] Implement client launcher abstraction (per OS, per asset type)
- [ ] macOS launcher: native terminal (SSH), FileZilla (SFTP), DBeaver (DB), terminal (Redis)
- [ ] Linux launcher: native terminal (SSH), FileZilla (SFTP), DBeaver (DB), terminal (Redis)
- [ ] Windows launcher: PuTTY (SSH), WinSCP (SFTP), DBeaver (DB), terminal (Redis)
- [ ] Implement protocol handler registration (optional — `pam://` URI scheme)
- [ ] Handle "client not installed" errors gracefully
- [ ] Build connector binary (cross-compile for darwin-amd64, darwin-arm64, linux-amd64, windows-amd64)
- [ ] Test on macOS, Linux, Windows

## Phase 17: Hardening

- [ ] Input validation on all API endpoints (reject malformed requests)
- [ ] Rate limiting on auth endpoints
- [ ] JWT secret/key configuration for production
- [ ] HTTPS/TLS configuration for API server
- [ ] SSH host key management for PAM's SSH proxy
- [ ] Enforce LDAPS or StartTLS for LDAP connections (when LDAP provider is enabled)
- [ ] Ensure no credentials leak in logs, error messages, or API responses
- [ ] Add request ID tracking across API and proxy layers
- [ ] Timeout configuration for proxy connections (idle timeout, max session duration)
- [ ] Graceful shutdown: drain active sessions before stopping
- [ ] Security review: OWASP top 10 check against API
- [ ] Dependency audit (`go mod tidy`, `npm audit`)

## Phase 18: Documentation

- [ ] README with project overview, architecture, and setup instructions
- [ ] Dev setup guide (docker-compose, env vars, first run)
- [ ] API documentation (from OpenAPI spec)
- [ ] Admin guide: managing users, assets, policies, credentials
- [ ] Connector installation guide (per OS)
- [ ] Deployment guide (systemd on Linux VM for production, Docker Compose for dev)

## Phase 19: v1 Release Readiness

- [ ] All v1 acceptance criteria pass (see PLAN.md section 16)
- [ ] End-to-end test: full workflow for each asset type
- [ ] Load test: concurrent SSH sessions (target: 50+ concurrent)
- [ ] Security test: attempt to extract credentials via API, connector, proxy
- [ ] Session recording verified: replay matches original session
- [ ] Audit log verified: all events captured for all workflows
- [ ] Deploy to staging environment
- [ ] Stakeholder walkthrough / demo
- [ ] Cut v1 release tag

---

## Resolved Decisions (locked for v1)

- [x] **Connector registration**: JWT-authenticated launch/session model only. No device registration or admin approval in v1.
- [x] **DB statement capture**: Deferred beyond v1. v1 guarantees brokered DB access + connection/session metadata audit only.
- [x] **SSH session token transport**: Keyboard-interactive auth method. Session token passed via challenge-response.
- [x] **DBeaver launch method**: Thin connector receives short-lived launch payload, creates temporary local connection profile/material, launches DBeaver, cleans up after session.
- [x] **Deployment target**: Production is systemd on Linux VM. Docker Compose for local dev/test only.
- [x] **Auth provider direction**: local auth provider first for development/POC; LDAP added later as an additional provider without changing authorization/session/policy internals.
- [x] **Redis command logging**: Included in v1 only if simple within proxy design. Otherwise connection/session audit only, deeper capture deferred.
- [x] **Credential encryption**: AES-256-GCM with single app master key from `PAM_VAULT_KEY` env var. Clearly documented as temporary v1 approach — must be replaced by external KMS/Vault post-v1.

## Blockers / Open Decisions

- [ ] **Default dev admin bootstrap**: final defaults for username/email/password and rotation expectation in team workflow.
- [ ] **LDAP schema details (deferred)**: what specific attributes/group structure does the target LDAP use? Need sample directory for integration.
- [ ] **DBeaver temp profile format**: need to prototype the exact mechanism for creating/cleaning temporary DBeaver connection profiles across platforms.

## Deferred Beyond v1

- RDP support
- Browser-based terminal (web shell)
- Video replay of sessions
- Approval / request workflows
- Secret rotation engine
- Multi-tenancy
- SAML / OIDC authentication
- Kubernetes-specific features
- High availability / clustering
- Object storage for session recordings (S3)
- Tamper-proof / signed audit logs
- Compliance framework automation
- Mobile UI
- Third-party API / webhook integrations
- Connector auto-update mechanism
- Connector device registration / admin approval
- DB statement-level capture (wire protocol parsing)
- External KMS / HashiCorp Vault key management
- Full LDAP sync engine (scheduled background sync, full attribute mapping)
- Behavioral analytics / anomaly detection
- Real-time session monitoring / admin kill-switch
