-- OIDC mode (Story 037). Adds the 4th auth_mode and its config columns.
-- Defaults preserve current behaviour: empty issuer/client_id/redirect_url
-- (validated only when auth_mode=oidc), standard scopes, preferred_username
-- claim, empty groups list (no ACL filtering).
ALTER TABLE runtime_config
    ADD COLUMN oidc_issuer text NOT NULL DEFAULT '',
    ADD COLUMN oidc_client_id text NOT NULL DEFAULT '',
    ADD COLUMN oidc_redirect_url text NOT NULL DEFAULT '',
    ADD COLUMN oidc_scopes text NOT NULL DEFAULT '["openid","profile","email"]',
    ADD COLUMN oidc_username_claim text NOT NULL DEFAULT 'preferred_username',
    ADD COLUMN oidc_allowed_groups text NOT NULL DEFAULT '[]';

-- Rebuild the auth_mode CHECK constraint to allow 'oidc'. The 036 migration
-- created the constraint inline with the ADD COLUMN; postgres treats it as
-- a table-level constraint with an auto-generated name. We drop by name
-- lookup, then re-add the 4-value form.
DO $$
DECLARE
    cname text;
BEGIN
    SELECT conname INTO cname
    FROM pg_constraint
    WHERE conrelid = 'runtime_config'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) ILIKE '%auth_mode%';
    IF cname IS NOT NULL THEN
        EXECUTE format('ALTER TABLE runtime_config DROP CONSTRAINT %I', cname);
    END IF;
END$$;

ALTER TABLE runtime_config
    ADD CONSTRAINT runtime_config_auth_mode_check
    CHECK (auth_mode IN ('forms','basic','none','oidc'));

ALTER TABLE admin_users
    ADD COLUMN oidc_subject text;

CREATE UNIQUE INDEX idx_admin_users_oidc_subject
    ON admin_users (oidc_subject)
    WHERE oidc_subject IS NOT NULL;
