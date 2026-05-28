package is24

import "testing"

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
