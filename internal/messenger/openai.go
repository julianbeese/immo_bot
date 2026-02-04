package messenger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/julianbeese/immo_bot/internal/domain"
)

const openAIAPIURL = "https://api.openai.com/v1/chat/completions"

// OpenAIEnhancer uses GPT to personalize messages
type OpenAIEnhancer struct {
	apiKey  string
	model   string
	enabled bool
	client  *http.Client
}

// NewOpenAIEnhancer creates a new OpenAI message enhancer
func NewOpenAIEnhancer(apiKey, model string, enabled bool) *OpenAIEnhancer {
	return &OpenAIEnhancer{
		apiKey:  apiKey,
		model:   model,
		enabled: enabled,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Enhance personalizes a message based on listing details
func (e *OpenAIEnhancer) Enhance(ctx context.Context, message string, listing *domain.Listing) (string, error) {
	if !e.enabled || e.apiKey == "" {
		// Fallback: use generic details
		return e.fallbackEnhance(message, listing), nil
	}

	// Generate personalized details using GPT
	personalizedDetails, err := e.generatePersonalizedDetails(ctx, listing)
	if err != nil {
		// Fallback on error
		return e.fallbackEnhance(message, listing), nil
	}

	// Replace placeholder in message
	enhanced := strings.Replace(message, "{{.PersonalizedDetails}}", personalizedDetails, 1)
	return enhanced, nil
}

func (e *OpenAIEnhancer) generatePersonalizedDetails(ctx context.Context, listing *domain.Listing) (string, error) {
	prompt := e.buildPrompt(listing)

	request := openAIRequest{
		Model: e.model,
		Messages: []openAIMessage{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
		MaxTokens:   150,
		Temperature: 0.7,
	}

	body, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIAPIURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API error: %d - %s", resp.StatusCode, string(respBody))
	}

	var response openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}

	return strings.TrimSpace(response.Choices[0].Message.Content), nil
}

func (e *OpenAIEnhancer) buildPrompt(listing *domain.Listing) string {
	// Collect features
	var features []string
	if listing.HasBalcony {
		features = append(features, "Balkon")
	}
	if listing.HasEBK {
		features = append(features, "Einbauküche")
	}
	if listing.HasElevator {
		features = append(features, "Aufzug")
	}
	if listing.Rooms > 0 {
		features = append(features, fmt.Sprintf("%.0f Zimmer", listing.Rooms))
	}
	if listing.Area > 0 {
		features = append(features, fmt.Sprintf("%d m²", listing.Area))
	}

	return fmt.Sprintf(`
Wohnungsinserat:
- Titel: %s
- Adresse/Lage: %s %s
- Features: %s
- Beschreibung: %s

Schreibe 1-2 kurze, authentische Sätze darüber, was an dieser Wohnung besonders ansprechend ist.
Beispiel-Stil: "Die Bilder haben uns direkt angesprochen, besonders die hellen Räume und der Balkon. Außerdem finden wir die Lage super!"

Nenne 2-3 konkrete Aspekte aus dem Inserat. Sei enthusiastisch aber nicht übertrieben. Schreibe auf Deutsch.
Gib NUR die 1-2 Sätze zurück, keine Anführungszeichen, keine Erklärung.
`,
		listing.Title,
		listing.District,
		listing.City,
		strings.Join(features, ", "),
		truncate(listing.Description, 500),
	)
}

func (e *OpenAIEnhancer) fallbackEnhance(message string, listing *domain.Listing) string {
	// Generate generic but reasonable details
	var details []string

	if listing.HasBalcony {
		details = append(details, "der Balkon")
	}
	if listing.Area > 0 {
		details = append(details, fmt.Sprintf("die großzügige Wohnfläche von %d m²", listing.Area))
	}
	if listing.District != "" {
		details = append(details, fmt.Sprintf("die Lage in %s", listing.District))
	}

	var personalizedDetails string
	if len(details) >= 2 {
		personalizedDetails = fmt.Sprintf("Die Wohnung hat uns direkt angesprochen, besonders %s und %s. Daher würden wir sie gerne persönlich besichtigen.", details[0], details[1])
	} else if len(details) == 1 {
		personalizedDetails = fmt.Sprintf("Die Wohnung hat uns direkt angesprochen, besonders %s. Daher würden wir sie gerne persönlich besichtigen.", details[0])
	} else {
		personalizedDetails = "Die Wohnung hat uns auf den Bildern direkt angesprochen und entspricht genau unseren Vorstellungen. Daher würden wir sie gerne persönlich besichtigen."
	}

	return strings.Replace(message, "{{.PersonalizedDetails}}", personalizedDetails, 1)
}

// IsEnabled returns whether the enhancer is enabled
func (e *OpenAIEnhancer) IsEnabled() bool {
	return e.enabled && e.apiKey != ""
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

const systemPrompt = `Du bist ein Assistent, der personalisierte Sätze für Wohnungsbewerbungen schreibt.
Deine Aufgabe ist es, 1-2 authentische, enthusiastische Sätze zu schreiben, die zeigen, warum diese spezifische Wohnung interessant ist.
Nenne konkrete Details aus dem Inserat (Lage, Ausstattung, Räume, etc.).
Schreibe natürlich und persönlich, nicht generisch.
Vermeide Phrasen wie "Sehr geehrte" oder "Mit freundlichen Grüßen" - nur den Mittelteil.`

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature float64         `json:"temperature"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
}
