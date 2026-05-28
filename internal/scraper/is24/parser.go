package is24

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/julianbeese/immo_bot/internal/domain"
)

// Parser extracts listing data from IS24 HTML pages
type Parser struct {
	jsonRe       *regexp.Regexp
	priceRe      *regexp.Regexp
	roomsRe      *regexp.Regexp
	areaRe       *regexp.Regexp
	is24IDRe     *regexp.Regexp
	postalCodeRe *regexp.Regexp
}

// NewParser creates a new IS24 parser
func NewParser() *Parser {
	return &Parser{
		// Match JSON-LD or embedded result list JSON
		jsonRe:       regexp.MustCompile(`<script[^>]*type="application/(?:ld\+)?json"[^>]*>(.*?)</script>`),
		priceRe:      regexp.MustCompile(`(\d+(?:\.\d+)?(?:,\d+)?)\s*€`),
		roomsRe:      regexp.MustCompile(`(\d+(?:,\d+)?)\s*(?:Zimmer|Zi\.)`),
		areaRe:       regexp.MustCompile(`(\d+(?:,\d+)?)\s*m²`),
		is24IDRe:     regexp.MustCompile(`/expose/(\d+)`),
		postalCodeRe: regexp.MustCompile(`\b(\d{5})\b`),
	}
}

// ParseSearchResults extracts listings from search result HTML
func (p *Parser) ParseSearchResults(html []byte) ([]domain.Listing, error) {
	htmlStr := string(html)
	var listings []domain.Listing

	// Try to find embedded JSON data (IS24 embeds search results as JSON)
	if results := p.extractResultListJSON(htmlStr); results != nil {
		for _, result := range results {
			listing := p.resultToListing(result)
			if listing.IS24ID != "" {
				listings = append(listings, listing)
			}
		}
		return listings, nil
	}

	// Fallback: parse HTML directly
	listings = p.parseHTMLResults(htmlStr)
	return listings, nil
}

// ParseExpose extracts detailed listing data from expose page
func (p *Parser) ParseExpose(html []byte, is24ID string) (*domain.Listing, error) {
	htmlStr := string(html)

	listing := &domain.Listing{
		IS24ID: is24ID,
		URL:    baseURL + "/expose/" + is24ID,
	}

	// Try to extract from JSON-LD
	if data := p.extractJSONLD(htmlStr); data != nil {
		p.populateFromJSONLD(listing, data)
	}

	// Extract additional details from HTML
	p.extractExposeDetails(listing, htmlStr)

	return listing, nil
}

// extractResultListJSON finds and parses the IS24 search results JSON
func (p *Parser) extractResultListJSON(html string) []map[string]interface{} {
	// Look for IS24-specific data structures
	patterns := []string{
		`"resultlistEntries":\s*\[\s*\{[^}]*"@id"`,
		`resultlistEntry`,
		`searchResponseModel`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if re.MatchString(html) {
			// Found IS24 data structure, try to extract full JSON
			return p.extractResultEntries(html)
		}
	}

	return nil
}

func (p *Parser) extractResultEntries(html string) []map[string]interface{} {
	// Pattern to find result entries in IS24's embedded JavaScript
	// IS24 uses various formats, we try to handle the common ones

	// Look for resultlistEntries array
	entryPattern := regexp.MustCompile(`"resultlistEntries":\s*\[([\s\S]*?)\](?:\s*,\s*"|})`)
	if matches := entryPattern.FindStringSubmatch(html); len(matches) > 1 {
		var entries []map[string]interface{}
		// Try to parse as JSON array
		jsonStr := "[" + matches[1] + "]"
		if err := json.Unmarshal([]byte(jsonStr), &entries); err == nil {
			return entries
		}
	}

	// Try to find searchResponseModel with resultlistEntries
	searchModelPattern := regexp.MustCompile(`"searchResponseModel"\s*:\s*\{[^}]*"resultlistEntries"\s*:\s*\[([^\]]+)\]`)
	if matches := searchModelPattern.FindStringSubmatch(html); len(matches) > 1 {
		var entries []map[string]interface{}
		jsonStr := "[" + matches[1] + "]"
		if err := json.Unmarshal([]byte(jsonStr), &entries); err == nil {
			return entries
		}
	}

	// Alternative: Look for individual expose objects with realEstate nested
	exposePattern := regexp.MustCompile(`"@id"\s*:\s*"([^"]+/expose/\d+)"[^}]*?"realEstate"\s*:\s*(\{[^}]+\})`)
	matches := exposePattern.FindAllStringSubmatch(html, -1)

	var results []map[string]interface{}
	for _, match := range matches {
		if len(match) >= 3 {
			var estate map[string]interface{}
			if err := json.Unmarshal([]byte(match[2]), &estate); err == nil {
				estate["@id"] = match[1]
				results = append(results, estate)
			}
		}
	}

	// Alternative: Extract individual listing cards with their IDs and data
	if len(results) == 0 {
		cardPattern := regexp.MustCompile(`data-id="(\d+)"[^>]*>[\s\S]*?(?:price|miete)[^\d]*(\d+(?:\.\d{3})*(?:,\d+)?)\s*€`)
		cardMatches := cardPattern.FindAllStringSubmatch(html, -1)
		for _, match := range cardMatches {
			if len(match) >= 3 {
				estate := map[string]interface{}{
					"@id":   "/expose/" + match[1],
					"price": parsePrice(match[2]),
				}
				results = append(results, estate)
			}
		}
	}

	return results
}

func (p *Parser) resultToListing(result map[string]interface{}) domain.Listing {
	listing := domain.Listing{}

	// Extract IS24 ID from @id field or id field
	if id, ok := result["@id"].(string); ok {
		if matches := p.is24IDRe.FindStringSubmatch(id); len(matches) > 1 {
			listing.IS24ID = matches[1]
			listing.URL = baseURL + "/expose/" + matches[1]
		}
	}

	// Get realEstate object if nested
	realEstate := result
	if re, ok := result["realEstate"].(map[string]interface{}); ok {
		realEstate = re
	}

	// Title
	if title, ok := realEstate["title"].(string); ok {
		listing.Title = title
	}

	// Address
	if addr, ok := realEstate["address"].(map[string]interface{}); ok {
		listing.City = getString(addr, "city")
		listing.District = getString(addr, "quarter")
		listing.PostalCode = getString(addr, "postcode")

		// Build full address
		parts := []string{}
		if street := getString(addr, "street"); street != "" {
			parts = append(parts, street)
			if num := getString(addr, "houseNumber"); num != "" {
				parts[len(parts)-1] += " " + num
			}
		}
		if listing.PostalCode != "" {
			parts = append(parts, listing.PostalCode)
		}
		if listing.City != "" {
			parts = append(parts, listing.City)
		}
		listing.Address = strings.Join(parts, ", ")
	}

	// Price - try multiple possible locations
	if price, ok := realEstate["price"].(map[string]interface{}); ok {
		if value := getFloat(price, "value"); value > 0 {
			listing.Price = int(value)
		}
	}
	if listing.Price == 0 {
		if price := getFloat(realEstate, "price"); price > 0 {
			listing.Price = int(price)
		}
	}
	// Try calculatedPrice (cold rent / Kaltmiete)
	if listing.Price == 0 {
		if calcPrice, ok := realEstate["calculatedPrice"].(map[string]interface{}); ok {
			if value := getFloat(calcPrice, "value"); value > 0 {
				listing.Price = int(value)
			}
		}
	}
	// Try rentBasePrice
	if listing.Price == 0 {
		listing.Price = int(getFloat(realEstate, "rentBasePrice"))
	}
	// Try baseRent
	if listing.Price == 0 {
		listing.Price = int(getFloat(realEstate, "baseRent"))
	}
	// Try coldRent
	if listing.Price == 0 {
		listing.Price = int(getFloat(realEstate, "coldRent"))
	}

	// Rooms
	listing.Rooms = getFloat(realEstate, "numberOfRooms")

	// Living space
	listing.Area = int(getFloat(realEstate, "livingSpace"))

	// Features
	listing.HasBalcony = getBool(realEstate, "balcony")
	listing.HasEBK = getBool(realEstate, "builtInKitchen")
	listing.HasElevator = getBool(realEstate, "lift")

	// Build year
	if year := getInt(realEstate, "constructionYear"); year > 0 {
		listing.BuildYear = year
	}

	return listing
}

func (p *Parser) parseHTMLResults(html string) []domain.Listing {
	var listings []domain.Listing

	// Find all expose links
	linkPattern := regexp.MustCompile(`<a[^>]*href="(/expose/(\d+))"[^>]*>`)
	matches := linkPattern.FindAllStringSubmatch(html, -1)

	seen := make(map[string]bool)
	for _, match := range matches {
		if len(match) >= 3 {
			is24ID := match[2]
			if seen[is24ID] {
				continue
			}
			seen[is24ID] = true

			listing := domain.Listing{
				IS24ID: is24ID,
				URL:    baseURL + match[1],
			}

			// Try to extract basic info from surrounding HTML
			// This is a simplified fallback
			listings = append(listings, listing)
		}
	}

	return listings
}

func (p *Parser) extractJSONLD(html string) map[string]interface{} {
	matches := p.jsonRe.FindAllStringSubmatch(html, -1)
	for _, match := range matches {
		if len(match) >= 2 {
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(match[1]), &data); err == nil {
				// Check if it's an Apartment/RealEstateListing type
				if typ, ok := data["@type"].(string); ok {
					if typ == "Apartment" || typ == "RealEstateListing" || typ == "Product" {
						return data
					}
				}
			}
		}
	}
	return nil
}

func (p *Parser) populateFromJSONLD(listing *domain.Listing, data map[string]interface{}) {
	if name, ok := data["name"].(string); ok {
		listing.Title = name
	}
	if desc, ok := data["description"].(string); ok {
		listing.Description = desc
	}

	// Address from JSON-LD
	if addr, ok := data["address"].(map[string]interface{}); ok {
		if locality, ok := addr["addressLocality"].(string); ok {
			listing.City = locality
		}
		if postal, ok := addr["postalCode"].(string); ok {
			listing.PostalCode = postal
		}
		if street, ok := addr["streetAddress"].(string); ok {
			listing.Address = street
		}
	}

	// Offers for price
	if offers, ok := data["offers"].(map[string]interface{}); ok {
		if price := getFloat(offers, "price"); price > 0 {
			listing.Price = int(price)
		}
	}

	// Ansprechpartner via JSON-LD realEstateAgent (preferred when present).
	if agent, ok := data["realEstateAgent"].(map[string]interface{}); ok {
		if name := strings.TrimSpace(getString(agent, "name")); name != "" {
			listing.ContactPerson = name
		}
	}
}

func (p *Parser) extractExposeDetails(listing *domain.Listing, html string) {
	// Extract title if not set
	if listing.Title == "" {
		titlePattern := regexp.MustCompile(`<h1[^>]*id="expose-title"[^>]*>([^<]+)</h1>`)
		if matches := titlePattern.FindStringSubmatch(html); len(matches) > 1 {
			listing.Title = strings.TrimSpace(matches[1])
		}
	}

	// Extract price - try multiple patterns
	if listing.Price == 0 {
		pricePatterns := []*regexp.Regexp{
			regexp.MustCompile(`<div[^>]*class="[^"]*is24qa-kaltmiete[^"]*"[^>]*>([^<]+)</div>`),
			regexp.MustCompile(`<span[^>]*class="[^"]*is24qa-kaltmiete[^"]*"[^>]*>([^<]+)</span>`),
			regexp.MustCompile(`<dd[^>]*class="[^"]*is24qa-kaltmiete[^"]*"[^>]*>([^<]+)</dd>`),
			regexp.MustCompile(`(?i)kaltmiete[^<]*?(\d+(?:\.\d{3})*(?:,\d+)?)\s*€`),
			regexp.MustCompile(`(?i)miete[^<]*?(\d+(?:\.\d{3})*(?:,\d+)?)\s*€`),
			regexp.MustCompile(`"rentBasePrice"\s*:\s*(\d+(?:\.\d+)?)`),
			regexp.MustCompile(`"baseRent"\s*:\s*(\d+(?:\.\d+)?)`),
			regexp.MustCompile(`"coldRent"\s*:\s*(\d+(?:\.\d+)?)`),
			regexp.MustCompile(`"price"\s*:\s*\{\s*"value"\s*:\s*(\d+(?:\.\d+)?)`),
		}
		for _, pattern := range pricePatterns {
			if matches := pattern.FindStringSubmatch(html); len(matches) > 1 {
				if price := parsePrice(matches[1]); price > 0 {
					listing.Price = price
					break
				}
			}
		}
	}

	// Extract rooms
	if listing.Rooms == 0 {
		roomsPattern := regexp.MustCompile(`<div[^>]*class="[^"]*is24qa-zi[^"]*"[^>]*>([^<]+)</div>`)
		if matches := roomsPattern.FindStringSubmatch(html); len(matches) > 1 {
			listing.Rooms = parseRooms(matches[1])
		}
	}

	// Extract area
	if listing.Area == 0 {
		areaPattern := regexp.MustCompile(`<div[^>]*class="[^"]*is24qa-wohnflaeche[^"]*"[^>]*>([^<]+)</div>`)
		if matches := areaPattern.FindStringSubmatch(html); len(matches) > 1 {
			listing.Area = parseArea(matches[1])
		}
	}

	// Extract features from criteria list
	if strings.Contains(html, "is24qa-balkon-terrasse-ja") ||
	   strings.Contains(strings.ToLower(html), "balkon: ja") {
		listing.HasBalcony = true
	}
	if strings.Contains(html, "is24qa-einbaukueche-ja") ||
	   strings.Contains(strings.ToLower(html), "einbauküche: ja") {
		listing.HasEBK = true
	}
	if strings.Contains(html, "is24qa-personenaufzug-ja") ||
	   strings.Contains(strings.ToLower(html), "aufzug: ja") {
		listing.HasElevator = true
	}

	// Landlord (agency) name. The realtor-title class typically holds the
	// company / agency name, not the personal Ansprechpartner.
	landlordPattern := regexp.MustCompile(`<span[^>]*class="[^"]*realtor-title[^"]*"[^>]*>([^<]+)</span>`)
	if matches := landlordPattern.FindStringSubmatch(html); len(matches) > 1 {
		listing.LandlordName = strings.TrimSpace(matches[1])
	}

	// Ansprechpartner (contact person) - IS24 exposes this in several places
	// depending on the listing type. Try each pattern in order; the first hit
	// that yields a plausible person name (two+ tokens) wins.
	if listing.ContactPerson == "" {
		listing.ContactPerson = extractContactPerson(html)
	}

	// Contact form URL
	contactPattern := regexp.MustCompile(`href="([^"]*kontaktformular[^"]*)"`)
	if matches := contactPattern.FindStringSubmatch(html); len(matches) > 1 {
		listing.ContactFormURL = matches[1]
		if !strings.HasPrefix(listing.ContactFormURL, "http") {
			listing.ContactFormURL = baseURL + listing.ContactFormURL
		}
	}
}

// contactPersonPatterns lists the HTML/JS patterns we try (in order) to pull
// the Ansprechpartner out of an IS24 expose. IS24 ships several layouts; the
// first match that looks like a real person name wins. Ordering matters:
// specific IS24-prefixed classes come before generic JSON keys.
var contactPersonPatterns = []*regexp.Regexp{
	regexp.MustCompile(`<p[^>]*class="[^"]*is24qa-contact-name[^"]*"[^>]*>([^<]+)</p>`),
	regexp.MustCompile(`<span[^>]*class="[^"]*is24qa-contact-name[^"]*"[^>]*>([^<]+)</span>`),
	regexp.MustCompile(`<div[^>]*class="[^"]*is24qa-contact-name[^"]*"[^>]*>([^<]+)</div>`),
	regexp.MustCompile(`<[^>]*data-qa="contactName"[^>]*>([^<]+)<`),
	regexp.MustCompile(`<[^>]*data-qa="contact-name"[^>]*>([^<]+)<`),
	regexp.MustCompile(`<p[^>]*class="[^"]*is24qa-contact-name-anbieter[^"]*"[^>]*>([^<]+)</p>`),
	regexp.MustCompile(`"contactName"\s*:\s*"([^"]+)"`),
	regexp.MustCompile(`"contactPerson"\s*:\s*"([^"]+)"`),
	regexp.MustCompile(`"firstname"\s*:\s*"([^"]+)"\s*,\s*"lastname"\s*:\s*"([^"]+)"`),
	regexp.MustCompile(`(?i)Ansprechpartner[^<]*<[^>]*>\s*([A-ZÄÖÜ][\w\-\.]+(?:\s+[A-ZÄÖÜ][\w\-\.]+)+)`),
}

// extractContactPerson scans the expose HTML for a plausible Ansprechpartner
// name. Returns "" if no pattern matched a name that looks like a person
// (i.e. has at least two whitespace-separated tokens with leading capitals).
func extractContactPerson(html string) string {
	for _, re := range contactPersonPatterns {
		matches := re.FindStringSubmatch(html)
		if len(matches) < 2 {
			continue
		}
		name := strings.TrimSpace(matches[1])
		// Special-case the first/last regex variant.
		if len(matches) >= 3 && matches[2] != "" {
			name = strings.TrimSpace(matches[1]) + " " + strings.TrimSpace(matches[2])
		}
		name = strings.Join(strings.Fields(name), " ")
		if isPlausiblePersonName(name) {
			return name
		}
	}
	return ""
}

// isPlausiblePersonName filters out company names and other false positives.
// Requires two+ whitespace-separated tokens, none of which look like company
// suffixes (GmbH, AG, KG, etc.).
func isPlausiblePersonName(name string) bool {
	if name == "" {
		return false
	}
	tokens := strings.Fields(name)
	if len(tokens) < 2 {
		return false
	}
	lower := strings.ToLower(name)
	companyMarkers := []string{
		"gmbh", "ag", " kg", "ohg", "ug ", "ug,",
		"e.k.", "e.k ", "immobilien", "immobilie",
		"makler", "verwaltung", "hausverwaltung",
		"genossenschaft", "wohnungsbau", "real estate",
		"& co", "und partner", "u. partner",
	}
	for _, marker := range companyMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

// Helper functions

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getFloat(m map[string]interface{}, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		f, _ := strconv.ParseFloat(strings.Replace(v, ",", ".", 1), 64)
		return f
	}
	return 0
}

func getInt(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		i, _ := strconv.Atoi(v)
		return i
	}
	return 0
}

func getBool(m map[string]interface{}, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "yes" || v == "ja"
	}
	return false
}

func parsePrice(s string) int {
	// Remove non-numeric chars except dots and commas
	cleaned := regexp.MustCompile(`[^\d,.]`).ReplaceAllString(s, "")
	// Handle German number format (1.234,56)
	cleaned = strings.Replace(cleaned, ".", "", -1)
	cleaned = strings.Replace(cleaned, ",", ".", 1)
	f, _ := strconv.ParseFloat(cleaned, 64)
	return int(f)
}

func parseRooms(s string) float64 {
	cleaned := regexp.MustCompile(`[^\d,.]`).ReplaceAllString(s, "")
	cleaned = strings.Replace(cleaned, ",", ".", 1)
	f, _ := strconv.ParseFloat(cleaned, 64)
	return f
}

func parseArea(s string) int {
	cleaned := regexp.MustCompile(`[^\d,.]`).ReplaceAllString(s, "")
	cleaned = strings.Replace(cleaned, ",", ".", 1)
	f, _ := strconv.ParseFloat(cleaned, 64)
	return int(f)
}
