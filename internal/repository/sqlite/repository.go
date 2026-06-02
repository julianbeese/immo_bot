package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/julianbeese/immo_bot/internal/domain"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// MetaLastPollOK is the meta key holding the RFC3339 timestamp of the last
// successful poll cycle; used by the container health check.
const MetaLastPollOK = "last_poll_ok"

// MetaIS24Cookie is the meta key holding a hot-reloaded IS24 cookie override.
// If set (non-empty), it takes precedence over IS24_COOKIE at startup; updates
// happen via the dashboard or the /cookie chat command.
const MetaIS24Cookie = "is24.cookie"

// CampaignPromptKey / CampaignTemplateKey are the meta-table keys under which
// dashboard-edited per-campaign overrides (AI system prompt, message template)
// are persisted. Shared by the scheduler (reads at send time) and the web
// dashboard (writes on save).
func CampaignPromptKey(name string) string   { return "campaign." + name + ".ai_prompt" }
func CampaignTemplateKey(name string) string { return "campaign." + name + ".template" }

// Repository provides database access for all entities
type Repository struct {
	db *sql.DB
}

// New creates a new SQLite repository and runs migrations
func New(dbPath string) (*Repository, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." {
		// Directory creation handled by caller
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Enable foreign keys and WAL mode
	if _, err := db.Exec("PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;"); err != nil {
		return nil, fmt.Errorf("enable pragmas: %w", err)
	}

	repo := &Repository{db: db}
	if err := repo.migrate(); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return repo, nil
}

// Close closes the database connection
func (r *Repository) Close() error {
	return r.db.Close()
}

// DB returns the underlying database connection for custom queries
func (r *Repository) DB() *sql.DB {
	return r.db
}

func (r *Repository) migrate() error {
	// Track applied migrations so additive schema changes (002+) run exactly once.
	if _, err := r.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files) // run in filename order: 001_, 002_, ...

	for _, name := range files {
		var applied int
		if err := r.db.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, name,
		).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}

		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := r.db.Exec(string(sqlBytes)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := r.db.Exec(
			`INSERT INTO schema_migrations (filename) VALUES (?)`, name,
		); err != nil {
			return err
		}
	}
	return nil
}

// SearchProfile methods

// CreateSearchProfile inserts a new search profile
func (r *Repository) CreateSearchProfile(ctx context.Context, sp *domain.SearchProfile) error {
	districts, _ := json.Marshal(sp.Districts)
	postalCodes, _ := json.Marshal(sp.PostalCodes)
	excludeKeywords, _ := json.Marshal(sp.ExcludeKeywords)

	result, err := r.db.ExecContext(ctx, `
		INSERT INTO search_profiles (
			name, city, districts, postal_codes, min_price, max_price,
			min_rooms, max_rooms, min_area, max_area, has_balcony, has_ebk,
			has_elevator, pets_allowed, min_build_year, max_build_year,
			exclude_keywords, search_url, category, active
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sp.Name, sp.City, string(districts), string(postalCodes),
		nullableInt(sp.MinPrice), nullableInt(sp.MaxPrice),
		nullableFloat(sp.MinRooms), nullableFloat(sp.MaxRooms),
		nullableInt(sp.MinArea), nullableInt(sp.MaxArea),
		nullableBool(sp.HasBalcony), nullableBool(sp.HasEBK),
		nullableBool(sp.HasElevator), nullableBool(sp.PetsAllowed),
		nullableInt(sp.MinBuildYear), nullableInt(sp.MaxBuildYear),
		string(excludeKeywords), sp.SearchURL, nullableString(sp.Category), sp.Active,
	)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	sp.ID = id
	sp.CreatedAt = time.Now()
	sp.UpdatedAt = time.Now()
	return nil
}

// GetActiveSearchProfiles returns all active search profiles
func (r *Repository) GetActiveSearchProfiles(ctx context.Context) ([]domain.SearchProfile, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, city, districts, postal_codes, min_price, max_price,
			min_rooms, max_rooms, min_area, max_area, has_balcony, has_ebk,
			has_elevator, pets_allowed, min_build_year, max_build_year,
			exclude_keywords, search_url, category, active, created_at, updated_at
		FROM search_profiles WHERE active = 1
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []domain.SearchProfile
	for rows.Next() {
		sp, err := scanSearchProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, *sp)
	}
	return profiles, rows.Err()
}

// ListAllSearchProfiles returns all search profiles (active and inactive).
func (r *Repository) ListAllSearchProfiles(ctx context.Context) ([]domain.SearchProfile, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, city, districts, postal_codes, min_price, max_price,
			min_rooms, max_rooms, min_area, max_area, has_balcony, has_ebk,
			has_elevator, pets_allowed, min_build_year, max_build_year,
			exclude_keywords, search_url, category, active, created_at, updated_at
		FROM search_profiles ORDER BY active DESC, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []domain.SearchProfile
	for rows.Next() {
		sp, err := scanSearchProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, *sp)
	}
	return profiles, rows.Err()
}

// DeleteSearchProfile permanently removes a search profile by ID. Existing
// listings are kept but detached (search_profile_id set NULL) to satisfy the
// foreign key; they fall back to the default campaign in the dashboard.
func (r *Repository) DeleteSearchProfile(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE listings SET search_profile_id = NULL WHERE search_profile_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM search_profiles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no search profile with id %d", id)
	}
	return tx.Commit()
}

// GetSearchProfileByID returns a single search profile (active or not) by ID.
func (r *Repository) GetSearchProfileByID(ctx context.Context, id int64) (*domain.SearchProfile, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, city, districts, postal_codes, min_price, max_price,
			min_rooms, max_rooms, min_area, max_area, has_balcony, has_ebk,
			has_elevator, pets_allowed, min_build_year, max_build_year,
			exclude_keywords, search_url, category, active, created_at, updated_at
		FROM search_profiles WHERE id = ?
	`, id)
	return scanSearchProfile(row)
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

// scanSearchProfile scans one search_profiles row (column order must match the
// SELECTs above) into a domain.SearchProfile.
func scanSearchProfile(s rowScanner) (*domain.SearchProfile, error) {
	var sp domain.SearchProfile
	var districts, postalCodes, excludeKeywords, searchURL, category sql.NullString
	var hasBalcony, hasEBK, hasElevator, petsAllowed sql.NullBool
	var minPrice, maxPrice, minArea, maxArea, minBuildYear, maxBuildYear sql.NullInt64
	var minRooms, maxRooms sql.NullFloat64

	err := s.Scan(
		&sp.ID, &sp.Name, &sp.City, &districts, &postalCodes,
		&minPrice, &maxPrice, &minRooms, &maxRooms,
		&minArea, &maxArea, &hasBalcony, &hasEBK,
		&hasElevator, &petsAllowed, &minBuildYear, &maxBuildYear,
		&excludeKeywords, &searchURL, &category, &sp.Active, &sp.CreatedAt, &sp.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	sp.MinPrice = int(minPrice.Int64)
	sp.MaxPrice = int(maxPrice.Int64)
	sp.MinRooms = minRooms.Float64
	sp.MaxRooms = maxRooms.Float64
	sp.MinArea = int(minArea.Int64)
	sp.MaxArea = int(maxArea.Int64)
	sp.MinBuildYear = int(minBuildYear.Int64)
	sp.MaxBuildYear = int(maxBuildYear.Int64)
	sp.SearchURL = searchURL.String
	sp.Category = category.String

	if districts.Valid {
		json.Unmarshal([]byte(districts.String), &sp.Districts)
	}
	if postalCodes.Valid {
		json.Unmarshal([]byte(postalCodes.String), &sp.PostalCodes)
	}
	if excludeKeywords.Valid {
		json.Unmarshal([]byte(excludeKeywords.String), &sp.ExcludeKeywords)
	}
	sp.HasBalcony = nullBoolPtr(hasBalcony)
	sp.HasEBK = nullBoolPtr(hasEBK)
	sp.HasElevator = nullBoolPtr(hasElevator)
	sp.PetsAllowed = nullBoolPtr(petsAllowed)

	return &sp, nil
}

// VacuumInto writes an atomic snapshot of the live database to the given
// path. The output is a single .db file (no WAL artefacts to copy) that any
// sqlite client can open. The target file is created or overwritten.
//
// VACUUM INTO does not support bound parameters for the destination path, so
// the path is interpolated; we quote any single quotes to avoid breaking out
// of the SQL string literal (paths under our control, but cheap to be safe).
func (r *Repository) VacuumInto(ctx context.Context, path string) error {
	quoted := strings.ReplaceAll(path, "'", "''")
	_, err := r.db.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", quoted))
	return err
}

// SetMeta upserts a key/value pair in the meta table.
func (r *Repository) SetMeta(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

// GetMeta returns the value for a key, or ("", nil) if absent.
func (r *Repository) GetMeta(ctx context.Context, key string) (string, error) {
	var value string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetSearchProfileActive enables or disables a search profile by ID.
func (r *Repository) SetSearchProfileActive(ctx context.Context, id int64, active bool) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE search_profiles SET active = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		active, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no search profile with id %d", id)
	}
	return nil
}

// Listing methods

// CreateListing inserts a new listing if it doesn't exist
func (r *Repository) CreateListing(ctx context.Context, l *domain.Listing) error {
	imageURLs, _ := json.Marshal(l.ImageURLs)

	result, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO listings (
			is24_id, title, url, address, city, district, postal_code,
			price, price_per_sqm, rooms, area, has_balcony, has_ebk,
			has_elevator, pets_allowed, build_year, available_from,
			description, landlord_name, landlord_type, image_urls,
			contact_form_url, search_profile_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		l.IS24ID, l.Title, l.URL, l.Address, l.City, l.District, l.PostalCode,
		l.Price, l.PricePerSqm, l.Rooms, l.Area, l.HasBalcony, l.HasEBK,
		l.HasElevator, nullableBool(l.PetsAllowed), nullableInt(l.BuildYear),
		l.AvailableFrom, l.Description, l.LandlordName, l.LandlordType,
		string(imageURLs), l.ContactFormURL, l.SearchProfileID,
	)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if id > 0 {
		l.ID = id
		l.CreatedAt = time.Now()
		l.UpdatedAt = time.Now()
	}
	return nil
}

// GetListingByIS24ID retrieves a listing by its IS24 ID
func (r *Repository) GetListingByIS24ID(ctx context.Context, is24ID string) (*domain.Listing, error) {
	var l domain.Listing
	var imageURLs sql.NullString
	var petsAllowed sql.NullBool

	err := r.db.QueryRowContext(ctx, `
		SELECT id, is24_id, title, url, address, city, district, postal_code,
			price, price_per_sqm, rooms, area, has_balcony, has_ebk,
			has_elevator, pets_allowed, build_year, available_from,
			description, landlord_name, landlord_type, image_urls,
			contact_form_url, search_profile_id, contacted, notified,
			created_at, updated_at
		FROM listings WHERE is24_id = ?
	`, is24ID).Scan(
		&l.ID, &l.IS24ID, &l.Title, &l.URL, &l.Address, &l.City, &l.District,
		&l.PostalCode, &l.Price, &l.PricePerSqm, &l.Rooms, &l.Area,
		&l.HasBalcony, &l.HasEBK, &l.HasElevator, &petsAllowed, &l.BuildYear,
		&l.AvailableFrom, &l.Description, &l.LandlordName, &l.LandlordType,
		&imageURLs, &l.ContactFormURL, &l.SearchProfileID, &l.Contacted,
		&l.Notified, &l.CreatedAt, &l.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if imageURLs.Valid {
		json.Unmarshal([]byte(imageURLs.String), &l.ImageURLs)
	}
	l.PetsAllowed = nullBoolPtr(petsAllowed)
	return &l, nil
}

// Inbox methods

// InboxExists reports whether a message with the given RFC822 Message-ID has
// already been stored. Empty messageID is treated as not-existing so such mails
// are always processed (and may duplicate — rare, since most mails carry one).
func (r *Repository) InboxExists(ctx context.Context, messageID string) (bool, error) {
	if messageID == "" {
		return false, nil
	}
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM inbox_messages WHERE message_id = ?`, messageID).Scan(&n)
	return n > 0, err
}

// CreateInboxMessage inserts a classified inbox message, ignoring duplicates by
// message_id. On insert the row id and CreatedAt are populated.
func (r *Repository) CreateInboxMessage(ctx context.Context, m *domain.InboxMessage) error {
	var listingID any
	if m.ListingID > 0 {
		listingID = m.ListingID
	}
	res, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO inbox_messages (
			message_id, from_addr, subject, snippet, is24_id, listing_id,
			is_landlord_reply, summary, notified, received_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		m.MessageID, m.FromAddr, m.Subject, m.Snippet, m.IS24ID, listingID,
		m.IsLandlordReply, m.Summary, m.Notified, m.ReceivedAt,
	)
	if err != nil {
		return err
	}
	if id, err := res.LastInsertId(); err == nil && id > 0 {
		m.ID = id
		m.CreatedAt = time.Now()
	}
	return nil
}

// ListInboxMessages returns the most recent inbox messages for the dashboard.
// When landlordOnly is true, only genuine provider replies are returned.
func (r *Repository) ListInboxMessages(ctx context.Context, limit int, landlordOnly bool) ([]domain.InboxMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	where := "1 = 1"
	if landlordOnly {
		where = "is_landlord_reply = 1"
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, COALESCE(message_id, ''), from_addr, subject, snippet, is24_id,
			COALESCE(listing_id, 0), is_landlord_reply, summary, notified,
			received_at, created_at
		FROM inbox_messages
		WHERE %s
		ORDER BY received_at DESC, id DESC
		LIMIT %d
	`, where, limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.InboxMessage
	for rows.Next() {
		var m domain.InboxMessage
		var receivedAt sql.NullTime
		if err := rows.Scan(
			&m.ID, &m.MessageID, &m.FromAddr, &m.Subject, &m.Snippet, &m.IS24ID,
			&m.ListingID, &m.IsLandlordReply, &m.Summary, &m.Notified,
			&receivedAt, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		if receivedAt.Valid {
			m.ReceivedAt = receivedAt.Time
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetUnnotifiedListings returns listings that haven't been notified
func (r *Repository) GetUnnotifiedListings(ctx context.Context) ([]domain.Listing, error) {
	return r.getListingsByCondition(ctx, "notified = 0", "")
}

// GetUncontactedListings returns listings that haven't been contacted
func (r *Repository) GetUncontactedListings(ctx context.Context) ([]domain.Listing, error) {
	return r.getListingsByCondition(ctx, "contacted = 0 AND notified = 1", "")
}

// ListRecentListings returns the most recent listings (for the dashboard).
func (r *Repository) ListRecentListings(ctx context.Context, limit int) ([]domain.Listing, error) {
	if limit <= 0 {
		limit = 100
	}
	return r.getListingsByCondition(ctx, "1 = 1", fmt.Sprintf("LIMIT %d", limit))
}

// GetPreviewableListings returns uncontacted listings that have not already
// received a test-mode preview.
func (r *Repository) GetPreviewableListings(ctx context.Context) ([]domain.Listing, error) {
	return r.getListingsByCondition(ctx, `
		contacted = 0
		AND notified = 1
		AND NOT EXISTS (
			SELECT 1 FROM sent_messages
			WHERE sent_messages.listing_id = listings.id
			AND sent_messages.status = 'preview'
		)
	`, "")
}

func (r *Repository) getListingsByCondition(ctx context.Context, condition, suffix string) ([]domain.Listing, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, is24_id, title, url, address, city, district, postal_code,
			price, price_per_sqm, rooms, area, has_balcony, has_ebk,
			has_elevator, pets_allowed, build_year, available_from,
			description, landlord_name, landlord_type, image_urls,
			contact_form_url, search_profile_id, contacted, notified,
			created_at, updated_at
		FROM listings WHERE %s ORDER BY created_at DESC %s
	`, condition, suffix))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var listings []domain.Listing
	for rows.Next() {
		var l domain.Listing
		var imageURLs, address, city, district, postalCode, availableFrom, description sql.NullString
		var landlordName, landlordType, contactFormURL sql.NullString
		var petsAllowed sql.NullBool
		var buildYear sql.NullInt64
		var pricePerSqm sql.NullFloat64

		err := rows.Scan(
			&l.ID, &l.IS24ID, &l.Title, &l.URL, &address, &city, &district,
			&postalCode, &l.Price, &pricePerSqm, &l.Rooms, &l.Area,
			&l.HasBalcony, &l.HasEBK, &l.HasElevator, &petsAllowed, &buildYear,
			&availableFrom, &description, &landlordName, &landlordType,
			&imageURLs, &contactFormURL, &l.SearchProfileID, &l.Contacted,
			&l.Notified, &l.CreatedAt, &l.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		l.Address = address.String
		l.City = city.String
		l.District = district.String
		l.PostalCode = postalCode.String
		l.PricePerSqm = pricePerSqm.Float64
		l.BuildYear = int(buildYear.Int64)
		l.AvailableFrom = availableFrom.String
		l.Description = description.String
		l.LandlordName = landlordName.String
		l.LandlordType = landlordType.String
		l.ContactFormURL = contactFormURL.String
		if imageURLs.Valid {
			json.Unmarshal([]byte(imageURLs.String), &l.ImageURLs)
		}
		l.PetsAllowed = nullBoolPtr(petsAllowed)
		listings = append(listings, l)
	}
	return listings, rows.Err()
}

// MarkListingNotified marks a listing as notified
func (r *Repository) MarkListingNotified(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE listings SET notified = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?
	`, id)
	return err
}

// MarkListingContacted marks a listing as contacted
func (r *Repository) MarkListingContacted(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE listings SET contacted = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?
	`, id)
	return err
}

// ListingExists checks if a listing with the given IS24 ID exists
func (r *Repository) ListingExists(ctx context.Context, is24ID string) (bool, error) {
	var exists int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM listings WHERE is24_id = ?`, is24ID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// SentMessage methods

// CreateSentMessage records a sent contact message
func (r *Repository) CreateSentMessage(ctx context.Context, sm *domain.SentMessage) error {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO sent_messages (listing_id, is24_id, message, status, error_msg, sent_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sm.ListingID, sm.IS24ID, sm.Message, sm.Status, sm.ErrorMsg, sm.SentAt)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	sm.ID = id
	sm.CreatedAt = time.Now()
	return nil
}

// UpdateSentMessageStatus updates the status of a sent message
func (r *Repository) UpdateSentMessageStatus(ctx context.Context, id int64, status, errorMsg string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE sent_messages SET status = ?, error_msg = ?, sent_at = CURRENT_TIMESTAMP WHERE id = ?
	`, status, errorMsg, id)
	return err
}

// Session methods

// GetValidSession returns a valid session
func (r *Repository) GetValidSession(ctx context.Context) (*domain.Session, error) {
	var s domain.Session
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, cookies, user_agent, valid, expires_at, created_at, updated_at
		FROM sessions WHERE valid = 1 ORDER BY updated_at DESC LIMIT 1
	`).Scan(&s.ID, &s.Name, &s.Cookies, &s.UserAgent, &s.Valid, &s.ExpiresAt, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &s, err
}

// SaveSession creates or updates a session
func (r *Repository) SaveSession(ctx context.Context, s *domain.Session) error {
	if s.ID == 0 {
		result, err := r.db.ExecContext(ctx, `
			INSERT INTO sessions (name, cookies, user_agent, valid, expires_at)
			VALUES (?, ?, ?, ?, ?)
		`, s.Name, s.Cookies, s.UserAgent, s.Valid, s.ExpiresAt)
		if err != nil {
			return err
		}
		id, _ := result.LastInsertId()
		s.ID = id
		return nil
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE sessions SET cookies = ?, user_agent = ?, valid = ?, expires_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, s.Cookies, s.UserAgent, s.Valid, s.ExpiresAt, s.ID)
	return err
}

// ActivityLog methods

// LogActivity records an activity
func (r *Repository) LogActivity(ctx context.Context, log *domain.ActivityLog) error {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO activity_log (action, entity_type, entity_id, details, error_msg)
		VALUES (?, ?, ?, ?, ?)
	`, log.Action, log.EntityType, log.EntityID, log.Details, log.ErrorMsg)
	if err != nil {
		return err
	}

	id, _ := result.LastInsertId()
	log.ID = id
	log.CreatedAt = time.Now()
	return nil
}

// Helper functions

func nullableInt(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func nullableFloat(v float64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func nullableString(v string) interface{} {
	if v == "" {
		return nil
	}
	return v
}

func nullableBool(v *bool) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

func nullBoolPtr(v sql.NullBool) *bool {
	if !v.Valid {
		return nil
	}
	return &v.Bool
}
