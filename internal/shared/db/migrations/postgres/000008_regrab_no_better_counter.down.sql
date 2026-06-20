-- 039f-1 down: drop the live counter table.
DROP INDEX IF EXISTS idx_regrab_no_better_counter_instance_id;
DROP INDEX IF EXISTS idx_regrab_no_better_counter_triple;
DROP TABLE IF EXISTS regrab_no_better_counter;
