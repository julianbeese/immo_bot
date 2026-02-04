-- Initial database schema for ImmobilienScout24 Bot

-- Search profiles define apartment search criteria
CREATE TABLE IF NOT EXISTS search_profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    city TEXT NOT NULL,
    districts TEXT, -- JSON array
    postal_codes TEXT, -- JSON array
    min_price INTEGER,
    max_price INTEGER,
    min_rooms REAL,
    max_rooms REAL,
    min_area INTEGER,
    max_area INTEGER,
    has_balcony INTEGER, -- nullable boolean
    has_ebk INTEGER, -- nullable boolean
    has_elevator INTEGER, -- nullable boolean
    pets_allowed INTEGER, -- nullable boolean
    min_build_year INTEGER,
    max_build_year INTEGER,
    exclude_keywords TEXT, -- JSON array
    search_url TEXT,
    active INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Listings store found apartments
CREATE TABLE IF NOT EXISTS listings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    is24_id TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    url TEXT NOT NULL,
    address TEXT,
    city TEXT,
    district TEXT,
    postal_code TEXT,
    price INTEGER,
    price_per_sqm REAL,
    rooms REAL,
    area INTEGER,
    has_balcony INTEGER NOT NULL DEFAULT 0,
    has_ebk INTEGER NOT NULL DEFAULT 0,
    has_elevator INTEGER NOT NULL DEFAULT 0,
    pets_allowed INTEGER,
    build_year INTEGER,
    available_from TEXT,
    description TEXT,
    landlord_name TEXT,
    landlord_type TEXT,
    image_urls TEXT, -- JSON array
    contact_form_url TEXT,
    search_profile_id INTEGER,
    contacted INTEGER NOT NULL DEFAULT 0,
    notified INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (search_profile_id) REFERENCES search_profiles(id)
);

CREATE INDEX IF NOT EXISTS idx_listings_is24_id ON listings(is24_id);
CREATE INDEX IF NOT EXISTS idx_listings_search_profile ON listings(search_profile_id);
CREATE INDEX IF NOT EXISTS idx_listings_contacted ON listings(contacted);
CREATE INDEX IF NOT EXISTS idx_listings_notified ON listings(notified);

-- Sent messages track contact attempts for deduplication
CREATE TABLE IF NOT EXISTS sent_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    listing_id INTEGER NOT NULL,
    is24_id TEXT NOT NULL,
    message TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    error_msg TEXT,
    sent_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (listing_id) REFERENCES listings(id)
);

CREATE INDEX IF NOT EXISTS idx_sent_messages_listing ON sent_messages(listing_id);
CREATE INDEX IF NOT EXISTS idx_sent_messages_is24_id ON sent_messages(is24_id);
CREATE INDEX IF NOT EXISTS idx_sent_messages_status ON sent_messages(status);

-- Sessions store IS24 authentication cookies
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL DEFAULT 'default',
    cookies TEXT NOT NULL,
    user_agent TEXT,
    valid INTEGER NOT NULL DEFAULT 1,
    expires_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Activity log for debugging and audit
CREATE TABLE IF NOT EXISTS activity_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    action TEXT NOT NULL,
    entity_type TEXT,
    entity_id INTEGER,
    details TEXT,
    error_msg TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_activity_log_action ON activity_log(action);
CREATE INDEX IF NOT EXISTS idx_activity_log_entity ON activity_log(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_activity_log_created ON activity_log(created_at);
