ALTER TABLE ldap_settings
    ADD COLUMN IF NOT EXISTS surname_attribute TEXT NOT NULL DEFAULT 'sn',
    ADD COLUMN IF NOT EXISTS ssh_key_attribute TEXT NOT NULL DEFAULT 'SshPublicKey',
    ADD COLUMN IF NOT EXISTS avatar_attribute TEXT NOT NULL DEFAULT 'jpegPhoto';
