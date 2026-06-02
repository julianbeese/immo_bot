package is24

import "testing"

func TestExtractJSONLD_GraphWrapper(t *testing.T) {
	// IS24 currently wraps the listing in schema.org's @graph array. The
	// top-level object has no @type — so the legacy check (data["@type"])
	// returned nil and every listing came back blank.
	html := `<html><head>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@graph": [
    { "@type": "BreadcrumbList", "itemListElement": [] },
    { "@type": "WebPage", "name": "page", "description": "page desc" },
    {
      "@type": "RealEstateListing",
      "name": "Architektenwohnung in Schwabing",
      "offers": {"@type": "Offer", "price": "1320", "priceCurrency": "EUR"},
      "address": {"@type": "PostalAddress", "addressLocality": "München", "postalCode": "80797"}
    }
  ]
}
</script>
</head><body></body></html>`

	p := NewParser()
	data := p.extractJSONLD(html)
	if data == nil {
		t.Fatal("extractJSONLD returned nil for @graph-wrapped JSON-LD")
	}
	if typ, _ := data["@type"].(string); typ != "RealEstateListing" {
		t.Errorf("expected RealEstateListing node, got @type=%v", data["@type"])
	}
	if name, _ := data["name"].(string); name != "Architektenwohnung in Schwabing" {
		t.Errorf("expected RealEstateListing.name, got %q", name)
	}
}

func TestExtractMainCriteria(t *testing.T) {
	// Slice of a real IS24 expose page. The JSON-LD RealEstateListing node
	// doesn't carry rooms/area; mainCriteriaData does.
	html := `garbage before "mainCriteriaData":{"criteria":[` +
		`{"metadata":{"pricePerSqm":"29,46 €/m²"},"labelKey":"x","type":"PRICE","value":"1.320 €"},` +
		`{"labelKey":"x","type":"NUMBER_OF_ROOMS","value":"2"},` +
		`{"metadata":{"postfix":"m²"},"labelKey":"x","type":"LIVING_SPACE","value":"44.8"}` +
		`]} more stuff`

	p := NewParser()
	rooms, area := p.extractMainCriteria(html)
	if rooms != 2.0 {
		t.Errorf("rooms = %v, want 2", rooms)
	}
	if area != 44 {
		t.Errorf("area = %d, want 44", area)
	}
}

func TestExtractBooleanCriteria(t *testing.T) {
	html := `"booleanCriteriaData":{"criteria":[` +
		`{"hasTooltip":false,"key":"balcony"},` +
		`{"hasTooltip":false,"key":"cellar"},` +
		`{"hasTooltip":false,"key":"lift"},` +
		`{"hasTooltip":false,"key":"builtInKitchen"}` +
		`]}`

	p := NewParser()
	keys := p.extractBooleanCriteria(html)
	for _, want := range []string{"balcony", "lift", "builtInKitchen"} {
		if !keys[want] {
			t.Errorf("expected key %q present, missing", want)
		}
	}
	if keys["garden"] {
		t.Error("garden should not be present")
	}
}

func TestExtractBooleanCriteria_Empty(t *testing.T) {
	p := NewParser()
	if got := p.extractBooleanCriteria(`no criteria here`); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestExtractObjectDescription(t *testing.T) {
	// Embedded JSON string with escapes — \n, \", ü must all survive.
	html := `prefix "objectDescription":"Schöne Wohnung\nmit \"Balkon\"" suffix`
	p := NewParser()
	got := p.extractObjectDescription(html)
	want := "Schöne Wohnung\nmit \"Balkon\""
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractJSONLD_LegacyTopLevel(t *testing.T) {
	// Older pages had the listing at the top level — keep that path working.
	html := `<script type="application/ld+json">
{"@type":"Apartment","name":"Legacy listing","description":"old"}
</script>`
	p := NewParser()
	data := p.extractJSONLD(html)
	if data == nil {
		t.Fatal("extractJSONLD returned nil for top-level RealEstate JSON-LD")
	}
	if name, _ := data["name"].(string); name != "Legacy listing" {
		t.Errorf("expected legacy listing name, got %q", name)
	}
}

func TestExtractContactPerson(t *testing.T) {
	cases := []struct {
		name, html, want string
	}{
		{
			name: "is24qa-contact-name class",
			html: `<p class="is24qa-contact-name">Max Mustermann</p>`,
			want: "Max Mustermann",
		},
		{
			name: "data-qa attribute",
			html: `<div data-qa="contactName">Anna Schmidt</div>`,
			want: "Anna Schmidt",
		},
		{
			name: "JSON contactName",
			html: `{"contactName":"Julia Becker"}`,
			want: "Julia Becker",
		},
		{
			name: "firstname + lastname JSON",
			html: `{"firstname":"Peter","lastname":"Schulz"}`,
			want: "Peter Schulz",
		},
		{
			name: "rejects GmbH company name",
			html: `<p class="is24qa-contact-name">Mustermann Immobilien GmbH</p>`,
			want: "",
		},
		{
			name: "rejects single token",
			html: `<p class="is24qa-contact-name">Mustermann</p>`,
			want: "",
		},
		{
			name: "no match",
			html: `<div>nothing here</div>`,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractContactPerson(tc.html)
			if got != tc.want {
				t.Errorf("extractContactPerson() = %q, want %q", got, tc.want)
			}
		})
	}
}
