# apps/connector — PAM Desktop Launcher

Thin desktop binary (Go, cross-compiled) that receives launch instructions from the PAM API and spawns approved client applications pointed at PAM's proxy endpoints.

## v1 Design (locked)

- Authenticates using the user's JWT (same as UI login) — no device registration or admin approval
- Calls `POST /sessions/launch` to get a short-lived launch payload
- Creates temporary local connection material (e.g., DBeaver profile) as needed
- Launches the correct client:
  - **SSH**: PuTTY (Windows) or native terminal (macOS/Linux)
  - **Database**: DBeaver with temporary connection profile
  - **SFTP**: FileZilla (macOS/Linux) or WinSCP (Windows)
  - **Redis**: Terminal with `redis-cli`
- Cleans up temporary material after session ends

## What the connector does NOT do

- Store credentials persistently
- Make policy decisions (server-side only)
- Bypass the proxy layer
- Provide a shell or terminal of its own

## Structure

```
cmd/connector/     Entry point
internal/
  launch/          Client launcher (per OS, per asset type)
  config/          Local config, PAM server URL, token storage
  auth/            JWT authentication with PAM API
```

## Build

```bash
# From repo root
make build-connector

# Cross-compile
GOOS=darwin GOARCH=arm64 go build -o bin/pam-connector-darwin-arm64 ./cmd/connector
GOOS=linux GOARCH=amd64 go build -o bin/pam-connector-linux-amd64 ./cmd/connector
GOOS=windows GOARCH=amd64 go build -o bin/pam-connector-windows-amd64.exe ./cmd/connector
```
