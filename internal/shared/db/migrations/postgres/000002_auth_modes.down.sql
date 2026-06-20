ALTER TABLE runtime_config
    DROP COLUMN IF EXISTS auth_session_epoch,
    DROP COLUMN IF EXISTS auth_local_networks,
    DROP COLUMN IF EXISTS auth_local_bypass,
    DROP COLUMN IF EXISTS auth_mode;
