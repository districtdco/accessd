# Remaining Blockers (Reconciled 2026-04-07)

Only active, real blockers are listed below.

## 1) MSSQL full client<->proxy TLS tunnel mode is not implemented
- Severity: `staging blocker`, `production blocker`
- Scope: does **not** block basic local testing if MSSQL target is configured without strict TLS tunnel requirements.
- Exact impact:
  - MSSQL sessions can fail when client/upstream requires full TDS TLS tunnel negotiation.
  - Current code paths still reject required client TLS and upstream-required tunnel mode.
- Evidence in code:
  - `apps/api/internal/mssqlproxy/server.go` (`client requested required tls...`)
  - `apps/api/internal/mssqlproxy/server.go` (`upstream requested tls encryption; ... not yet implemented`)
- Next action:
  - Implement full bidirectional TDS TLS tunnel support in MSSQL proxy and add integration validation with strict TLS-required MSSQL targets.

## 2) Redis client-leg TLS to PAM Redis proxy is not implemented
- Severity: `production blocker`
- Scope: does **not** block local testing in current loopback trust model.
- Exact impact:
  - Connector/API launch payloads currently operate with plaintext local client leg (`redis_tls=false` in effective flow).
  - Environments requiring TLS from local client to PAM Redis proxy endpoint cannot satisfy that requirement yet.
- Evidence in code/docs:
  - Launch payload handling supports `redis_tls`, but current flow remains non-TLS for client leg in this slice.
  - Documented in `apps/connector/README.md` and local-testing limitations.
- Next action:
  - Add optional TLS listener mode for session-scoped Redis proxy endpoints and emit `redis_tls=true` launch payload when enabled.

## 3) Runtime Go test execution is blocked in this current host environment
- Severity: `local-testing blocker` (environment-specific)
- Scope: blocks full runtime `go test` execution **in this environment only**, not a product architecture blocker.
- Exact impact:
  - Go tests compile, but runtime execution can fail with local dyld loader error (`missing LC_UUID load command`).
  - Prevents trustworthy full runtime test pass on this machine until host/runtime issue is fixed.
- What is still validated locally:
  - API/connector compile/build checks
  - UI build checks
  - script syntax checks
- Next action:
  - Run full Go runtime tests on a clean host/runtime (or fix local toolchain/runtime), then capture results back into checklist/release notes.
