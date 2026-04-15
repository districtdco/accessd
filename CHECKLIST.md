# AccessD v1 — Build Checklist (Reconciled)

## Current Status

- Phase: hardening + local test-readiness consolidation
- Last updated: 2026-04-15
- Source of truth docs:
  - `LOCAL_TESTING.md` (local runbook)
  - `REMAINING_BLOCKERS.md` (real blockers only)

---

## What Is Ready Now

- Monorepo structure + CI baseline are in place.
- API, UI, connector run together locally.
- Local script suite exists for bring-up, seeding, and guided test flow:
  - `scripts/check_env.sh`
  - `scripts/dev_up.sh`
  - `scripts/dev_api.sh`
  - `scripts/dev_ui.sh`
  - `scripts/dev_connector.sh`
  - `scripts/dev_seed.sh`
  - `scripts/test_api_smoke_extended.sh`
  - `scripts/test_matrix.sh`
- Access policy model is implemented via `access_grants` (not a separate `access_policies` table in this slice).
- DB proxy support exists for PostgreSQL/MySQL/MSSQL (with MSSQL TLS tunnel limitation).
- SFTP relay and Redis proxy flows are implemented (with Redis client-leg TLS limitation).
- Connector trust model is HMAC-signed launch tokens (`ACCESSD_CONNECTOR_SECRET`), not JWT login flow.
- API TLS posture is edge-terminated TLS (reverse proxy/LB), not app-native TLS listener.

---

## Reconciliation Ledger (All Previously Open Items)

### A) Done (wording was stale)

- Phase 7: Implement asset CRUD API (`GET/POST/PUT/DELETE /assets`)
  - Reconciled as done for current admin-scoped endpoints (`GET/POST/PUT /admin/assets`, detail/credential/grant helpers). Delete remains intentionally deferred.
- Phase 8: Update "my assets" page to show only policy-allowed assets with permitted actions
  - Already true via `GET /access/my` + allowed actions.
- Phase 14: Event types (`login`, `logout`, `session_start`, `session_end`, `credential_access`, `file_operation`, `admin_action`)
  - Implemented in current event model (plus connector/db/redis/session lifecycle event types).
- Phase 16: Implement client launcher abstraction (per OS, per asset type)
  - `apps/connector/internal/launch` now provides per-OS launch abstraction.
- Phase 16: macOS launcher matrix item
  - Shell/SFTP/DBeaver/Redis launch flows implemented.
- Phase 16: Linux launcher matrix item
  - Shell/SFTP/DBeaver/Redis launch flows implemented.
- Phase 16: Windows launcher matrix item
  - PuTTY/WinSCP/DBeaver/Redis terminal launch flows implemented.
- Phase 16: Handle "client not installed" errors gracefully
  - Implemented via structured connector `LaunchError` + `client_not_installed` code/hints.
- Phase 17: Connector auth wording (JWT-based)
  - Superseded by implemented HMAC-signed connector token verification model.
- Phase 18: README with project overview, architecture, setup instructions
  - Updated and reconciled in this pass.
- Phase 18: Dev setup guide (docker-compose, env vars, first run)
  - Covered by `LOCAL_TESTING.md` + script suite.
- Blocker/Open decision: default dev admin bootstrap final defaults
  - Locked for local testing (`admin` / `admin123`) with explicit production rotation expectation.

### B) Real Blockers Before Rollout/Testing

- Phase 2: Add migration test coverage in CI (fresh DB + idempotency checks)
  - Rollout confidence blocker (staging/prod quality gate).
- Phase 4: Cover auth/users/assets/policies/sessions endpoints in OpenAPI
  - Contract completeness blocker for stable integrations.
- Phase 5: Expand auth provider test matrix (including expired session/provider compatibility)
  - Reliability blocker before production rollout.
- Phase 8: Policy evaluation tests (direct/group/no-access/multiple-policy scenarios)
  - Authorization confidence blocker.
- Phase 9: Credential tests (roundtrip/missing-key/no-plaintext guarantees)
  - Security confidence blocker.
- Phase 11: SFTP manual/e2e verification (upload/download + audit checks)
  - Protocol rollout blocker.
- Phase 12: DB proxy practical e2e verification (Postgres/MySQL/MSSQL + audit checks)
  - Protocol rollout blocker.
- Phase 13: Redis practical e2e verification + audit checks
  - Protocol rollout blocker.
- Phase 14: Ensure audit writes are non-blocking across all hot paths
  - Scale/reliability blocker.
- Phase 14: Add broader audit/session lifecycle tests
  - Reliability blocker.
- Phase 17: Input validation coverage across all API endpoints
  - Security blocker.
- Phase 17: Ensure no credential leak in logs/errors/API responses (full formal sweep)
  - Security blocker.
- Phase 17: OWASP top-10 security review against API
  - Security blocker.
- Phase 17: Dependency audit (`go mod tidy`, `npm audit`)
  - Supply-chain/security blocker.
- Phase 18: API docs completeness from OpenAPI (aligned with implemented routes)
  - Integration/readiness blocker.
- Phase 19: End-to-end workflow verification for each asset type
  - Release-readiness blocker.
- Phase 19: All v1 acceptance criteria pass (PLAN.md section 16)
  - Release-readiness blocker.
- Phase 19: Security test (credential extraction attempts via API/connector/proxy)
  - Release-readiness blocker.
- Phase 19: Audit verification across workflows
  - Release-readiness blocker.
- Open decision: MSSQL client/proxy TLS tunnel support
  - Real blocker (staging/prod); tracked in `REMAINING_BLOCKERS.md`.
- Open decision: Redis client-leg TLS mode
  - Real blocker (production); tracked in `REMAINING_BLOCKERS.md`.

### C) Deferred Beyond Current Rollout

- Phase 2: Add `updated_at` trigger strategy (DB trigger vs app-managed)
- Phase 4: Generate TypeScript types from OpenAPI
- Phase 6: User CRUD API (`GET/POST/PUT /users`, `GET /users/:id`)
- Phase 6: Group CRUD API
- Phase 6: Group membership API (`POST /groups/:id/members`)
- Phase 6: Build admin groups page
- Phase 6: CRUD/group-sync test expansion
- Phase 7: Asset validation/type-field test expansion
- Phase 10: Full asciicast v2 recording
- Phase 10: Compress/store SSH recordings
- Phase 10: Recording-specific SSH e2e verification
- Phase 15: Replay test with real full recordings
- Phase 19: Session recording replay parity verification
- Phase 16: Connector protocol-handler registration (`pam://` URI scheme, optional)
- Phase 16: Cross-platform runtime test matrix (macOS/Linux/Windows)
- Phase 16: Formal connector cross-compile packaging pipeline (beyond current local build path)
- Phase 18: Admin guide and connector installation guide completeness
- Phase 19: Load test for 50+ concurrent SSH sessions
- Phase 19: Staging deploy walkthrough, stakeholder demo, release tag cut
- Open decision: LDAP schema details for target enterprise directory
  - Explicitly deferred until target directory samples are available.

### D) Not Needed / Superseded By Current Implementation

- Phase 3: Install TailAdmin template and integrate with Vite + React
  - Superseded by current custom UI shell (role-aware app layout already in use).
- Phase 8: Create migration `access_policies` table
  - Superseded by `access_grants` model in this slice.
- Phase 8: Implement policy CRUD API
  - Superseded by direct grant management APIs and effective-access evaluation path.
- Phase 14: Implement single centralized audit writer module called by all modules
  - Superseded by current per-module audited event writes; remaining concern is non-blocking behavior, tracked above.
- Phase 16: Connector authentication via JWT login flow and stored connector login config
  - Superseded by HMAC-signed backend launch tokens (`ACCESSD_CONNECTOR_SECRET`) and loopback trust boundary.
- Phase 17: JWT secret/key configuration for production
  - Superseded wording for current architecture (cookie sessions + launch token secret + connector secret).
- Phase 17: HTTPS/TLS listener in API app process
  - Superseded by explicit edge TLS termination architecture.

---

## Active Work Queue (Actionable)

Use this as the short actionable queue for near-term progress:

- [ ] Close blockers from `REMAINING_BLOCKERS.md` (MSSQL TLS tunnel, Redis client-leg TLS, runtime Go test host issue).
- [ ] Expand integration/e2e coverage for all six protocol flows using `LOCAL_TESTING.md`.
- [ ] Complete security hardening sweep (input validation, OWASP checklist, dependency audit, leak review).
- [ ] Finalize OpenAPI coverage/documentation alignment.
