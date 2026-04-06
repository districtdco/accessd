# scripts/

Dev tooling and helper scripts.

## Included

- `smoke_api.sh`
  - Lightweight backend smoke check.
  - Verifies:
    - `GET /health/ready`
    - login flow (`POST /auth/login`)
    - `GET /access/my`
    - `POST /sessions/launch` response shape

## Usage

```bash
./scripts/smoke_api.sh
```

Optional environment overrides:

- `API_BASE_URL` (default: `http://127.0.0.1:8080`)
- `PAM_SMOKE_USERNAME` (default: `admin`)
- `PAM_SMOKE_PASSWORD` (default: `admin123`)
- `PAM_SMOKE_ACTION` (default: `shell`)
- `PAM_SMOKE_ASSET_ID` (optional explicit asset id)
