CREATE TABLE admin_users (
    id integer PRIMARY KEY AUTOINCREMENT,
    username text,
    password_hash text,
    auto_generated numeric,
    created_at datetime,
    updated_at datetime
);

CREATE TABLE cooldowns (
    scope text,
    key text,
    expires_at datetime,
    reason text,
    created_at datetime,
    PRIMARY KEY (scope, key)
);

CREATE TABLE decisions (
    id text,
    scan_run_id text,
    instance_name text,
    series_id integer,
    series_title text,
    season_number integer,
    decision text,
    reason text,
    missing_count integer,
    existing_count integer,
    releases_found integer,
    candidates_count integer,
    filtered_out JSON,
    selected_guid text,
    selected_data JSON,
    would_grab numeric,
    error_detail text,
    superseded_by_id text,
    created_at datetime,
    PRIMARY KEY (id)
);

CREATE TABLE grab_records (
    id text,
    instance_name text,
    series_id integer,
    series_title text,
    season_number integer,
    release_guid text,
    release_title text,
    download_id text,
    indexer_id integer,
    indexer_name text,
    custom_format_score integer,
    quality text,
    coverage_count integer,
    status text,
    error_message text,
    scan_run_id text,
    attempts integer,
    created_at datetime,
    updated_at datetime,
    PRIMARY KEY (id)
);

CREATE TABLE instance_secret (
    instance_id integer,
    secret_name text,
    ciphertext blob NOT NULL,
    created_at datetime,
    updated_at datetime,
    PRIMARY KEY (instance_id, secret_name)
);

CREATE TABLE origin_releases (
    instance_name text,
    series_id integer,
    season_number integer,
    guid text,
    indexer_id integer,
    indexer_name text,
    source text,
    first_seen_at datetime,
    last_seen_at datetime,
    last_used_at datetime,
    PRIMARY KEY (instance_name, series_id, season_number)
);

CREATE TABLE runtime_config (
    id integer PRIMARY KEY AUTOINCREMENT,
    cron_enabled numeric,
    cron_schedule text,
    cron_on_start numeric,
    cron_jitter_seconds integer,
    scan_shutdown_grace_sec integer,
    scan_cooldown_sweep_sec integer,
    dry_run numeric,
    global_rpm integer,
    global_burst integer,
    auth_session_ttl_sec integer,
    auth_secure_cookie numeric,
    auth_trusted_proxies text,
    api_key_ciphertext blob,
    api_key_auto_generated numeric,
    created_at datetime,
    updated_at datetime
);

CREATE TABLE scan_runs (
    id text,
    instance_name text,
    trigger text,
    started_at datetime,
    finished_at datetime,
    status text,
    series_scanned integer,
    candidates_found integer,
    grabs_performed integer,
    grabs_failed integer,
    errors_count integer,
    error_message text,
    dry_run numeric,
    created_at datetime,
    updated_at datetime,
    PRIMARY KEY (id)
);

CREATE TABLE sonarr_instance (
    id integer PRIMARY KEY AUTOINCREMENT,
    name text,
    url text,
    mode text,
    timeout_seconds integer,
    search_timeout_seconds integer,
    dry_run numeric,
    tags_mode text,
    tags_include text,
    tags_exclude text,
    search_require_all_aired numeric,
    search_skip_specials numeric,
    search_skip_anime numeric,
    search_min_custom_format_score integer,
    ranking_indexer_priority_enabled numeric,
    ranking_origin_bonus real,
    limits_scan_max_series integer,
    limits_max_grabs_per_scan integer,
    rate_limit_rpm integer,
    rate_limit_burst integer,
    cooldown_mode text,
    cooldown_series_after_grab_sec integer,
    cooldown_guid_failed_grab_sec integer,
    cooldown_guid_failed_import_sec integer,
    retry_max_attempts integer,
    retry_initial_backoff_sec integer,
    retry_max_backoff_sec integer,
    health_check_recheck_auth_sec integer,
    health_check_recheck_net_sec integer,
    created_at datetime,
    updated_at datetime
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
