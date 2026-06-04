ALTER TABLE runtime_config
    DROP COLUMN oidc_client_secret_ciphertext,
    DROP COLUMN oidc_groups_claim;
