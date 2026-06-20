-- Story 202 (S-2): runtime config for external enrichment services
-- (TMDB / OMDb / TVDB). Encrypted fields use the same AES-GCM helper
-- as instance_qbit_settings.password_encrypted. last_test_* columns
-- store the most recent /test outcome for the Settings UI status pill.
CREATE TABLE external_service_settings (
    service              text NOT NULL PRIMARY KEY,
    enabled              boolean NOT NULL DEFAULT false,
    api_key_enc          bytea,
    api_key_last4        text,
    proxy_url_enc        bytea,
    proxy_username_enc   bytea,
    proxy_password_enc   bytea,
    last_test_at         timestamp with time zone,
    last_test_outcome    text,
    last_test_message    text,
    created_at           timestamp with time zone NOT NULL,
    updated_at           timestamp with time zone NOT NULL
);
