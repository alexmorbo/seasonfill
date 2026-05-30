CREATE TABLE admin_users (
    id bigserial PRIMARY KEY,
    username character varying(128),
    password_hash character varying(128),
    auto_generated boolean,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);

CREATE TABLE cooldowns (
    scope character varying(16) NOT NULL,
    key character varying(512) NOT NULL,
    expires_at timestamp with time zone,
    reason character varying(128),
    created_at timestamp with time zone,
    PRIMARY KEY (scope, key)
);

CREATE TABLE decisions (
    id character varying(36) PRIMARY KEY,
    scan_run_id character varying(36),
    instance_name character varying(128),
    series_id bigint,
    series_title character varying(512),
    season_number bigint,
    decision character varying(32),
    reason character varying(128),
    missing_count bigint,
    existing_count bigint,
    releases_found bigint,
    candidates_count bigint,
    filtered_out jsonb,
    selected_guid character varying(512),
    selected_data jsonb,
    would_grab boolean,
    error_detail character varying(300),
    superseded_by_id character varying(36),
    created_at timestamp with time zone
);

CREATE TABLE grab_records (
    id character varying(36) PRIMARY KEY,
    instance_name character varying(128),
    series_id bigint,
    series_title character varying(512),
    season_number bigint,
    release_guid character varying(512),
    release_title character varying(1024),
    download_id character varying(128),
    indexer_id bigint,
    indexer_name character varying(256),
    custom_format_score bigint,
    quality character varying(128),
    coverage_count bigint,
    status character varying(32),
    error_message text,
    scan_run_id character varying(36),
    attempts bigint,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);

CREATE TABLE instance_secret (
    instance_id bigint NOT NULL,
    secret_name character varying(64) NOT NULL,
    ciphertext bytea NOT NULL,
    created_at timestamp with time zone,
    updated_at timestamp with time zone,
    PRIMARY KEY (instance_id, secret_name)
);

CREATE TABLE origin_releases (
    instance_name character varying(128) NOT NULL,
    series_id bigint NOT NULL,
    season_number bigint NOT NULL,
    guid character varying(512),
    indexer_id bigint,
    indexer_name character varying(256),
    source character varying(32),
    first_seen_at timestamp with time zone,
    last_seen_at timestamp with time zone,
    last_used_at timestamp with time zone,
    PRIMARY KEY (instance_name, series_id, season_number)
);

CREATE TABLE runtime_config (
    id bigserial PRIMARY KEY,
    cron_enabled boolean,
    cron_schedule character varying(64),
    cron_on_start boolean,
    cron_jitter_seconds bigint,
    scan_shutdown_grace_sec bigint,
    scan_cooldown_sweep_sec bigint,
    dry_run boolean,
    global_rpm bigint,
    global_burst bigint,
    auth_session_ttl_sec bigint,
    auth_secure_cookie boolean,
    auth_trusted_proxies text,
    api_key_ciphertext bytea,
    api_key_auto_generated boolean,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);

CREATE TABLE scan_runs (
    id character varying(36) PRIMARY KEY,
    instance_name character varying(128),
    trigger character varying(32),
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    status character varying(32),
    series_scanned bigint,
    candidates_found bigint,
    grabs_performed bigint,
    grabs_failed bigint,
    errors_count bigint,
    error_message text,
    dry_run boolean,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);

CREATE TABLE sonarr_instance (
    id bigserial PRIMARY KEY,
    name character varying(128),
    url character varying(512),
    mode character varying(16),
    timeout_seconds bigint,
    search_timeout_seconds bigint,
    dry_run boolean,
    tags_mode character varying(16),
    tags_include text,
    tags_exclude text,
    search_require_all_aired boolean,
    search_skip_specials boolean,
    search_skip_anime boolean,
    search_min_custom_format_score bigint,
    ranking_indexer_priority_enabled boolean,
    ranking_origin_bonus numeric,
    limits_scan_max_series bigint,
    limits_max_grabs_per_scan bigint,
    rate_limit_rpm bigint,
    rate_limit_burst bigint,
    cooldown_mode character varying(16),
    cooldown_series_after_grab_sec bigint,
    cooldown_guid_failed_grab_sec bigint,
    cooldown_guid_failed_import_sec bigint,
    retry_max_attempts bigint,
    retry_initial_backoff_sec bigint,
    retry_max_backoff_sec bigint,
    health_check_recheck_auth_sec bigint,
    health_check_recheck_net_sec bigint,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);

CREATE UNIQUE INDEX idx_admin_users_username ON admin_users (username);

CREATE INDEX idx_cooldowns_expires_at ON cooldowns (expires_at);

CREATE INDEX idx_decisions_created_at_id ON decisions (created_at, id);
CREATE INDEX idx_decisions_instance_name ON decisions (instance_name);
CREATE INDEX idx_decisions_scan_run_id ON decisions (scan_run_id);
CREATE INDEX idx_decisions_series_id ON decisions (series_id);

CREATE UNIQUE INDEX idx_grab_dedupe ON grab_records (instance_name, series_id, season_number, release_guid);
CREATE INDEX idx_grab_inst_series ON grab_records (instance_name, series_id, season_number);
CREATE INDEX idx_grab_records_created_at_id ON grab_records (created_at, id);
CREATE INDEX idx_grab_records_download_id ON grab_records (download_id);
CREATE INDEX idx_grab_records_release_guid ON grab_records (release_guid);
CREATE INDEX idx_grab_records_scan_run_id ON grab_records (scan_run_id);
CREATE INDEX idx_grab_records_status ON grab_records (status);

CREATE INDEX idx_scan_runs_created_at_id ON scan_runs (created_at, id);
CREATE INDEX idx_scan_runs_instance_name ON scan_runs (instance_name);
CREATE INDEX idx_scan_runs_started_at_id ON scan_runs (started_at, id);

CREATE UNIQUE INDEX idx_sonarr_instance_name ON sonarr_instance (name);
