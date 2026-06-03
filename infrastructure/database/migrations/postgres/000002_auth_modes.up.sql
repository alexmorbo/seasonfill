-- Auth modes (forms / basic / none) + opt-in local-address bypass.
-- Defaults preserve current behaviour: mode=forms, bypass=false. The
-- session_epoch column is bumped by the application whenever the auth
-- mode (or any field that should invalidate live sessions) changes;
-- existing cookies were issued before this column existed and decode
-- with ep=0, which validates against the default 0 epoch.
ALTER TABLE runtime_config
    ADD COLUMN auth_mode text NOT NULL DEFAULT 'forms'
        CHECK (auth_mode IN ('forms','basic','none')),
    ADD COLUMN auth_local_bypass boolean NOT NULL DEFAULT false,
    ADD COLUMN auth_local_networks text NOT NULL DEFAULT
        '["127.0.0.0/8","::1/128","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16","169.254.0.0/16","fe80::/10","fc00::/7"]',
    ADD COLUMN auth_session_epoch bigint NOT NULL DEFAULT 0;
