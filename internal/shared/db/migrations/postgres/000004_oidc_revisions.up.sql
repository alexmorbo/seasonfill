-- OIDC revisions (Story 037d).
-- D-1: oidc_client_secret_ciphertext — AES-GCM ciphertext. NULL/empty = not set.
-- D-5: oidc_groups_claim — dot-notation path (default "groups").
ALTER TABLE runtime_config
    ADD COLUMN oidc_client_secret_ciphertext bytea,
    ADD COLUMN oidc_groups_claim text NOT NULL DEFAULT 'groups';
