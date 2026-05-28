package messenger

import (
	"strings"
	"testing"

	"github.com/julianbeese/immo_bot/internal/domain"
)

func TestNewGeneratorFromText(t *testing.T) {
	g, err := NewGeneratorFromText(`Betreff: {{.Title}} in {{.City}}. {{.PersonalizedDetails}}`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.Generate(&domain.Listing{Title: "3-Zi-Whg", City: "Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "3-Zi-Whg") || !strings.Contains(out, "Berlin") {
		t.Errorf("template not rendered: %q", out)
	}
	// Placeholder is preserved for the enhancer to replace later.
	if !strings.Contains(out, "{{.PersonalizedDetails}}") {
		t.Errorf("PersonalizedDetails placeholder should remain, got %q", out)
	}
}

func TestNewGeneratorFromTextEmptyUsesDefault(t *testing.T) {
	g, err := NewGeneratorFromText("")
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.Generate(&domain.Listing{City: "Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Selbstauskunft") {
		t.Errorf("empty text should fall back to default template, got %q", out)
	}
}

func TestNewGeneratorMissingTemplateReturnsError(t *testing.T) {
	if _, err := NewGenerator("does-not-exist.txt", "", "", ""); err == nil {
		t.Error("missing template should return an error")
	}
}

func TestNewGeneratorFromTextInvalid(t *testing.T) {
	if _, err := NewGeneratorFromText("{{.Unclosed"); err == nil {
		t.Error("invalid template should return an error")
	}
}

func TestBuildSalutation(t *testing.T) {
	const fallback = "Sehr geehrte Damen und Herren,"
	cases := []struct {
		name, person, gender, want string
	}{
		{"male with surname", "Max Mustermann", domain.SalutationMale, "Sehr geehrter Herr Mustermann,"},
		{"female with surname", "Anna Schmidt", domain.SalutationFemale, "Sehr geehrte Frau Schmidt,"},
		{"with title", "Dr. Müller", domain.SalutationMale, fallback}, // single token after stripping title
		{"with title two parts", "Dr. Hans Müller", domain.SalutationMale, "Sehr geehrter Herr Müller,"},
		{"unknown gender", "Kim Lee", domain.SalutationUnknown, fallback},
		{"empty person", "", domain.SalutationMale, fallback},
		{"empty gender", "Max Mustermann", "", fallback},
		{"single token", "Mustermann", domain.SalutationMale, fallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildSalutation(tc.person, tc.gender)
			if got != tc.want {
				t.Errorf("BuildSalutation(%q, %q) = %q, want %q", tc.person, tc.gender, got, tc.want)
			}
		})
	}
}

func TestGenerateUsesSalutationFromListing(t *testing.T) {
	g, err := NewGeneratorFromText("")
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.Generate(&domain.Listing{
		City:              "Berlin",
		ContactPerson:     "Anna Schmidt",
		ContactSalutation: domain.SalutationFemale,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Sehr geehrte Frau Schmidt,") {
		t.Errorf("expected personalized salutation in default template, got %q", out)
	}
}
