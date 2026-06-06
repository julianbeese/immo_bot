-- Backfill marker: rows seeded by the one-time `-backfill` command to record
-- IS24 IDs that already existed on the platform when the bot was first deployed.
-- These rows have notified=1 and contacted=1 so the regular pipeline ignores
-- them, but the column lets us distinguish them from fully-scraped listings.
ALTER TABLE listings ADD COLUMN backfilled INTEGER NOT NULL DEFAULT 0;
