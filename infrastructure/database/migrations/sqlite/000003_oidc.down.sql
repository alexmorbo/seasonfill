DROP INDEX IF EXISTS idx_admin_users_oidc_subject;
ALTER TABLE admin_users DROP COLUMN oidc_subject;
ALTER TABLE runtime_config DROP COLUMN oidc_allowed_groups;
ALTER TABLE runtime_config DROP COLUMN oidc_username_claim;
ALTER TABLE runtime_config DROP COLUMN oidc_scopes;
ALTER TABLE runtime_config DROP COLUMN oidc_redirect_url;
ALTER TABLE runtime_config DROP COLUMN oidc_client_id;
ALTER TABLE runtime_config DROP COLUMN oidc_issuer;
