DROP INDEX IF EXISTS idx_admin_users_oidc_subject;

ALTER TABLE admin_users
    DROP COLUMN IF EXISTS oidc_subject;

ALTER TABLE runtime_config
    DROP CONSTRAINT IF EXISTS runtime_config_auth_mode_check;

ALTER TABLE runtime_config
    ADD CONSTRAINT runtime_config_auth_mode_check
    CHECK (auth_mode IN ('forms','basic','none'));

ALTER TABLE runtime_config
    DROP COLUMN IF EXISTS oidc_allowed_groups,
    DROP COLUMN IF EXISTS oidc_username_claim,
    DROP COLUMN IF EXISTS oidc_scopes,
    DROP COLUMN IF EXISTS oidc_redirect_url,
    DROP COLUMN IF EXISTS oidc_client_id,
    DROP COLUMN IF EXISTS oidc_issuer;
