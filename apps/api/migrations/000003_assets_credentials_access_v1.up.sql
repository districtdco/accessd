ALTER TABLE assets
    ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}'::JSONB;

ALTER TABLE credentials
    ADD COLUMN IF NOT EXISTS username TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_credentials_asset_type
    ON credentials(asset_id, credential_type);
