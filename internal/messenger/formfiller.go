package messenger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/julianbeese/immo_bot/internal/contact"
)

// OpenAIFormFiller is an LLM-backed contact.FieldMapper. When the static
// selector fill fails, it reads the live form fields and maps them to applicant
// data via a single GPT call — the browser-use idea, but Go-native and scoped
// to one known form (no autonomous agent loop).
type OpenAIFormFiller struct {
	apiKey string
	model  string
	url    string // overridable in tests; defaults to openAIAPIURL
	client *http.Client
}

// NewOpenAIFormFiller creates a form filler backed by the OpenAI chat API.
func NewOpenAIFormFiller(apiKey, model string) *OpenAIFormFiller {
	return &OpenAIFormFiller{
		apiKey: apiKey,
		model:  model,
		url:    openAIAPIURL,
		client: &http.Client{Timeout: 45 * time.Second},
	}
}

// MapFormFields asks the model to map each live field to a fill action.
func (f *OpenAIFormFiller) MapFormFields(ctx context.Context, fields []contact.FormField, profile contact.Profile, message string) ([]contact.FieldAction, error) {
	if f.apiKey == "" {
		return nil, fmt.Errorf("openai form filler: no api key")
	}

	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	applicantJSON, err := json.Marshal(applicantData(profile, message))
	if err != nil {
		return nil, err
	}

	userPrompt := fmt.Sprintf(
		"FORMULARFELDER (live aus dem DOM):\n%s\n\nBEWERBERDATEN:\n%s\n\n"+
			"Ordne jedem passenden Feld einen Wert zu. Antworte als JSON.",
		fieldsJSON, applicantJSON)

	request := openAIRequest{
		Model: f.model,
		Messages: []openAIMessage{
			{Role: "system", Content: formFillerSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		MaxTokens:      2000,
		Temperature:    0,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.apiKey)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI API error: %d - %s", resp.StatusCode, string(respBody))
	}

	var response openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no response from OpenAI")
	}

	var out struct {
		Actions []contact.FieldAction `json:"actions"`
	}
	if err := json.Unmarshal([]byte(response.Choices[0].Message.Content), &out); err != nil {
		return nil, fmt.Errorf("decode field actions: %w", err)
	}
	return out.Actions, nil
}

// applicantData flattens the profile (plus message) into German-labelled values
// the model maps onto form fields.
func applicantData(p contact.Profile, message string) map[string]string {
	yn := func(b bool) string {
		if b {
			return "ja"
		}
		return "nein"
	}
	return map[string]string{
		"Anrede":                  p.Salutation,
		"Vorname":                 p.FirstName,
		"Nachname":                p.LastName,
		"Voller Name":             p.FirstName + " " + p.LastName,
		"E-Mail":                  p.Email,
		"Telefon":                 p.Phone,
		"Straße":                  p.Street,
		"Hausnummer":              p.HouseNumber,
		"PLZ":                     p.PostalCode,
		"Ort":                     p.City,
		"Anzahl Erwachsene":       fmt.Sprintf("%d", p.Adults),
		"Anzahl Kinder":           fmt.Sprintf("%d", p.Children),
		"Haustiere":               yn(p.Pets),
		"Nettohaushaltseinkommen": fmt.Sprintf("%d", p.Income),
		"Einzugstermin":           p.MoveInDate,
		"Beschäftigungsstatus":    p.Employment,
		"Mietrückstände":          yn(p.RentArrears),
		"Insolvenzverfahren":      yn(p.Insolvency),
		"Raucher":                 yn(p.Smoker),
		"Gewerbliche Nutzung":     yn(p.CommercialUse),
		"Nachricht":               message,
	}
}

const formFillerSystemPrompt = `Du füllst ein deutsches Immobilien-Kontaktformular (ImmobilienScout24) aus.
Du bekommst die live aus dem DOM extrahierten Formularfelder und die Bewerberdaten.

Gib ein JSON-Objekt zurück mit genau diesem Format:
{"actions": [{"selector": "...", "value": "...", "kind": "type|select|click"}]}

Regeln:
- Verwende exakt die "selector"-Strings aus der Eingabe, unverändert.
- kind="type" für Text-, E-Mail-, Tel-, Zahl- und Textarea-Felder. "value" = einzutragender Text.
- kind="select" für <select>-Felder. "value" MUSS einer der "value"-Werte aus "options" sein (matche per "text" auf die Bewerberdaten).
- kind="click" für Radio-Buttons/Checkboxen. Wähle den Selector der Option, die zur Bewerberangabe passt (z.B. Haustiere=nein → die "Nein"-Option). "value" leer lassen.
- Das Feld für die Nachricht (textarea) bekommt den Wert "Nachricht".
- Felder, die du keiner Bewerberangabe zuordnen kannst, lässt du weg.
- Keine Felder erfinden. Antworte NUR mit dem JSON-Objekt, ohne Erklärung.`
