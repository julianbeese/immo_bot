package contact

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/julianbeese/immo_bot/internal/antidetect"
)

// fakeForm mimics an IS24 contact form whose field names have drifted away from
// our hard-coded selectors, so the static path would miss them.
const fakeForm = `<!doctype html><html><body>
<form data-qa="contactForm">
  <label for="anrede">Anrede</label>
  <select id="anrede" name="x_salutation">
    <option value="">Bitte wählen</option>
    <option value="MALE">Herr</option>
    <option value="FEMALE">Frau</option>
  </select>

  <label for="vn">Vorname</label>
  <input id="vn" name="x_firstName" type="text" required>

  <label for="nn">Nachname</label>
  <input id="nn" name="x_lastName" type="text" required>

  <label for="em">E-Mail-Adresse</label>
  <input id="em" name="x_email" type="email" required>

  <fieldset>
    <legend>Haustiere</legend>
    <label><input type="radio" name="x_pets" value="YES"> Ja</label>
    <label><input type="radio" name="x_pets" value="NO"> Nein</label>
  </fieldset>

  <label for="msg">Ihre Nachricht</label>
  <textarea id="msg" name="x_message"></textarea>

  <button type="submit">Senden</button>
</form>
</body></html>`

func chromeOpts(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)
	if p := chromePathForTest(); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	ctx, timeoutCancel := context.WithTimeout(browserCtx, 30*time.Second)
	return ctx, func() {
		timeoutCancel()
		browserCancel()
		allocCancel()
	}
}

func chromePathForTest() string {
	const mac = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	return mac // empty-string fallback handled by caller's append guard
}

func newTestSubmitter() *Submitter {
	return &Submitter{behavior: antidetect.NewHumanBehavior(time.Millisecond, time.Millisecond)}
}

// TestExtractFormFields verifies the DOM dump captures drifted field names,
// labels, select options and value-narrowed radio selectors.
func TestExtractFormFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(fakeForm))
	}))
	defer srv.Close()

	ctx, cancel := chromeOpts(t)
	defer cancel()

	s := newTestSubmitter()
	if err := chromedp.Run(ctx, chromedp.Navigate(srv.URL), chromedp.WaitVisible(`form`, chromedp.ByQuery)); err != nil {
		t.Fatalf("navigate: %v (chrome installed?)", err)
	}

	fields, err := s.extractFormFields(ctx)
	if err != nil {
		t.Fatalf("extractFormFields: %v", err)
	}

	bySel := map[string]FormField{}
	for _, f := range fields {
		bySel[f.Selector] = f
	}

	// Salutation select with options.
	sal, ok := bySel[`select[name="x_salutation"]`]
	if !ok {
		t.Fatalf("salutation field not extracted; got %+v", fields)
	}
	if sal.Label != "Anrede" {
		t.Errorf("salutation label = %q, want Anrede", sal.Label)
	}
	if len(sal.Options) != 3 {
		t.Errorf("salutation options = %d, want 3", len(sal.Options))
	}

	// Text field label resolved via label[for].
	if vn, ok := bySel[`input[name="x_firstName"]`]; !ok || vn.Label != "Vorname" {
		t.Errorf("firstName label = %q, want Vorname", vn.Label)
	}

	// Radio selectors narrowed by value so each option is clickable.
	if _, ok := bySel[`input[name="x_pets"][value="NO"]`]; !ok {
		t.Errorf("pets NO radio not extracted with value selector; got %+v", fields)
	}
	if _, ok := bySel[`input[name="x_pets"][value="YES"]`]; !ok {
		t.Errorf("pets YES radio not extracted with value selector")
	}

	// Textarea present.
	if _, ok := bySel[`textarea[name="x_message"]`]; !ok {
		t.Errorf("message textarea not extracted")
	}
}

// TestApplyActions checks each action kind mutates the live DOM as intended.
func TestApplyActions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(fakeForm))
	}))
	defer srv.Close()

	ctx, cancel := chromeOpts(t)
	defer cancel()

	s := newTestSubmitter()
	if err := chromedp.Run(ctx, chromedp.Navigate(srv.URL), chromedp.WaitVisible(`form`, chromedp.ByQuery)); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	actions := []FieldAction{
		{Selector: `input[name="x_firstName"]`, Value: "Max", Kind: "type"},
		{Selector: `input[name="x_email"]`, Value: "max@example.com", Kind: "type"},
		{Selector: `select[name="x_salutation"]`, Value: "MALE", Kind: "select"},
		{Selector: `input[name="x_pets"][value="NO"]`, Kind: "click"},
		{Selector: `textarea[name="x_message"]`, Value: "Hallo, Interesse an der Wohnung.", Kind: "type"},
	}
	s.applyActions(ctx, actions)

	var firstName, email, salutation, message string
	var petsNo bool
	if err := chromedp.Run(ctx,
		chromedp.Value(`input[name="x_firstName"]`, &firstName, chromedp.ByQuery),
		chromedp.Value(`input[name="x_email"]`, &email, chromedp.ByQuery),
		chromedp.Value(`select[name="x_salutation"]`, &salutation, chromedp.ByQuery),
		chromedp.Value(`textarea[name="x_message"]`, &message, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('input[name="x_pets"][value="NO"]').checked`, &petsNo),
	); err != nil {
		t.Fatalf("read back values: %v", err)
	}

	if firstName != "Max" {
		t.Errorf("firstName = %q, want Max", firstName)
	}
	if email != "max@example.com" {
		t.Errorf("email = %q, want max@example.com", email)
	}
	if salutation != "MALE" {
		t.Errorf("salutation = %q, want MALE", salutation)
	}
	if !petsNo {
		t.Errorf("pets NO radio not checked")
	}
	if message != "Hallo, Interesse an der Wohnung." {
		t.Errorf("message = %q", message)
	}
}
