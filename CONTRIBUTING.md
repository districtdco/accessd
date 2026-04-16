# Contributing to AccessD

Thank you for your interest in contributing to AccessD.

AccessD is developed and maintained by Districtd, and we welcome community contributions that improve reliability, security, usability, and documentation.

## Before You Start

- Read the [README](README.md) for product context and local setup.
- Review open issues and pull requests before starting duplicate work.
- For security findings, do **not** open a public issue. Use the process in [SECURITY.md](SECURITY.md).

## Development Setup

Prerequisites:

- Go 1.22+
- Node.js 20+
- Docker

Quick start:

```bash
make dev-up
make dev-api
make dev-ui
```

Optional (for launcher flows on operator machine):

```bash
make dev-connector
```

## Branch and Commit Guidelines

- Create a focused branch per change.
- Keep commits small and descriptive.
- Use imperative commit messages (example: `Add session export audit filter`).
- Link related issues in commit or PR description.

## Pull Request Expectations

Please include the following in every PR:

- Clear problem statement and solution summary
- Scope of change (API, UI, connector, docs, or infra)
- Testing notes (what you ran and what you validated)
- Screenshots for UI changes
- Migration or rollout notes when applicable

## Quality Checklist

Before opening a PR, run:

```bash
# API
cd apps/api && go test ./...

# Connector
cd ../connector && go test ./...

# UI
cd ../ui && npm ci && npm run build
```

Also ensure:

- New behavior is covered by tests where practical
- Existing tests continue to pass
- Public API/contract changes are reflected in `packages/contracts/api.yaml`
- Documentation is updated for behavioral or operational changes

## Style and Review

- Prefer readability over cleverness.
- Keep interfaces stable and backwards compatible unless explicitly changing contracts.
- Add comments only when code intent is not obvious.
- Treat review feedback as collaborative engineering, not gatekeeping.

## Community

By participating, you agree to follow our [Code of Conduct](CODE_OF_CONDUCT.md).

We appreciate your time and contributions.
