-- Story 202 (S-2): SQLite mirror of postgres 000025. boolean -> numeric,
-- timestamp with time zone -> datetime, bytea -> blob.
CREATE TABLE external_service_settings (
    service              text NOT NULL PRIMARY KEY,
    enabled              numeric NOT NULL DEFAULT 0,
    api_key_enc          blob,
    api_key_last4        text,
    proxy_url_enc        blob,
    proxy_username_enc   blob,
    proxy_password_enc   blob,
    last_test_at         datetime,
    last_test_outcome    text,
    last_test_message    text,
    created_at           datetime NOT NULL,
    updated_at           datetime NOT NULL
);
