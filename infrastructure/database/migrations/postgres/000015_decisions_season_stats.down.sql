-- 046a down (Postgres).
ALTER TABLE decisions
    DROP COLUMN grabbed_episodes,
    DROP COLUMN existing_episodes,
    DROP COLUMN aired_episodes,
    DROP COLUMN total_episodes;
