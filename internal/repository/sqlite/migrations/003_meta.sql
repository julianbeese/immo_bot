-- Key/value table for small operational state, e.g. the last successful poll
-- timestamp used by the container health check.
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
