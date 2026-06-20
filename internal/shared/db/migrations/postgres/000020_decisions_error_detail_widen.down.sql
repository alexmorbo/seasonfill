-- 092 / F-P2-4: rollback to varchar(300). Note: rows containing a
-- post-widen error_detail >300 chars will fail this down migration —
-- by design. The operator must accept data loss to roll back.
ALTER TABLE decisions
    ALTER COLUMN error_detail TYPE character varying(300);
