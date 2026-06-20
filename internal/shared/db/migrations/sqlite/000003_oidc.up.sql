-- SQLite mirror of postgres v3. SQLite has no DROP CONSTRAINT; the
-- application layer (runtimeconfig.UseCase) is the authoritative enum
-- gate for auth_mode (validates against runtime.AuthMode{Forms,Basic,None,OIDC}).
ALTER TABLE runtime_config ADD COLUMN oidc_issuer text NOT NULL DEFAULT '';
ALTER TABLE runtime_config ADD COLUMN oidc_client_id text NOT NULL DEFAULT '';
ALTER TABLE runtime_config ADD COLUMN oidc_redirect_url text NOT NULL DEFAULT '';
ALTER TABLE runtime_config ADD COLUMN oidc_scopes text NOT NULL DEFAULT '["openid","profile","email"]';
ALTER TABLE runtime_config ADD COLUMN oidc_username_claim text NOT NULL DEFAULT 'preferred_username';
ALTER TABLE runtime_config ADD COLUMN oidc_allowed_groups text NOT NULL DEFAULT '[]';

ALTER TABLE admin_users ADD COLUMN oidc_subject text;

CREATE UNIQUE INDEX idx_admin_users_oidc_subject
    ON admin_users (oidc_subject)
    WHERE oidc_subject IS NOT NULL;
