# Remaining Blockers (Current)

## 1) MSSQL full client<->proxy TLS tunnel mode is not implemented
- Why it remains: the MSSQL proxy still rejects required client TLS and upstream TLS tunnel negotiation in this slice.
- Evidence in code:
  - `apps/api/internal/mssqlproxy/server.go` rejects required client TLS (`client requested required tls...`)
  - `apps/api/internal/mssqlproxy/server.go` returns `upstream requested tls encryption; ... not yet implemented`
- Impact: MSSQL assets requiring strict client<->proxy and upstream TDS TLS tunnel mode are not fully supported in this slice.
- Suggested next action: implement and validate full TDS TLS tunnel mode on both client and upstream legs.

## 2) Redis client-leg TLS to PAM Redis proxy is still unavailable in effective product flow
- Why it remains: connector command builders can pass `redis-cli --tls`, but PAM launch responses currently issue non-TLS client-leg Redis proxy endpoints (`redis_tls=false`) and the Redis proxy listener is plaintext for client sessions in this slice.
- Impact: environments requiring TLS from local client to PAM Redis proxy need compensating controls (loopback trust boundary + host hardening) or further implementation.
- Suggested next action: add optional TLS listener mode for session-scoped Redis proxy endpoints and emit `redis_tls=true` launch payload when enabled.

## 3) Full Go runtime test execution is blocked in this environment
- Why it remains: runtime test binaries fail at execution with local dyld error (`missing LC_UUID load command`), unrelated to PAM business logic changes.
- What was validated in this pass:
  - `GOCACHE=$(pwd)/.gocache go build ./...` in `apps/api` succeeded (compile check).
  - `GOCACHE=$(pwd)/.gocache go test -c ./internal/auth ./internal/integration ./internal/sessions` succeeded (test binaries compile).
  - `npm run build` in `apps/ui` succeeded (typecheck + bundle).
- What remains to run elsewhere:
  - full runtime Go test execution (for example `go test ./...` and integration suites) in a host/runtime where test binaries execute normally.
