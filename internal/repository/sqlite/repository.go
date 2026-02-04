package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/julianbeese/immo_bot/internal/domain"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

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
	// Read and execute migration file
	migration, err := migrationsFS.ReadFile("migrations/001_initial.sql")
	if err != nil {
		return err
	}
	_, err = r.db.Exec(string(migration))
	return err
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
			exclude_keywords, search_url, active
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sp.Name, sp.City, string(districts), string(postalCodes),
		nullableInt(sp.MinPrice), nullableInt(sp.MaxPrice),
		nullableFloat(sp.MinRooms), nullableFloat(sp.MaxRooms),
		nullableInt(sp.MinArea), nullableInt(sp.MaxArea),
		nullableBool(sp.HasBalcony), nullableBool(sp.HasEBK),
		nullableBool(sp.HasElevator), nullableBool(sp.PetsAllowed),
		nullableInt(sp.MinBuildYear), nullableInt(sp.MaxBuildYear),
		string(excludeKeywords), sp.SearchURL, sp.Active,
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
			exclude_keywords, search_url, active, created_at, updated_at
		FROM search_profiles WHERE active = 1
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []domain.SearchProfile
	for rows.Next() {
		var sp domain.SearchProfile
		var districts, postalCodes, excludeKeywords, searchURL sql.NullString
		var hasBalcony, hasEBK, hasElevator, petsAllowed sql.NullBool
		var minPrice, maxPrice, minArea, maxArea, minBuildYear, maxBuildYear sql.NullInt64
		var minRooms, maxRooms sql.NullFloat64

		err := rows.Scan(
			&sp.ID, &sp.Name, &sp.City, &districts, &postalCodes,
			&minPrice, &maxPrice, &minRooms, &maxRooms,
			&minArea, &maxArea, &hasBalcony, &hasEBK,
			&hasElevator, &petsAllowed, &minBuildYear, &maxBuildYear,
			&excludeKeywords, &searchURL, &sp.Active, &sp.CreatedAt, &sp.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		// Convert nullable values
		sp.MinPrice = int(minPrice.Int64)
		sp.MaxPrice = int(maxPrice.Int64)
		sp.MinRooms = minRooms.Float64
		sp.MaxRooms = maxRooms.Float64
		sp.MinArea = int(minArea.Int64)
		sp.MaxArea = int(maxArea.Int64)
		sp.MinBuildYear = int(minBuildYear.Int64)
		sp.MaxBuildYear = int(maxBuildYear.Int64)
		sp.SearchURL = searchURL.String

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

		profiles = append(profiles, sp)
	}
	return profiles, rows.Err()
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

// GetUnnotifiedListings returns listings that haven't been notified
func (r *Repository) GetUnnotifiedListings(ctx context.Context) ([]domain.Listing, error) {
	return r.getListingsByCondition(ctx, "notified = 0")
}

// GetUncontactedListings returns listings that haven't been contacted
func (r *Repository) GetUncontactedListings(ctx context.Context) ([]domain.Listing, error) {
	return r.getListingsByCondition(ctx, "contacted = 0 AND notified = 1")
}

func (r *Repository) getListingsByCondition(ctx context.Context, condition string) ([]domain.Listing, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, is24_id, title, url, address, city, district, postal_code,
			price, price_per_sqm, rooms, area, has_balcony, has_ebk,
			has_elevator, pets_allowed, build_year, available_from,
			description, landlord_name, landlord_type, image_urls,
			contact_form_url, search_profile_id, contacted, notified,
			created_at, updated_at
		FROM listings WHERE %s ORDER BY created_at DESC
	`, condition))
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
