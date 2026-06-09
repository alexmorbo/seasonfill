-- 092 / F-P2-4: widen decisions.error_detail from varchar(300) to text
-- so the full upstream Sonarr response body (capped at 4 KiB by
-- application/evaluate.truncateErrorDetail) persists end-to-end. The
-- old varchar(300) bound dropped most of a typical NzbDrone stack
-- trace before it ever reached the drawer.
ALTER TABLE decisions
    ALTER COLUMN error_detail TYPE text;
