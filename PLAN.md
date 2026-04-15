# PAM System — v1 Plan

> Status note (2026-04-15): this file is a historical planning document and no longer reflects the live implementation details in several areas (naming, auth/session model, and route surface). For current behavior, use `README.md`, `apps/api/README.md`, `apps/ui/README.md`, `LOCAL_TESTING.md`, `CHECKLIST.md`, and `REMAINING_BLOCKERS.md`.

## 1. Executive Summary

We are building a lean Privileged Access Management (PAM) system that centralizes access to infrastructure assets (Linux servers, databases, Redis, file transfer endpoints). Users authenticate into PAM, which brokers all connections — users never receive raw credentials. Every session is audited. The system ships as a modular monolith: Go backend, React+TailAdmin UI, and a desktop connector/launcher.

Development mode (current): local auth provider first, with LDAP deferred but planned as an additional provider. Authorization, session handling, policy enforcement, and audit remain provider-agnostic from day one.

## 2. Product Scope

### In scope (v1)
- Local authentication provider for development/POC
- Local user/group/role model
- Asset inventory (servers, databases, Redis instances, file transfer endpoints)
- Access policy: assign assets to users/groups
- Central credential vault (encrypted at rest in PostgreSQL)
- Access table UI — user sees their assigned assets with launch actions
- SSH gateway/proxy with full session recording (keystroke + output)
- SFTP/file transfer managed path with file-operation logging
- DB access brokered through PAM (DBeaver enforced), connection-level audit guaranteed
- Redis managed shell with connection-level audit (command logging included only if simple within proxy design)
- Audit trail: who, when, what asset, session duration, activity
- Session history and review UI
- Desktop connector/launcher for client enforcement

### Development mode overrides (effective now)
- LDAP integration is deferred during foundation and early feature slices.
- Auth is implemented as a provider model: `local` first, `ldap` later.
- Authorization is unchanged and fully enforced server-side:
  - users, groups, roles, access grants
  - policy checks
  - session token lifecycle
  - audit event model
- Seed a default development admin user on first bootstrap/seed path.
- LDAP onboarding later must plug into the same auth-provider interface without rewriting authorization, policy, or session/audit logic.

### Out of scope (v1)
See section 3.

## 3. Explicit Non-Goals

- RDP / Windows remote desktop
- Browser-based terminal / web shell
- Video replay of sessions
- Approval workflows / request-based access
- Secret rotation engine
- Multi-tenancy
- Microservices architecture
- Kubernetes-specific control plane
- SAML / OIDC federation
- Compliance framework automation (SOX, PCI checklists, etc.)
- High-availability / clustering (single-instance deployment for v1)
- DB statement-level capture (connection/session audit only in v1)
- Connector device registration / admin approval
- Full LDAP sync engine
- External KMS / HashiCorp Vault integration (v1 uses app-level master key)
- Mobile UI
- API for third-party integrations

## 4. Monorepo Structure

```
pam/
├── apps/
│   ├── api/              # Go backend (HTTP API + SSH proxy + DB broker + audit)
│   │   ├── cmd/          # Entry points (server, migrate, seed)
│   │   ├── internal/
│   │   │   ├── auth/     # Auth providers (local first, LDAP later), session/token management
│   │   │   ├── user/     # User/group/role domain
│   │   │   ├── asset/    # Asset inventory domain
│   │   │   ├── policy/   # Access assignments, policy evaluation
│   │   │   ├── vault/    # Credential storage and retrieval
│   │   │   ├── proxy/    # SSH proxy, DB broker, Redis proxy, SFTP relay
│   │   │   ├── audit/    # Audit log writes, session recording storage
│   │   │   ├── api/      # HTTP handlers, middleware, routes
│   │   │   └── db/       # PostgreSQL queries, migrations
│   │   ├── migrations/
│   │   └── go.mod
│   ├── ui/               # React + TailAdmin
│   │   ├── src/
│   │   │   ├── pages/    # Login, Dashboard, Assets, Sessions, Admin
│   │   │   ├── components/
│   │   │   ├── api/      # API client (generated or hand-written from contracts)
│   │   │   ├── hooks/
│   │   │   └── types/    # TypeScript types (from contracts)
│   │   ├── package.json
│   │   └── vite.config.ts
│   └── connector/        # Desktop launcher/helper (Go binary)
│       ├── cmd/
│       ├── internal/
│       │   ├── launch/   # Client launcher (PuTTY, terminal, DBeaver, FileZilla)
│       │   ├── config/   # Local config, API endpoint discovery
│       │   └── auth/     # Token forwarding to API
│       └── go.mod
├── packages/
│   └── contracts/        # Shared API contracts
│       ├── api.yaml      # OpenAPI spec (or similar)
│       └── types/        # Shared type definitions
├── scripts/              # Dev tooling, docker-compose, etc.
├── deploy/               # Systemd unit files, production config templates
├── docker-compose.yml    # PostgreSQL (+ optional LDAP for later integration tests)
├── Makefile
├── PLAN.md
├── CHECKLIST.md
└── README.md
```

Go modules: `apps/api` and `apps/connector` are separate Go modules. They share contracts via the OpenAPI spec / generated types, not via Go imports.

## 5. High-Level Architecture

```
┌─────────────┐       ┌──────────────────────────────────────────┐
│   Browser    │◄─────►│  PAM API  (Go, single binary)            │
│  React UI   │  HTTP  │                                          │
└─────────────┘       │  ┌──────────┐ ┌────────┐ ┌───────────┐  │
                      │  │ Auth     │ │ Policy │ │ Vault     │  │
┌─────────────┐       │  │Provider  │ │ Engine │ │ (creds)   │  │
│  Connector  │◄─────►│  └──────────┘ └────────┘ └───────────┘  │
│  (desktop)  │  HTTP  │                                          │
└─────────────┘       │  ┌──────────────────────────────────────┐│
                      │  │  Proxy Layer                         ││
                      │  │  ┌─────┐ ┌──────┐ ┌─────┐ ┌──────┐ ││
                      │  │  │ SSH │ │ DB   │ │SFTP │ │Redis │ ││
                      │  │  │Proxy│ │Broker│ │Relay│ │Proxy │ ││
                      │  │  └──┬──┘ └──┬───┘ └──┬──┘ └──┬───┘ ││
                      │  └─────┼───────┼────────┼───────┼──────┘│
                      │        │       │        │       │       │
                      │  ┌─────┴───────┴────────┴───────┴──────┐│
                      │  │  Audit Engine (writes all events)   ││
                      │  └─────────────────────────────────────┘│
                      │                                          │
                      │  ┌──────────────┐                       │
                      │  │  PostgreSQL   │                       │
                      │  └──────────────┘                       │
                      └──────────────────────────────────────────┘
                              │       │        │       │
                      ┌───────┴───┐ ┌─┴──────┐ │  ┌────┴────┐
                      │ Linux VMs │ │  DBs   │ │  │  Redis  │
                      │ (SSH)     │ │(PG/My) │ │  │instances│
                      └───────────┘ └────────┘ │  └─────────┘
                                          ┌────┴────┐
                                          │  SFTP   │
                                          │endpoints│
                                          └─────────┘
```

Key architectural decisions:
- **Single Go binary** serves HTTP API + SSH proxy + DB broker + SFTP relay + Redis proxy on different ports.
- **Connector** is a thin desktop binary. It authenticates to PAM API, receives launch instructions, and spawns the correct local client with injected credentials. It does NOT hold credentials persistently.
- **All proxy traffic routes through PAM** — this is the enforcement point, not the UI or connector.
- **PostgreSQL is the single data store** — config, audit, session recordings (v1). Session recordings stored as compressed blobs or files referenced from PG.

## 6. Backend Module Breakdown

### `auth`
- Provider-oriented authentication (`local` in dev mode, `ldap` later)
- JWT token issuance (short-lived access + refresh)
- Session management
- Middleware for route protection
- Default seeded admin user for development/POC bootstrap
- LDAP provider later: minimal attribute sync on login (username, email, display name, group membership). Not a full sync engine.

### `user`
- CRUD for users, groups, roles
- Group membership
- Role definitions: `admin`, `operator`, `viewer` (v1 roles)
- Provider metadata bookkeeping (including future LDAP sync fields)

### `asset`
- Asset CRUD (servers, databases, Redis, file-transfer endpoints)
- Asset types: `ssh`, `database`, `redis`, `sftp`
- Asset metadata: host, port, engine type (for DB), description, tags
- Asset health/status (basic ping, optional in v1)

### `policy`
- Access assignment rules: user/group → asset(s) with allowed actions
- Policy evaluation: given a user + asset, return allowed/denied + permitted actions
- Server-side enforcement — every proxy connection checks policy before proceeding

### `vault`
- Credential storage: encrypted at rest using AES-256-GCM
- **v1 key management (temporary)**: single application master key loaded from `ACCESSD_VAULT_KEY` environment variable. This is explicitly a temporary v1 approach — documented as requiring migration to external KMS or HashiCorp Vault-backed key management in hardening/post-v1.
- Credential types: SSH key pair, SSH password, DB username/password, Redis password, SFTP credentials
- Credential retrieval: internal only, never exposed to API consumers

### `proxy`
**SSH proxy** (highest-fidelity recording path in v1):
- Implements an SSH server that clients connect to
- On connection: authenticate user via **keyboard-interactive** SSH auth method (locked for v1). The session token is passed through the keyboard-interactive challenge-response. This avoids abusing the username field and is well-supported by PuTTY, OpenSSH, and other clients.
- Establishes upstream SSH connection to target using stored credential
- Relays traffic bidirectionally
- Records full session (stdin/stdout as asciicast-compatible stream)
- Logs connect, disconnect, duration

**DB broker:**
- Receives launch request from connector
- Checks policy, retrieves DB credential from vault
- Returns a short-lived launch payload to the connector
- Connector creates temporary local connection profile/material for DBeaver, launches DBeaver pointed at PAM's DB proxy port
- Temporary connection material is cleaned up after session ends
- PAM proxies the DB connection to the real target
- Logs: connection open/close, database name, user, duration
- Statement-level capture: **deferred beyond v1**. v1 guarantees connection/session metadata audit only.

**SFTP relay:**
- PAM runs an SFTP server (or proxies SFTP protocol)
- Connector launches FileZilla/WinSCP pointed at PAM's SFTP endpoint
- PAM relays to target, logs file operations (upload, download, delete, rename with paths)

**Redis proxy:**
- TCP proxy that authenticates via PAM, connects to target Redis using stored credential
- v1 guarantees connection/session-level audit (connect, disconnect, duration)
- RESP command-level logging is included in v1 only if it remains simple within the chosen proxy design; otherwise deferred to post-v1

### `audit`
- Structured audit log writes to PostgreSQL
- Event types: login, logout, session_start, session_end, policy_check, credential_access, file_operation, admin_action
- Session recording storage: compressed session data in filesystem, referenced from PG
- Query interface for session history

### `api`
- RESTful HTTP API
- Routes grouped: `/auth/*`, `/users/*`, `/assets/*`, `/policies/*`, `/sessions/*`, `/admin/*`
- JSON request/response
- Middleware: auth, RBAC, request logging, CORS

### `db`
- SQL migrations (golang-migrate or similar)
- Query layer (sqlc or hand-written)
- Connection pooling

## 7. UI Module / Page Breakdown

### Pages
| Page | Path | Purpose |
|------|------|---------|
| Login | `/login` | Username/password form (provider-backed; local in dev mode) |
| Dashboard | `/` | Overview: recent sessions, assigned asset count, alerts |
| My Assets | `/assets` | Table of assets assigned to current user with launch actions |
| Asset Detail | `/assets/:id` | Asset info, connection history for that asset |
| Sessions | `/sessions` | Session history table with filters (user, asset, date range) |
| Session Detail | `/sessions/:id` | Session replay (SSH text replay), metadata, timeline |
| Admin: Users | `/admin/users` | User list, group assignment, role management |
| Admin: Assets | `/admin/assets` | Asset CRUD, credential assignment |
| Admin: Policies | `/admin/policies` | Access assignment rules |
| Admin: Audit Log | `/admin/audit` | Full audit event log with search/filter |

### Key UI components
- **Access table**: sortable/filterable table showing asset name, type, host, allowed actions, launch button
- **Launch button**: triggers connector protocol handler or direct proxy connection
- **Session player**: text-based replay of SSH sessions (asciicast/xterm.js based)
- **Policy editor**: assign users/groups to assets with action scoping

## 8. Connector Responsibilities

The connector is a lightweight desktop binary (Go, cross-compiled for Windows/macOS/Linux).

**What it does:**
1. Authenticates to PAM API (stores JWT locally, refreshes as needed)
2. Receives launch instructions from API (which client to open, what proxy endpoint to connect to)
3. Launches the approved client application:
   - SSH: PuTTY (Windows) or native terminal with `ssh` (macOS/Linux) pointed at PAM's SSH proxy
   - DB: DBeaver pointed at PAM's DB proxy port with connection parameters
   - SFTP: FileZilla (macOS/Linux) or WinSCP (Windows) pointed at PAM's SFTP endpoint
   - Redis: Terminal with `redis-cli` pointed at PAM's Redis proxy
4. Passes credentials ephemerally (env vars, temp config files deleted after launch, or protocol-level injection)
5. Reports launch status back to API

**What it does NOT do:**
- Store credentials persistently
- Make policy decisions
- Bypass the proxy layer
- Provide a shell or terminal of its own

**Registration flow (v1 — locked):**
- No device registration or admin approval in v1
- Connector authenticates using the user's JWT (same token as UI login)
- Connector calls `POST /sessions/launch` with JWT to receive launch payload
- This is a pure JWT-authenticated launch/session model — no device identity layer yet

## 9. Access Flow Design

### 9.1 Login Flow
```
User → UI login form → POST /auth/login {username, password}
  → API authenticates via active provider (dev mode: local provider)
  → On success: load user/groups/roles from local DB
  → Issue JWT (access token: 15min, refresh token: 8h)
  → Return tokens to UI
  → UI stores tokens, redirects to dashboard
```

### 9.2 Asset Listing Flow
```
User → UI loads /assets → GET /assets/mine (with JWT)
  → API evaluates policy: which assets is this user assigned to?
  → Return asset list with allowed actions per asset
  → UI renders access table
```

### 9.3 Launch Flow (generic)
```
User clicks "Connect" on an asset in UI
  → UI calls POST /sessions/launch {asset_id, action}
  → API checks policy (server-side, authoritative)
  → API retrieves target credential from vault
  → API prepares proxy endpoint (if not already listening)
  → API creates session record (status: pending)
  → API returns launch instructions {proxy_host, proxy_port, session_token, client_hint}
  → UI passes launch instructions to connector (via localhost API or protocol handler)
  → Connector launches approved client pointed at proxy endpoint with session token
```

### 9.4 Proxied SSH Shell Flow
```
Connector launches: ssh -p <pam_ssh_port> user@<pam_host>
  → PAM SSH proxy accepts connection
  → Prompts for session token via keyboard-interactive auth
  → Connector (or user) provides session token through challenge-response
  → Validates session token, maps to user + asset + policy
  → Retrieves target SSH credential from vault
  → Opens upstream SSH connection to target host
  → Relays bidirectional traffic
  → Records full I/O stream (stdin + stdout + stderr)
  → On disconnect: finalizes session record, compresses recording
```

This is the **highest-fidelity recording path** in v1. PAM is a full man-in-the-middle for SSH, so we capture everything the user types and everything the server returns.

### 9.5 DB Launch / Brokering Flow
```
Connector receives short-lived launch payload for DB asset
  → Connector creates temporary local DBeaver connection profile
      (host=pam_host, port=proxy_port, db=target_db, session_token as auth)
  → Connector launches DBeaver pointed at this temporary profile
  → DBeaver connects to PAM's DB proxy port
  → PAM proxy authenticates via session token, connects upstream to real DB
  → Relays TCP traffic
  → Logs: connection open/close, duration, database name
  → On session end: connector cleans up temporary connection material
  → Statement-level capture: deferred beyond v1
```

### 9.6 File Transfer Flow
```
Connector launches FileZilla/WinSCP pointed at PAM's SFTP endpoint
  → PAM SFTP relay accepts connection, authenticates via session token
  → Connects upstream to target SFTP server using stored credentials
  → Relays SFTP operations
  → Logs: file uploads, downloads, deletes, renames (with full paths and sizes)
  → On disconnect: finalizes session record
```

### 9.7 Redis Flow
```
Connector launches terminal with: redis-cli -h <pam_host> -p <pam_redis_port> -a <session_token>
  → PAM Redis proxy accepts connection
  → Authenticates session token, connects upstream to target Redis using stored credential
  → Relays RESP protocol traffic
  → If protocol parsing implemented: logs individual commands
  → Otherwise: logs connection-level events (connect, disconnect, bytes)
```

## 10. Session Recording and Audit Strategy

### Guaranteed in v1
| Capability | Coverage |
|-----------|----------|
| Login/logout events | All users, all sessions |
| Session start/end | All connection types |
| Session duration | All connection types |
| User ↔ asset mapping | All sessions |
| SSH full I/O recording | Complete keystroke + output capture |
| SFTP file operation log | File paths, operations, sizes |
| DB connection audit | Connect/disconnect, database, duration |
| Redis connection audit | Connect/disconnect, duration |
| Admin action audit | All CRUD operations on users, assets, policies |

### Partial / conditional in v1
| Capability | Notes |
|-----------|-------|
| Redis command logging | RESP protocol is simple to parse. Included in v1 only if it remains straightforward within the chosen proxy design. Otherwise connection-level audit only. |

### Deferred beyond v1
| Capability | Notes |
|-----------|-------|
| DB statement capture (any engine) | Wire protocol parsing (PG, MySQL) is non-trivial and out of v1 scope. v1 guarantees brokered access + connection/session metadata audit only. |
| DB result capture | Very expensive in bandwidth/storage. Deferred. |

### Not promised in v1
- Full video-style replay of DB or file transfer sessions
- Behavioral analytics or anomaly detection
- Real-time alerting on suspicious commands
- Tamper-proof audit log (append-only / signed — desirable but deferred)

### Recording storage
- SSH recordings: stored as asciicast v2 files (JSON lines with timestamps), compressed with gzip, saved to local filesystem, referenced from `sessions` table in PG.
- Audit events: rows in `audit_events` table in PG.
- v1 storage is local disk + PG. Object storage (S3) deferred.

## 11. Credential Management Strategy

### Storage
- Credentials stored in `credentials` table in PostgreSQL
- Encrypted at rest using AES-256-GCM
- Encryption key: loaded from environment variable or config file at startup
- Each credential row: `{id, asset_id, credential_type, encrypted_blob, created_at, updated_at}`
- Credential types: `ssh_password`, `ssh_keypair`, `db_password`, `redis_password`, `sftp_password`

### Access
- Credentials are **never** returned via any API endpoint
- Only the proxy layer retrieves credentials internally to establish upstream connections
- Vault module provides: `GetCredential(assetID) → plaintext` (internal only)
- All credential retrievals are audit-logged

### Lifecycle (v1)
- Admin creates/updates credentials via Admin UI → API → vault
- No automatic rotation in v1 (manual rotation only)
- Credential deletion is soft-delete with audit trail

### v1 key management — TEMPORARY APPROACH
> **This is explicitly a temporary v1 design.** It must be replaced by external KMS or HashiCorp Vault-backed key management in hardening or post-v1.

- Single application master key loaded from `ACCESSD_VAULT_KEY` env var
- Key rotation: re-encrypt all credentials with new key (manual process in v1)
- The env var must be protected by OS-level access controls (file permissions, systemd `LoadCredential`, etc.)
- Migration path: post-v1, integrate with HashiCorp Vault or cloud KMS (AWS KMS, GCP KMS) for envelope encryption

## 12. PostgreSQL Data Model Outline

### Key tables

| Table | Purpose |
|-------|---------|
| `users` | Local user records (id, username, email, display_name, auth_provider, external_subject, status, created/updated) |
| `groups` | Groups (id, name, description, external_ref, created/updated) |
| `user_groups` | Many-to-many user ↔ group membership |
| `roles` | Role definitions (admin, operator, viewer) |
| `user_roles` | User ↔ role assignment |
| `assets` | Infrastructure assets (id, name, type, host, port, engine, description, status, created/updated) |
| `credentials` | Encrypted credentials per asset (id, asset_id, type, encrypted_blob, created/updated) |
| `access_policies` | Assignment rules: (id, subject_type[user/group], subject_id, asset_id, allowed_actions[], created/updated) |
| `sessions` | Session records (id, user_id, asset_id, type, status, started_at, ended_at, recording_path, metadata) |
| `audit_events` | Immutable audit log (id, timestamp, user_id, event_type, resource_type, resource_id, detail_json, ip_address) |

### Indexes
- `audit_events`: index on (timestamp), (user_id, timestamp), (event_type)
- `sessions`: index on (user_id, started_at), (asset_id, started_at)
- `access_policies`: index on (subject_type, subject_id), (asset_id)

### Notes
- `audit_events` should be append-only in application logic (no UPDATE/DELETE in code)
- `sessions.recording_path` points to filesystem location of recording file
- `credentials.encrypted_blob` contains JSON-structured credential data, encrypted as a single blob
- Consider partitioning `audit_events` by month if volume is high (v1: simple table, partition later)

## 13. Security Assumptions and Trust Boundaries

### Trust boundaries
1. **User ↔ PAM UI/API**: untrusted. All requests authenticated and authorized.
2. **Connector ↔ PAM API**: semi-trusted. Connector authenticates with user's JWT. API validates every request.
3. **PAM proxy ↔ target infrastructure**: trusted network path assumed in v1. (TLS to targets where supported.)
4. **PAM API ↔ PostgreSQL**: trusted. Same host or private network in v1.
5. **PAM API ↔ external auth provider**: trusted network path when enabled (e.g., LDAP over LDAPS/StartTLS).

### Security assumptions
- PAM server is deployed in a secured network segment
- PostgreSQL is not publicly accessible
- The `ACCESSD_VAULT_KEY` environment variable is protected by OS-level access controls
- PAM authentication is provider-based. In development mode, PAM stores local password hashes; with LDAP enabled, LDAP becomes external source of truth for identity verification.
- JWT tokens are signed with a strong secret/key pair; short-lived access tokens minimize impact of theft
- The connector is installed on authorized workstations; it is not a security boundary itself (the proxy is)
- All proxy connections require a valid, unexpired session token — even if someone reaches the proxy port directly

### What PAM enforces (server-side, not UI-only)
- Authentication on every API call and every proxy connection
- Policy check before every session launch
- Credential isolation — users cannot extract credentials through any path
- Audit logging of all security-relevant events

## 14. Risks / Hard Parts / Tradeoffs

| Risk / Hard Part | Severity | Mitigation |
|-----------------|----------|------------|
| SSH proxy implementation complexity | High | Use `golang.org/x/crypto/ssh` — well-proven. Start here, it's the highest-value path. |
| DB wire protocol parsing for statement capture | Medium | **Deferred beyond v1.** v1 ships connection/session audit only. |
| Connector cross-platform support | Medium | Go cross-compilation helps. Windows client launching (PuTTY, DBeaver, WinSCP) needs testing on real Windows machines. |
| Credential security in transit to clients | Medium | Prefer session tokens over credential forwarding. For DBeaver/FileZilla: use PAM proxy so credentials never leave the server. |
| SFTP relay complexity | Medium | Use `pkg/sftp` Go library. Proxying SFTP is well-understood but needs careful testing. |
| Session recording storage growth | Low (v1) | Compress recordings. Monitor disk usage. Object storage deferred. |
| Single encryption key for vault | Medium | Temporary v1 approach, clearly documented. Upgrade to external KMS/Vault required post-v1. |
| Auth provider expansion complexity | Low | Keep provider interface explicit from the start (`local` + future `ldap`), so adding providers does not impact authorization/session/policy paths. |
| Redis RESP parsing for command logging | Low | Include if simple within proxy design; otherwise defer. Connection audit guaranteed. |
| Connector deployment/updates | Medium | Ship as a single binary. Auto-update mechanism deferred to post-v1. |

### Key tradeoffs made
- **Monolith over microservices**: faster to build, deploy, and debug. Can be split later if needed.
- **PostgreSQL for everything**: credentials, audit, config in one DB. Simpler ops. Acceptable at v1 scale.
- **Proxy everything through PAM**: increases latency slightly but is the only way to guarantee credential isolation and audit. Non-negotiable.
- **Asciicast for SSH recording over video**: lightweight, text-searchable, good enough for v1. Video replay deferred.
- **Keyboard-interactive for SSH session auth**: well-supported across all target clients, avoids username-field hacks.
- **Systemd on Linux VM for production**: simple, proven, single-server deployment. Docker Compose for dev/test only.
- **Provider-first auth architecture**: local provider now for rapid development/POC, LDAP added later on same interface without changing authorization/session/policy internals.
- **Temporary app-level vault key**: ships fast, clearly documented as requiring KMS migration post-v1.

## 15. Recommended Implementation Order

### Phase 1: Foundation (weeks 1-2)
1. Monorepo setup, build tooling, CI skeleton
2. PostgreSQL schema + migrations
3. Backend bootstrap (HTTP server, config, DB connection, middleware)
4. UI bootstrap (React + TailAdmin, routing, auth context)
5. Shared contracts (API spec)

### Phase 2: Core Identity & Inventory (weeks 2-3)
6. Local auth provider + JWT auth flow
7. User/group/role model + admin UI
8. Asset inventory CRUD + UI
9. Access policy model + assignment UI
10. LDAP provider integration on same auth-provider interface (after local auth is stable)

### Phase 3: Credential Vault (week 3)
10. Credential storage with encryption
11. Admin UI for credential management

### Phase 4: SSH Proxy — the critical path (weeks 3-5)
12. SSH proxy server implementation
13. Session token flow (launch → proxy auth → upstream)
14. Full I/O session recording
15. Connector: SSH launch path (macOS/Linux first, then Windows/PuTTY)
16. Session history UI + text replay

### Phase 5: DB, SFTP, Redis Paths (weeks 5-7)
17. DB TCP proxy + DBeaver launch path
18. SFTP relay + FileZilla/WinSCP launch path
19. Redis proxy + terminal launch path
20. Audit events for all connection types

### Phase 6: Polish & Harden (weeks 7-8)
21. Audit log UI with search/filter
22. Dashboard page
23. Security hardening (input validation, rate limiting, token rotation)
24. Error handling, edge cases, reconnection behavior
25. Documentation

### Why this order
- Auth and inventory are prerequisites for everything
- SSH proxy is the highest-value, highest-fidelity path — prove the core model here first
- DB/SFTP/Redis paths reuse the same session token + policy + audit infrastructure built for SSH
- Polish last, not first

## 16. v1 Acceptance Criteria

1. A user can log in to PAM using the active auth provider (development mode: local auth)
2. An admin can add/edit/remove users, groups, and roles
3. An admin can add/edit/remove infrastructure assets (SSH, DB, SFTP, Redis)
4. An admin can assign access: user/group → asset with allowed actions
5. An admin can store credentials for each asset (encrypted at rest)
6. A user sees only their assigned assets in the access table
7. A user can launch an SSH session to an assigned server — connection is proxied through PAM
8. The SSH session is fully recorded (keystrokes + output) and stored
9. A user can launch a DBeaver session to an assigned database — connection is brokered through PAM
10. A user can launch a file transfer session (FileZilla/WinSCP) to an assigned endpoint — traffic relayed through PAM
11. A user can launch a Redis CLI session — connection proxied through PAM
12. All session events (start, end, duration, user, asset) are recorded in the audit log
13. An admin can view the audit log with search and filters
14. An admin can view session history and replay SSH sessions as text
15. The connector correctly launches PuTTY (Windows), native terminal (macOS/Linux), DBeaver, FileZilla/WinSCP, and redis-cli
16. No user ever sees or receives raw target credentials
17. All policy enforcement happens server-side — bypassing the UI does not grant access
18. The system runs as a single deployable unit (one Go binary + static UI assets + PostgreSQL) deployed via systemd on a Linux VM
19. Credential encryption uses AES-256-GCM with documented temporary key management approach
20. LDAP can be introduced as an additional auth provider without reworking authorization, policy enforcement, session handling, or audit model
