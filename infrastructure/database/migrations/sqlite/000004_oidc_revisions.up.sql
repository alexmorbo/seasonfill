ALTER TABLE runtime_config ADD COLUMN oidc_client_secret_ciphertext blob;
ALTER TABLE runtime_config ADD COLUMN oidc_groups_claim text NOT NULL DEFAULT 'groups';
