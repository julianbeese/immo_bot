-- Manual "skip" flag: listings the user marked as seen/handled so the bot
-- never auto-contacts them. Distinct from `contacted` (a real sent message).
ALTER TABLE listings ADD COLUMN skipped INTEGER NOT NULL DEFAULT 0;
