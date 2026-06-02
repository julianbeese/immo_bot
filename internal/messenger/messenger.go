package messenger

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/julianbeese/immo_bot/internal/domain"
)

// Generator creates contact messages from templates
type Generator struct {
	template *template.Template
}

// TemplateData contains data for message template
type TemplateData struct {
	// Listing info
	Title               string
	Address             string
	City                string
	District            string
	PostalCode          string
	Price               int
	Rooms               float64
	Area                int
	Description         string
	LandlordName        string
	ContactPerson       string // Ansprechpartner (individual), may be empty
	// Salutation is the ready-to-use opening line (e.g. "Sehr geehrter Herr
	// Müller,") derived from ContactPerson + the cached gender. Falls back to
	// "Sehr geehrte Damen und Herren," when no person/gender is known.
	Salutation          string
	PersonalizedDetails string // Filled by OpenAI enhancer
}

// NewGenerator creates a message generator from a template file path, falling
// back to the built-in default template only when no path is configured.
func NewGenerator(templatePath, _, _, _ string) (*Generator, error) {
	if templatePath == "" {
		return NewGeneratorFromText(defaultTemplate)
	}
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, fmt.Errorf("read message template %q: %w", templatePath, err)
	}
	return NewGeneratorFromText(string(content))
}

// NewGeneratorFromText creates a message generator from raw template text.
// Empty text falls back to the built-in default template.
func NewGeneratorFromText(text string) (*Generator, error) {
	if text == "" {
		text = defaultTemplate
	}
	tmpl, err := template.New("message").Parse(text)
	if err != nil {
		return nil, err
	}
	return &Generator{template: tmpl}, nil
}

// DefaultTemplate returns the built-in fallback message template text. The
// dashboard shows it as a baseline when a campaign has no template override.
func DefaultTemplate() string { return defaultTemplate }

// Generate creates a message for a listing (without personalization)
func (g *Generator) Generate(listing *domain.Listing) (string, error) {
	data := TemplateData{
		Title:               sanitizeTitle(listing.Title),
		Address:             listing.Address,
		City:                listing.City,
		District:            listing.District,
		PostalCode:          listing.PostalCode,
		Price:               listing.Price,
		Rooms:               listing.Rooms,
		Area:                listing.Area,
		Description:         listing.Description,
		LandlordName:        listing.LandlordName,
		ContactPerson:       listing.ContactPerson,
		Salutation:          BuildSalutation(listing.ContactPerson, listing.ContactSalutation),
		PersonalizedDetails: "{{.PersonalizedDetails}}", // Placeholder for enhancer
	}

	var buf bytes.Buffer
	if err := g.template.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// BuildSalutation renders the German opening line ("Sehr geehrter Herr X," /
// "Sehr geehrte Frau Y,") from the Ansprechpartner name + cached gender.
// Falls back to "Sehr geehrte Damen und Herren," whenever the gender is
// unknown or no usable surname is present.
func BuildSalutation(contactPerson, gender string) string {
	surname := lastNameOf(contactPerson)
	switch strings.ToUpper(strings.TrimSpace(gender)) {
	case domain.SalutationMale:
		if surname != "" {
			return "Sehr geehrter Herr " + surname + ","
		}
	case domain.SalutationFemale:
		if surname != "" {
			return "Sehr geehrte Frau " + surname + ","
		}
	}
	return "Sehr geehrte Damen und Herren,"
}

// lastNameOf returns the surname (last whitespace-separated token) of the
// given full name, ignoring common academic titles. Returns "" for empty
// input or single-token names where the gender salutation would be awkward.
func lastNameOf(full string) string {
	full = strings.TrimSpace(full)
	if full == "" {
		return ""
	}
	tokens := strings.Fields(full)
	// Strip common honorifics so "Dr. Müller" -> "Müller", not "Müller".
	titles := map[string]bool{
		"dr.": true, "dr": true, "prof.": true, "prof": true,
		"dipl.-ing.": true, "dipl.": true, "mag.": true,
	}
	for len(tokens) > 1 && titles[strings.ToLower(tokens[0])] {
		tokens = tokens[1:]
	}
	if len(tokens) < 2 {
		return ""
	}
	return tokens[len(tokens)-1]
}

// sanitizeTitle strips ad-attention characters (Markdown asterisks, hashtags,
// excessive whitespace) that look fine in an IS24 search card but jarring in
// a written letter. Empty input stays empty so the template's {{if .Title}}
// branch can fall back to a neutral phrase.
func sanitizeTitle(s string) string {
	if s == "" {
		return ""
	}
	// drop the most common attention-grabbers
	for _, ch := range []string{"*", "#", "★", "✓", "✔", "❗", "❣", "!!"} {
		s = strings.ReplaceAll(s, ch, "")
	}
	// collapse runs of whitespace + trim
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

const defaultTemplate = `{{.Salutation}}

ich interessiere mich sehr für Ihre angebotene Wohnung in {{if .District}}{{.District}}{{else}}{{.City}}{{end}}.

{{.PersonalizedDetails}}

Meine Selbstauskunft, Bonitätsbescheinigung und weitere Unterlagen stelle ich Ihnen gerne bereit.

Vielen Dank für Ihre Zeit.

Beste Grüße
`
