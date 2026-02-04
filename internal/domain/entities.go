package domain

import "time"

// SearchProfile defines criteria for apartment search
type SearchProfile struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	City        string    `json:"city"`
	Districts   []string  `json:"districts,omitempty"`
	PostalCodes []string  `json:"postal_codes,omitempty"`
	MinPrice    int       `json:"min_price,omitempty"`
	MaxPrice    int       `json:"max_price,omitempty"`
	MinRooms    float64   `json:"min_rooms,omitempty"`
	MaxRooms    float64   `json:"max_rooms,omitempty"`
	MinArea     int       `json:"min_area,omitempty"`
	MaxArea     int       `json:"max_area,omitempty"`
	HasBalcony  *bool     `json:"has_balcony,omitempty"`
	HasEBK      *bool     `json:"has_ebk,omitempty"`
	HasElevator *bool     `json:"has_elevator,omitempty"`
	PetsAllowed *bool     `json:"pets_allowed,omitempty"`
	MinBuildYear int      `json:"min_build_year,omitempty"`
	MaxBuildYear int      `json:"max_build_year,omitempty"`
	ExcludeKeywords []string `json:"exclude_keywords,omitempty"`
	SearchURL   string    `json:"search_url,omitempty"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Listing represents an apartment listing from IS24
type Listing struct {
	ID              int64     `json:"id"`
	IS24ID          string    `json:"is24_id"`
	Title           string    `json:"title"`
	URL             string    `json:"url"`
	Address         string    `json:"address"`
	City            string    `json:"city"`
	District        string    `json:"district,omitempty"`
	PostalCode      string    `json:"postal_code,omitempty"`
	Price           int       `json:"price"`
	PricePerSqm     float64   `json:"price_per_sqm,omitempty"`
	Rooms           float64   `json:"rooms"`
	Area            int       `json:"area"`
	HasBalcony      bool      `json:"has_balcony"`
	HasEBK          bool      `json:"has_ebk"`
	HasElevator     bool      `json:"has_elevator"`
	PetsAllowed     *bool     `json:"pets_allowed,omitempty"`
	BuildYear       int       `json:"build_year,omitempty"`
	AvailableFrom   string    `json:"available_from,omitempty"`
	Description     string    `json:"description,omitempty"`
	LandlordName    string    `json:"landlord_name,omitempty"`
	LandlordType    string    `json:"landlord_type,omitempty"`
	ImageURLs       []string  `json:"image_urls,omitempty"`
	ContactFormURL  string    `json:"contact_form_url,omitempty"`
	SearchProfileID int64     `json:"search_profile_id"`
	Contacted       bool      `json:"contacted"`
	Notified        bool      `json:"notified"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// SentMessage tracks contact messages sent to avoid duplicates
type SentMessage struct {
	ID          int64     `json:"id"`
	ListingID   int64     `json:"listing_id"`
	IS24ID      string    `json:"is24_id"`
	Message     string    `json:"message"`
	Status      string    `json:"status"` // pending, sent, failed
	ErrorMsg    string    `json:"error_msg,omitempty"`
	SentAt      time.Time `json:"sent_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// Session stores IS24 authentication cookies
type Session struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Cookies   string    `json:"cookies"` // JSON encoded cookies
	UserAgent string    `json:"user_agent"`
	Valid     bool      `json:"valid"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ActivityLog for debugging and audit
type ActivityLog struct {
	ID          int64     `json:"id"`
	Action      string    `json:"action"`
	EntityType  string    `json:"entity_type,omitempty"`
	EntityID    int64     `json:"entity_id,omitempty"`
	Details     string    `json:"details,omitempty"`
	ErrorMsg    string    `json:"error_msg,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// MessageStatus constants
const (
	MessageStatusPending = "pending"
	MessageStatusSent    = "sent"
	MessageStatusFailed  = "failed"
)

// ActivityAction constants
const (
	ActionSearch         = "search"
	ActionListingFound   = "listing_found"
	ActionListingFiltered = "listing_filtered"
	ActionNotificationSent = "notification_sent"
	ActionContactSent    = "contact_sent"
	ActionContactFailed  = "contact_failed"
	ActionError          = "error"
)
