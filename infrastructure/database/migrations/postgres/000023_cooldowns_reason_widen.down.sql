-- 118: rollback to varchar(128). Rows whose reason exceeds 128 chars
-- after the widening will fail this down migration — by design,
-- mirroring migration 20's rollback semantics. The operator must
-- accept data loss to roll back.
ALTER TABLE cooldowns
    ALTER COLUMN reason TYPE character varying(128);
