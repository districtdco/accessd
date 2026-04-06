// Package vault provides encrypted credential storage and retrieval.
// Credentials are encrypted at rest using AES-256-GCM.
// v1 uses a single application master key from PAM_VAULT_KEY env var.
// This is a temporary approach — external KMS integration is planned post-v1.
package vault
