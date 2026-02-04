package filter

import (
	"strings"

	"github.com/julianbeese/immo_bot/internal/domain"
)

// Engine applies search profile filters to listings
type Engine struct{}

// NewEngine creates a new filter engine
func NewEngine() *Engine {
	return &Engine{}
}

// FilterResult contains filtering outcome for a listing
type FilterResult struct {
	Passed  bool
	Reasons []string // Reasons for filtering out
}

// Filter applies all profile filters to a listing
func (e *Engine) Filter(listing *domain.Listing, profile *domain.SearchProfile) FilterResult {
	result := FilterResult{Passed: true}

	// Apply all matchers
	matchers := []Matcher{
		&PriceMatcher{MinPrice: profile.MinPrice, MaxPrice: profile.MaxPrice},
		&RoomsMatcher{MinRooms: profile.MinRooms, MaxRooms: profile.MaxRooms},
		&AreaMatcher{MinArea: profile.MinArea, MaxArea: profile.MaxArea},
		&LocationMatcher{
			City:        profile.City,
			Districts:   profile.Districts,
			PostalCodes: profile.PostalCodes,
		},
		&AmenitiesMatcher{
			HasBalcony:  profile.HasBalcony,
			HasEBK:      profile.HasEBK,
			HasElevator: profile.HasElevator,
			PetsAllowed: profile.PetsAllowed,
		},
		&BuildYearMatcher{MinYear: profile.MinBuildYear, MaxYear: profile.MaxBuildYear},
		&KeywordExclusionMatcher{Keywords: profile.ExcludeKeywords},
	}

	for _, matcher := range matchers {
		if reason := matcher.Match(listing); reason != "" {
			result.Passed = false
			result.Reasons = append(result.Reasons, reason)
		}
	}

	return result
}

// FilterListings filters a slice of listings against a profile
func (e *Engine) FilterListings(listings []domain.Listing, profile *domain.SearchProfile) []domain.Listing {
	var filtered []domain.Listing
	for _, l := range listings {
		if e.Filter(&l, profile).Passed {
			filtered = append(filtered, l)
		}
	}
	return filtered
}

// Matcher interface for individual filter criteria
type Matcher interface {
	Match(listing *domain.Listing) string // Returns empty string if passes, reason if filtered
}

// PriceMatcher filters by price range
type PriceMatcher struct {
	MinPrice int
	MaxPrice int
}

func (m *PriceMatcher) Match(l *domain.Listing) string {
	if l.Price == 0 {
		return "" // No price info, let it pass
	}
	if m.MinPrice > 0 && l.Price < m.MinPrice {
		return "price_too_low"
	}
	if m.MaxPrice > 0 && l.Price > m.MaxPrice {
		return "price_too_high"
	}
	return ""
}

// RoomsMatcher filters by room count
type RoomsMatcher struct {
	MinRooms float64
	MaxRooms float64
}

func (m *RoomsMatcher) Match(l *domain.Listing) string {
	if l.Rooms == 0 {
		return "" // No room info, let it pass
	}
	if m.MinRooms > 0 && l.Rooms < m.MinRooms {
		return "too_few_rooms"
	}
	if m.MaxRooms > 0 && l.Rooms > m.MaxRooms {
		return "too_many_rooms"
	}
	return ""
}

// AreaMatcher filters by living space
type AreaMatcher struct {
	MinArea int
	MaxArea int
}

func (m *AreaMatcher) Match(l *domain.Listing) string {
	if l.Area == 0 {
		return "" // No area info, let it pass
	}
	if m.MinArea > 0 && l.Area < m.MinArea {
		return "area_too_small"
	}
	if m.MaxArea > 0 && l.Area > m.MaxArea {
		return "area_too_large"
	}
	return ""
}

// LocationMatcher filters by city, district, or postal code
type LocationMatcher struct {
	City        string
	Districts   []string
	PostalCodes []string
}

func (m *LocationMatcher) Match(l *domain.Listing) string {
	// City check (if specified and listing has city info)
	if m.City != "" && l.City != "" {
		if !strings.EqualFold(l.City, m.City) {
			return "wrong_city"
		}
	}

	// District check (if specified)
	if len(m.Districts) > 0 && l.District != "" {
		found := false
		for _, d := range m.Districts {
			if strings.EqualFold(l.District, d) || strings.Contains(strings.ToLower(l.District), strings.ToLower(d)) {
				found = true
				break
			}
		}
		if !found {
			return "wrong_district"
		}
	}

	// Postal code check (if specified)
	if len(m.PostalCodes) > 0 && l.PostalCode != "" {
		found := false
		for _, pc := range m.PostalCodes {
			// Support prefix matching (e.g., "10" matches "10115")
			if l.PostalCode == pc || strings.HasPrefix(l.PostalCode, pc) {
				found = true
				break
			}
		}
		if !found {
			return "wrong_postal_code"
		}
	}

	return ""
}

// AmenitiesMatcher filters by required amenities
type AmenitiesMatcher struct {
	HasBalcony  *bool
	HasEBK      *bool
	HasElevator *bool
	PetsAllowed *bool
}

func (m *AmenitiesMatcher) Match(l *domain.Listing) string {
	if m.HasBalcony != nil && *m.HasBalcony && !l.HasBalcony {
		return "no_balcony"
	}
	if m.HasEBK != nil && *m.HasEBK && !l.HasEBK {
		return "no_ebk"
	}
	if m.HasElevator != nil && *m.HasElevator && !l.HasElevator {
		return "no_elevator"
	}
	if m.PetsAllowed != nil && *m.PetsAllowed {
		if l.PetsAllowed != nil && !*l.PetsAllowed {
			return "no_pets"
		}
	}
	return ""
}

// BuildYearMatcher filters by construction year
type BuildYearMatcher struct {
	MinYear int
	MaxYear int
}

func (m *BuildYearMatcher) Match(l *domain.Listing) string {
	if l.BuildYear == 0 {
		return "" // No info, let it pass
	}
	if m.MinYear > 0 && l.BuildYear < m.MinYear {
		return "building_too_old"
	}
	if m.MaxYear > 0 && l.BuildYear > m.MaxYear {
		return "building_too_new"
	}
	return ""
}

// KeywordExclusionMatcher filters out listings containing certain keywords
type KeywordExclusionMatcher struct {
	Keywords []string
}

func (m *KeywordExclusionMatcher) Match(l *domain.Listing) string {
	if len(m.Keywords) == 0 {
		return ""
	}

	// Combine title and description for search
	text := strings.ToLower(l.Title + " " + l.Description)

	for _, keyword := range m.Keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return "excluded_keyword:" + keyword
		}
	}
	return ""
}

// PricePerSqmMatcher filters by price per square meter
type PricePerSqmMatcher struct {
	MaxPricePerSqm float64
}

func (m *PricePerSqmMatcher) Match(l *domain.Listing) string {
	if m.MaxPricePerSqm <= 0 {
		return ""
	}

	// Calculate price per sqm if not provided
	pricePerSqm := l.PricePerSqm
	if pricePerSqm == 0 && l.Price > 0 && l.Area > 0 {
		pricePerSqm = float64(l.Price) / float64(l.Area)
	}

	if pricePerSqm == 0 {
		return "" // No info, let it pass
	}

	if pricePerSqm > m.MaxPricePerSqm {
		return "price_per_sqm_too_high"
	}
	return ""
}
