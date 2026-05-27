package telegram

import "testing"

func TestMarkupToHTML(t *testing.T) {
	cases := map[string]string{
		"plain":              "plain",
		"*bold*":             "<b>bold</b>",
		"a *b* c *d*":        "a <b>b</b> c <b>d</b>",
		"*unbalanced":        "<b>unbalanced</b>", // dangling marker is closed
		"x < y & z > w":      "x &lt; y &amp; z &gt; w",
		"*Kontakt:* ✅ aktiv": "<b>Kontakt:</b> ✅ aktiv",
	}
	for in, want := range cases {
		if got := markupToHTML(in); got != want {
			t.Errorf("markupToHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkupEscapesBeforeTags(t *testing.T) {
	// HTML in the input must be escaped; the bold tags we add must not be.
	got := markupToHTML("*<script>*")
	want := "<b>&lt;script&gt;</b>"
	if got != want {
		t.Errorf("markupToHTML = %q, want %q", got, want)
	}
}
