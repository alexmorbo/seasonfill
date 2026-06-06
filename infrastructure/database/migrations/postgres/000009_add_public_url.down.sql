-- 041 down: remove the optional public URL column.
ALTER TABLE sonarr_instance DROP COLUMN IF EXISTS public_url;
