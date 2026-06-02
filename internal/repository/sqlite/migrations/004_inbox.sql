-- Inbox messages: IS24-related emails fetched via IMAP and classified by AI.
-- message_id is unique so re-fetching the same mail across polls is idempotent.
CREATE TABLE IF NOT EXISTS inbox_messages (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id        TEXT UNIQUE,
    from_addr         TEXT NOT NULL DEFAULT '',
    subject           TEXT NOT NULL DEFAULT '',
    snippet           TEXT NOT NULL DEFAULT '',
    is24_id           TEXT NOT NULL DEFAULT '',
    listing_id        INTEGER,
    is_landlord_reply INTEGER NOT NULL DEFAULT 0,
    summary           TEXT NOT NULL DEFAULT '',
    notified          INTEGER NOT NULL DEFAULT 0,
    received_at       DATETIME,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_inbox_received ON inbox_messages (received_at DESC);
CREATE INDEX IF NOT EXISTS idx_inbox_landlord ON inbox_messages (is_landlord_reply);
