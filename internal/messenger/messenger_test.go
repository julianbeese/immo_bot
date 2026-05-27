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
	if !strings.Contains(out, "Marie Wiegelmann") {
		t.Errorf("empty text should fall back to default template, got %q", out)
	}
}

func TestNewGeneratorFromTextInvalid(t *testing.T) {
	if _, err := NewGeneratorFromText("{{.Unclosed"); err == nil {
		t.Error("invalid template should return an error")
	}
}
