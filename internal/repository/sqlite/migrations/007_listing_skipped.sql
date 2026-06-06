-- Manual ignore flag: the user can mark uninteresting listings as skipped
-- from the dashboard so the scheduler stops considering them for auto-contact
-- without deleting them from history.
ALTER TABLE listings ADD COLUMN skipped INTEGER NOT NULL DEFAULT 0;
