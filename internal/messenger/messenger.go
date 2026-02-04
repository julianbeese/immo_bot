package messenger

import (
	"bytes"
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
	Title              string
	Address            string
	City               string
	District           string
	PostalCode         string
	Price              int
	Rooms              float64
	Area               int
	Description        string
	LandlordName       string
	PersonalizedDetails string // Filled by OpenAI enhancer
}

// NewGenerator creates a new message generator
func NewGenerator(templatePath, _, _, _ string) (*Generator, error) {
	// Read template file
	content, err := os.ReadFile(templatePath)
	if err != nil {
		// Use default template if file not found
		content = []byte(defaultTemplate)
	}

	tmpl, err := template.New("message").Parse(string(content))
	if err != nil {
		return nil, err
	}

	return &Generator{
		template: tmpl,
	}, nil
}

// Generate creates a message for a listing (without personalization)
func (g *Generator) Generate(listing *domain.Listing) (string, error) {
	data := TemplateData{
		Title:       listing.Title,
		Address:     listing.Address,
		City:        listing.City,
		District:    listing.District,
		PostalCode:  listing.PostalCode,
		Price:       listing.Price,
		Rooms:       listing.Rooms,
		Area:        listing.Area,
		Description: listing.Description,
		LandlordName: listing.LandlordName,
		PersonalizedDetails: "{{.PersonalizedDetails}}", // Placeholder for enhancer
	}

	var buf bytes.Buffer
	if err := g.template.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

const defaultTemplate = `Sehr geehrte Damen und Herren,

wir sind Marie Wiegelmann und Julian Beese und interessieren uns sehr für Ihre angebotene Wohnung in {{if .District}}{{.District}}{{else}}{{.City}}{{end}}.

{{.PersonalizedDetails}}

Wir arbeiten beide in der Unternehmensberatung (Nettohaushaltseinkommen ca. 7.400€) und sind auf der Suche nach einer größeren Wohnung in zentraler Lage, in der wir uns langfristig wohlfühlen können.

Unsere Selbstauskunft, Bonitätsbescheinigung etc. finden Sie in unserem Profil.

Über eine Einladung zur Besichtigung würden wir uns sehr freuen. Gerne können Sie mich auch unter 0151 67660667 telefonisch erreichen.

Vielen Dank für Ihre Zeit.

Beste Grüße
Marie Wiegelmann & Julian Beese
`
