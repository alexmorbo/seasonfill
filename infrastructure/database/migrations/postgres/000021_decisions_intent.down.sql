-- 091a / F-P2-2: rollback drops the intent column. Captured intent on
-- rolled-back rows is lost — the GrabDrawer "why this grab" subsection
-- will simply render the pre-091a "no intent recorded" state again.
ALTER TABLE decisions
    DROP COLUMN intent;
