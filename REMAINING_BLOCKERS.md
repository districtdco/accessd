# Remaining Blockers (Post-Consolidation Pass)

## 1) App-level HTTPS/TLS listener is not implemented
- Why it remains: current API server intentionally runs HTTP only; introducing in-process TLS would change deployment posture and cert lifecycle handling.
- Impact: production rollout must front PAM with reverse proxy/LB TLS termination. Direct public exposure of `PAM_HTTP_ADDR` is not acceptable.
- Suggested next action: standardize a reverse-proxy reference deployment (nginx/traefik/ALB) and enforce HTTPS at edge before rollout.

## 2) MSSQL full client<->proxy TLS tunnel mode is not implemented
- Why it remains: current MSSQL proxy slice focuses query capture and session brokering, not full TDS TLS tunnel passthrough.
- Impact: MSSQL assets requiring strict end-to-end client TLS tunnel are not fully supported in this slice.
- Suggested next action: implement and test full TDS TLS tunnel negotiation path for client and upstream legs.

## 3) Redis client-leg TLS to PAM proxy is not implemented
- Why it remains: connector `redis-cli` path currently targets a session-scoped loopback/plain endpoint to PAM Redis proxy.
- Impact: environments requiring TLS from local client to PAM Redis proxy need compensating controls (local trust boundary) or additional implementation.
- Suggested next action: add optional TLS listener mode for redis proxy session endpoints and connector flags for `redis-cli --tls`.

## 4) Full test suite runtime execution is blocked in this environment
- Why it remains: `go test ./...` fails with local dyld runtime issue (`missing LC_UUID load command`) unrelated to code logic.
- Impact: this pass validated via `go build ./...` (API compile) and `npm run build` (UI typecheck+bundle), but not full Go runtime test execution.
- Suggested next action: rerun `go test ./...` in a clean host/runtime where Go test binaries execute normally.
