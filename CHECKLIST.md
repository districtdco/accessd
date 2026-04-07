# PAM v1 — Build Checklist

## Current Status

**Phase**: Phase 17 (hardening) in progress — production readiness consolidation pass.

**Last updated**: 2026-04-07

---

## Phase 1: Repo / Monorepo Foundation

- [x] Create monorepo directory structure (`apps/api`, `apps/ui`, `apps/connector`, `packages/contracts`)
- [x] Initialize Go module for `apps/api`
- [x] Initialize Go module for `apps/connector`
- [x] Initialize React + Vite project in `apps/ui` *(minimal React shell flow active; TailAdmin deferred)*
- [x] Set up `Makefile` with targets: `build`, `dev`, `test`, `lint`, `migrate`
- [x] Create `docker-compose.yml` with PostgreSQL (LDAP service can be added later for provider integration tests)
- [x] Add `.gitignore` for Go, Node, IDE files
- [x] Set up practical CI (GitHub Actions) for contract validation + API/connector Go test/build + UI install/build

## Phase 2: Backend Bootstrap

- [x] Set up Go HTTP server with graceful shutdown (`apps/api/cmd/server`)
- [x] Implement config loading (env vars + config file) *(added optional `PAM_CONFIG_FILE` loader with env-first precedence; file values only backfill missing env vars)*
- [x] Set up PostgreSQL connection pool (`pgxpool`)
- [x] Set up in-repo SQL migration runner (`migrate up`, `migrate status`)
- [x] Create initial migration: `users`, `groups`, `user_groups`, `assets`, `asset_protocols`, `access_grants`, `credentials`, `sessions`, `session_events`, `audit_events`
- [x] Add auth/RBAC migration: `roles`, `user_roles`, `auth_sessions`, local auth columns on `users`
- [x] Implement structured logging (`slog`)
- [x] Implement request logging middleware
- [x] Implement CORS middleware *(API CORS middleware added with allowlist via `PAM_CORS_ALLOWED_ORIGINS`, credential-aware origin echo, and preflight handling)*
- [x] Implement health check endpoints (`GET /health/live`, `GET /health/ready`)
- [x] Implement version endpoint (`GET /version`)
- [x] Write first backend integration test slice against real DB handlers/services *(expanded beyond health: login/logout/me, RBAC authz checks, `/access/my`, launch happy/denied, sessions list filters, and export authz)*
- [ ] Add `updated_at` trigger strategy (DB trigger vs app-managed writes)
- [ ] Add migration test coverage in CI (fresh DB + idempotency checks)

## Phase 3: UI Bootstrap

- [ ] Install TailAdmin template and integrate with Vite + React
- [x] Set up React Router with auth-guarded routes
- [x] Implement auth context (cookie-session aware `/me` fetch + logout)
- [x] Create layout shell (sidebar, header, content area) *(role-aware sidebar/header layout active; detail routes now include breadcrumb navigation)*
- [x] Create login page (form + backend local auth integration)
- [x] Set up API client module (`apps/ui/src/api.ts`)
- [x] Confirm dev proxy from Vite to Go backend works

## Phase 4: Shared Contracts

- [x] Define initial OpenAPI spec in `packages/contracts/api.yaml`
- [ ] Cover auth, users, assets, policies, sessions endpoints *(expanded with admin RBAC helper endpoints for users/roles/groups/assets/grants/effective access; broader CRUD still pending)*
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
- [x] Add LDAP provider integration task (same provider interface; mode-configurable `local`/`ldap`/`hybrid`)
- [x] Minimal LDAP attribute sync on login (username, email, display name; group-to-role mapping optional/additive)
- [x] Refine LDAP defaults/diagnostics for Samba AD (`sAMAccountName`, AD-friendly user/group filters, clearer bind/search vs invalid-password vs connectivity logs)
- [x] Support additive LDAP group-to-role mapping by group name (`cn`) or full group DN
- [x] Wire login page to backend
- [x] Handle auth errors in UI (minimal error display for login/access/launch failures)
- [ ] Write tests: successful login, bad password, logout, expired session, provider-switch compatibility *(partial reliability slice now added: login/logout/me + admin authorization checks, plus LDAP config/mapping/fallback unit tests)*

## Phase 6: User / Group / Role Model

- [ ] Implement user CRUD API (`GET/POST/PUT /users`, `GET /users/:id`)
- [ ] Implement group CRUD API
- [x] Implement role assignment API *(admin helper endpoints: `GET /admin/roles`, `POST /admin/users/{id}/roles`, `DELETE /admin/users/{id}/roles/{role}`)*
- [ ] Implement group membership API (`POST /groups/:id/members`)
- [x] Implement RBAC middleware foundation (role checks in backend, used by `/admin/ping`)
- [x] Build admin users page *(minimal: list users, view roles, open detail, role/grant controls in detail view)*
- [ ] Build admin groups page *(deferred; API visibility added via `GET /admin/groups`, `GET /admin/groups/{id}/members`, `GET /admin/groups/{id}/grants`)*
- [ ] Write tests: CRUD operations, RBAC enforcement, group sync

## Phase 7: Asset Inventory

- [x] Create migration: `assets` table *(included in foundational migration `000001_core_v1.up.sql`)*
- [ ] Implement asset CRUD API (`GET/POST/PUT/DELETE /assets`)
- [x] Asset types: `linux_vm`, `database`, `redis` *(service-level model implemented for this slice)*
- [x] Asset fields: id, name, type, host, port, metadata_json, created_at *(service + migration `000003_assets_credentials_access_v1.up.sql`)*
- [x] Build admin assets page *(minimal helper: list assets and inspect grants only; create/edit/delete deferred)*
- [x] Build "my assets" page (user-facing table via `GET /access/my`, shell action only in this slice)
- [ ] Write tests: CRUD, validation, type-specific fields

## Phase 8: Access Policy / Assignments

- [ ] Create migration: `access_policies` table
- [ ] Implement policy CRUD API
- [x] Policy model: subject (user or group) → asset → allowed actions *(implemented via `access_grants` usage layer)*
- [x] Implement policy evaluation function: `CanAccess(userID, assetID, action) → bool`
- [x] Apply policy filter endpoint: `GET /access/my` *(replaces planned `GET /assets/mine` in this slice)*
- [x] Build admin policies helper view *(user-focused direct allow-grant management on user detail; group policy editing deferred)*
- [ ] Update "my assets" page to show only policy-allowed assets with permitted actions
- [ ] Write tests: policy creation, evaluation (direct user, group membership, no access, multiple policies)

## Phase 9: Credential Storage

- [x] Create migration: `credentials` table *(included in foundational migration `000001_core_v1.up.sql`)*
- [x] Implement AES-256-GCM encryption/decryption in vault module
- [x] Implement `PAM_VAULT_KEY` loading from environment
- [x] Implement credential create/resolve service (backend-only decrypted use; no plaintext API exposure)
- [x] Credential types (this slice): `password`, `ssh_key`, `db_password`
- [x] Build admin credential management in asset detail page *(minimal helper: write-only credential update + safe metadata visibility, no plaintext secret readback)*
- [x] Audit log credential access events *(credential usage now emitted for launch preparation + proxy upstream auth stages across SSH/DB/Redis flows)*
- [ ] Write tests: encrypt/decrypt roundtrip, key missing error, credential CRUD, no plaintext in API responses

## Phase 10: SSH Proxy / Shell Path

This is the critical path — highest-value v1 feature.

- [x] Implement SSH server using `golang.org/x/crypto/ssh`
- [x] Accept connections on configurable port (e.g., 2222)
- [x] Authenticate incoming connections via keyboard-interactive auth (session token passed through challenge-response) *(password auth fallback also supported in first pass)*
- [x] Map session token → user + asset + policy *(policy checked at launch creation; token + session binding verified on proxy auth)*
- [x] Retrieve target SSH credential from vault
- [x] Establish upstream SSH connection to target host
- [x] Relay bidirectional I/O (PTY, shell, stdin/stdout/stderr)
- [ ] Record full session stream in asciicast v2 format
- [ ] Compress and store recording to filesystem
- [x] Create session record in DB (status: active → completed/disconnected)
- [x] Handle disconnection, timeout, and error cases gracefully
- [x] Implement `POST /sessions/launch` for SSH assets
- [x] Write connector SSH launch logic:
  - [x] macOS/Linux: spawn `ssh` in native terminal pointed at PAM proxy
  - [x] Windows: spawn PuTTY pointed at PAM proxy *(manual token entry first pass)*
- [ ] End-to-end test: login → select SSH asset → launch → connect → type commands → disconnect → verify recording

## Phase 11: File Transfer Path

- [ ] Implement SFTP relay server (using `pkg/sftp` or similar)
- [ ] Accept connections, authenticate via session token
- [ ] Connect upstream to target SFTP using stored credentials
- [ ] Relay SFTP operations
- [ ] Log file operations: upload, download, delete, rename (path, size, timestamp)
- [x] Create session record for SFTP sessions (managed launch lifecycle via `sessions` + connector events)
- [x] Implement `POST /sessions/launch` for SFTP assets
- [x] Connector: launch FileZilla (macOS/Linux) or WinSCP (Windows) in managed connector flow
- [ ] Test: upload file, download file, verify audit log entries

## Phase 12: DB Broker / DBeaver Path

- [ ] Implement TCP proxy for database connections
- [ ] Allocate ephemeral port per session (or multiplex on single port with session routing)
- [ ] Authenticate session, retrieve DB credential from vault
- [ ] Proxy TCP traffic to target database *(deferred in this slice)*
- [x] Log connection/session lifecycle launch events for connector handoff (`launch_created`, `connector_launch_requested`, `connector_launch_succeeded`/`connector_launch_failed`, `session_ended`/`session_failed`)
- [x] Implement `POST /sessions/launch` support for database assets (`action=dbeaver`)
- [x] Connector: launch DBeaver with launch payload parameters (`/launch/dbeaver`)
- [x] Connector creates temporary local DBeaver launch material from payload (temp manifest in OS temp dir)
- [x] Clean up temporary connection material after session ends *(TTL-based auto cleanup + startup stale temp cleanup in connector)*
- [ ] Test: launch DBeaver, connect through PAM DB proxy, run query, verify audit log *(DB proxy/query audit deferred)*

## Phase 13: Redis Path

- [ ] Implement TCP proxy for Redis (RESP protocol)
- [ ] Authenticate via session token, connect upstream with stored Redis password
- [x] Log connector/session lifecycle metadata events for Redis managed launch path (`launch_created`, `connector_launch_requested`, `connector_launch_succeeded`/`connector_launch_failed`, `session_ended`/`session_failed`)
- [x] Implement `POST /sessions/launch` for Redis assets
- [x] Connector: launch terminal with `redis-cli` in managed connector flow
- [ ] Test: connect, run commands, verify audit log
- [ ] Parse RESP protocol to log individual commands (include only if simple within proxy design; otherwise defer)

## Phase 14: Audit + Session Storage

- [x] Create migration: `audit_events` table *(included in foundational migration `000001_core_v1.up.sql`)*
- [x] Create migration: `sessions` table *(included in foundational migration `000001_core_v1.up.sql`)*
- [ ] Implement audit event writer (called from all modules)
- [ ] Event types: `login`, `logout`, `session_start`, `session_end`, `policy_check`, `credential_access`, `file_operation`, `admin_action`
- [x] Implement audit query API with practical filters *(added `GET /admin/audit/events` with `event_type`, `user_id`, `asset_id`, `session_id`, `action`, `from`, `to`, `limit`; plus `GET /admin/audit/events/{id}` detail)*
- [x] Implement session list API with filters *(added `GET /sessions/my` and `GET /admin/sessions` with practical status/action/asset/date/user filters + limit)*
- [x] Implement session detail API *(added `GET /sessions/{id}` + `GET /sessions/{id}/events`; recording download still deferred)*
- [ ] Ensure audit writes are non-blocking (don't slow down proxy traffic)
- [x] Add indexes for query performance *(added `000005_session_audit_perf_v1.up.sql` composite indexes for session event paging/filtering and audit/sessions query paths)*
- [ ] Write tests: audit event creation, query filters, session lifecycle *(partial reliability slice now added: launch/list/export authorization paths)*

## Phase 15: Session Review UI

- [x] Build session history page (table with practical filters and list browsing for user/admin slices; session detail/replay deferred)
- [x] Build session detail page with metadata panel
- [x] Implement first-pass SSH session text replay *(approximate event-based replay from `data_in`/`data_out`; terminal-perfect emulation deferred)*
- [x] Add DBeaver session detail metadata review *(connector launch lifecycle + final outcome, no transcript emulation)*
- [x] Add practical session recap/export helpers *(summary JSON, shell transcript TXT, admin sessions CSV)*
- [x] Display SFTP session file operation log *(session detail now renders paged `file_operation` timeline with operation/path/path_to/size + destructive markers)*
- [x] Display DB/Redis session connection timeline *(session detail now renders paged `db_query` and `redis_command` replay/timeline sections with search/filter and protocol/danger badges)*
- [x] Build admin audit log page (practical filters + detail drilldown; heavier SIEM-style analytics/export deferred)
- [x] Build lightweight admin recap/dashboard view *(added `/admin/dashboard` with summary cards, sessions-by-action, active sessions list, and recent audit activity feed from new admin summary endpoints)*
- [ ] Test replay with real recorded sessions

## Phase 16: Connector / Launcher

- [x] Define connector launch protocol for this slice (`POST /launch/shell` request shape documented and implemented)
- [ ] Implement connector authentication (JWT-based, login flow)
- [ ] Implement connector config (PAM server URL, stored token)
- [ ] Implement client launcher abstraction (per OS, per asset type)
- [ ] macOS launcher: native terminal (SSH), FileZilla (SFTP), DBeaver (DB), terminal (Redis) *(DBeaver launch added in first pass)*
- [ ] Linux launcher: native terminal (SSH), FileZilla (SFTP), DBeaver (DB), terminal (Redis) *(DBeaver launch added in first pass)*
- [ ] Windows launcher: PuTTY (SSH), WinSCP (SFTP), DBeaver (DB), terminal (Redis) *(DBeaver launch added in first pass)*
- [ ] Implement protocol handler registration (optional — `pam://` URI scheme)
- [ ] Handle "client not installed" errors gracefully
- [ ] Build connector binary (cross-compile for darwin-amd64, darwin-arm64, linux-amd64, windows-amd64)
- [ ] Test on macOS, Linux, Windows

## Phase 17: Hardening

- [ ] Input validation on all API endpoints (reject malformed requests)
- [x] Rate limiting on auth endpoints *(in-memory sliding window rate limiter: 10 attempts / 5min per username, with cleanup goroutine)*
- [ ] JWT secret/key configuration for production
- [ ] HTTPS/TLS configuration for API server *(app-level TLS listener intentionally not implemented in this slice; deploy docs now require external reverse proxy/LB TLS termination in front of API/connector routes)*
- [x] SSH host key management for PAM's SSH proxy *(added persistent proxy host key file + upstream host-key modes: `known-hosts` (default), `accept-new`, `insecure`; unsafe modes gated outside development)*
- [x] Enforce explicit LDAP transport mode controls (`PAM_LDAP_USE_TLS` or `PAM_LDAP_STARTTLS`; mutually exclusive)
- [x] Harden session cookie defaults and controls (`PAM_AUTH_COOKIE_SECURE` env-aware default, `PAM_AUTH_COOKIE_SAMESITE`, production validation guards)
- [x] Restrict unsafe mode use outside development (`PAM_ALLOW_UNSAFE_MODE` escape hatch required for insecure LDAP TLS, weak vault-key format, or permissive SSH host-key modes)
- [x] Tighten connector local trust boundary (loopback-only callers by default + explicit unsafe overrides for remote access and wildcard CORS)
- [x] Add audit events for admin credential updates (`event_type=admin_action`, `action=credential_upsert`)
- [x] Add login audit events (`login_success`, `login_failed`, `login_failed_invalid_password`, `login_failed_user_not_found`, `login_failed_ldap_error`, `login_failed_rate_limited`)
- [x] Connector trust model: HMAC-signed connector tokens (`PAM_CONNECTOR_SECRET`); connector verifies signature, session_id, and expiry before launching
- [x] Samba AD-aligned LDAP defaults (`sAMAccountName`, `displayName`, `mail`, simplified user search filter)
- [x] Structured login failure diagnostics (differentiate user_not_found, invalid_password, bind/config, TLS/connectivity in logs and audit)
- [x] Hybrid auth mode clarity (explicit fallback logging with failure reason classification)
- [x] Startup configuration summary log (provider mode, session settings, connector trust, unsafe mode status)
- [ ] Ensure no credentials leak in logs, error messages, or API responses *(targeted review completed for launch/session/audit/connector diagnostics; full repo-wide formal security review still pending)*
- [x] Add request ID tracking across API and proxy layers *(request id now propagated through API middleware, launch/proxy registration, and session/audit event payload metadata)*
- [x] Timeout configuration for proxy connections (idle timeout, max session duration) *(SSH/DB/Redis proxies enforce configurable idle and max session duration guards)*
- [x] Graceful shutdown: drain active sessions before stopping *(proxy services close listeners, wait for active workers, and force-close only on shutdown context timeout)*
- [ ] Security review: OWASP top 10 check against API
- [ ] Dependency audit (`go mod tidy`, `npm audit`)

## Phase 18: Documentation

- [ ] README with project overview, architecture, and setup instructions
- [ ] Dev setup guide (docker-compose, env vars, first run)
- [ ] API documentation (from OpenAPI spec)
- [ ] Admin guide: managing users, assets, policies, credentials
- [ ] Connector installation guide (per OS)
- [x] Deployment guide (systemd on Linux VM for production, Docker Compose for dev) *(added deploy assets + step-by-step `deploy/README.md`)*

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
- [x] **DB statement capture scope**: lightweight query-event capture (`db_query`) is included in this slice; full protocol-perfect/result-aware reconstruction remains deferred.
- [x] **SSH session token transport**: Keyboard-interactive auth method. Session token passed via challenge-response.
- [x] **DBeaver launch method**: Thin connector receives short-lived launch payload, creates temporary local connection profile/material, launches DBeaver, cleans up after session.
- [x] **Deployment target**: Production is systemd on Linux VM. Docker Compose for local dev/test only.
- [x] **Auth provider direction**: local auth provider first for development/POC; LDAP added later as an additional provider without changing authorization/session/policy internals.
- [x] **Redis command logging**: Included in v1 only if simple within proxy design. Otherwise connection/session audit only, deeper capture deferred.
- [x] **Credential encryption**: AES-256-GCM with single app master key from `PAM_VAULT_KEY` env var. Clearly documented as temporary v1 approach — must be replaced by external KMS/Vault post-v1.

## Blockers / Open Decisions

- [ ] **Default dev admin bootstrap**: final defaults for username/email/password and rotation expectation in team workflow.
- [ ] **LDAP schema details (deferred)**: what specific attributes/group structure does the target LDAP use? Need sample directory for integration.
- [x] **DBeaver temp profile cleanup**: connector now auto-cleans temp launch material by TTL and removes stale temp dirs on startup.
- [ ] **MSSQL client/proxy TLS tunnel support**: current MSSQL proxy does not support full client<->proxy TLS tunnel mode in this slice; rollout must use documented limitation/workaround.
- [ ] **Redis client-leg TLS mode**: connector/redis-cli currently connects to PAM Redis proxy over plaintext loopback/session endpoint; rollout docs must treat this as current limitation.

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
- Full protocol-perfect statement reconstruction with result/error attribution across all DB engines
- External KMS / HashiCorp Vault key management
- Full LDAP sync engine (scheduled background sync, full attribute mapping)
- Behavioral analytics / anomaly detection
- Real-time session monitoring / admin kill-switch
