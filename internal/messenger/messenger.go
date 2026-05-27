package messenger

import (
	"bytes"
	"fmt"
	"os"
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

// Generate creates a message for a listing (without personalization)
func (g *Generator) Generate(listing *domain.Listing) (string, error) {
	data := TemplateData{
		Title:               listing.Title,
		Address:             listing.Address,
		City:                listing.City,
		District:            listing.District,
		PostalCode:          listing.PostalCode,
		Price:               listing.Price,
		Rooms:               listing.Rooms,
		Area:                listing.Area,
		Description:         listing.Description,
		LandlordName:        listing.LandlordName,
		PersonalizedDetails: "{{.PersonalizedDetails}}", // Placeholder for enhancer
	}

	var buf bytes.Buffer
	if err := g.template.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

const defaultTemplate = `Sehr geehrte Damen und Herren,

ich interessiere mich sehr für Ihre angebotene Wohnung in {{if .District}}{{.District}}{{else}}{{.City}}{{end}}.

{{.PersonalizedDetails}}

Meine Selbstauskunft, Bonitätsbescheinigung und weitere Unterlagen stelle ich Ihnen gerne bereit.

Vielen Dank für Ihre Zeit.

Beste Grüße
`
