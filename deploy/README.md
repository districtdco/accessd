# deploy/

Production deployment configuration for systemd on Linux VM.

Planned contents:
- `pam-server.service` — systemd unit file for the PAM backend
- `pam.env.example` — example environment file with all required variables
- `nginx.conf.example` — reverse proxy config (optional, if fronting with nginx)

Docker Compose is for local development only — see `docker-compose.yml` in repo root.
