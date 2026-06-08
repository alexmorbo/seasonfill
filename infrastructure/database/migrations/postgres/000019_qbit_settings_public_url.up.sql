-- 082: F-P2-1 backend. Add browser-reachable qBittorrent web UI URL.
-- Nullable — empty/absent value means the SPA falls back to the
-- in-cluster `url`, preserving the deployed F-P0-8 GrabDrawer "open
-- in qBit" link behaviour for instances that have not opted in.
ALTER TABLE instance_qbit_settings
    ADD COLUMN qbit_public_url text;
