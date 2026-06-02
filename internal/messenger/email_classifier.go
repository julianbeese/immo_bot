package messenger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/julianbeese/immo_bot/internal/email"
)

// OpenAIEmailClassifier implements email.Classifier. It decides whether an
// IS24-related mail is a genuine reply from a provider/landlord (who answered
// by email rather than via the IS24 chat) and extracts the listing id.
type OpenAIEmailClassifier struct {
	apiKey string
	model  string
	url    string // overridable in tests; defaults to openAIAPIURL
	client *http.Client
}

// NewOpenAIEmailClassifier creates a classifier backed by the OpenAI chat API.
func NewOpenAIEmailClassifier(apiKey, model string) *OpenAIEmailClassifier {
	return &OpenAIEmailClassifier{
		apiKey: apiKey,
		model:  model,
		url:    openAIAPIURL,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Classify sends the mail to the model and returns its verdict.
func (c *OpenAIEmailClassifier) Classify(ctx context.Context, m email.Message) (email.Classification, error) {
	if c.apiKey == "" {
		return email.Classification{}, fmt.Errorf("email classifier: no api key")
	}

	userPrompt := fmt.Sprintf(
		"VON: %s\nBETREFF: %s\nDATUM: %s\n\nINHALT:\n%s",
		m.From, m.Subject, m.Date.Format(time.RFC3339), m.Body)

	request := openAIRequest{
		Model: c.model,
		Messages: []openAIMessage{
			{Role: "system", Content: emailClassifierSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		MaxTokens:      300,
		Temperature:    0,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	body, err := json.Marshal(request)
	if err != nil {
		return email.Classification{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return email.Classification{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return email.Classification{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return email.Classification{}, fmt.Errorf("OpenAI API error: %d - %s", resp.StatusCode, string(respBody))
	}

	var response openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return email.Classification{}, err
	}
	if len(response.Choices) == 0 {
		return email.Classification{}, fmt.Errorf("no response from OpenAI")
	}

	var cls email.Classification
	if err := json.Unmarshal([]byte(response.Choices[0].Message.Content), &cls); err != nil {
		return email.Classification{}, fmt.Errorf("decode classification: %w", err)
	}
	return cls, nil
}

const emailClassifierSystemPrompt = `Du klassifizierst E-Mails rund um ImmobilienScout24 (IS24) für einen Wohnungssuchenden.

Ziel: Erkenne echte, persönliche Nachrichten von einem Anbieter/Vermieter, der auf eine Bewerbung antwortet — insbesondere wenn er per E-Mail antwortet statt über den IS24-Chat. Solche Nachrichten sind wichtig und sollen gemeldet werden.

KEINE echten Anbieter-Nachrichten sind: automatische Eingangsbestätigungen, Newsletter, Werbung, "Ihre Anfrage wurde gesendet", Suchagent-Treffer, System-/Sicherheitsmails, Rechnungen.

Antworte als JSON-Objekt mit genau diesen Feldern:
{
  "is_landlord_reply": true|false,   // true nur bei echter, persönlicher Antwort eines Anbieters/Vermieters
  "is24_id": "string",                // IS24-Exposé-ID aus Betreff/Text falls vorhanden, sonst ""
  "summary": "string",                // ein kurzer deutscher Satz, worum es geht
  "reason": "string"                  // kurze Begründung der Einstufung
}
Antworte NUR mit dem JSON-Objekt.`
