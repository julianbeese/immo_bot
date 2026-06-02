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

// Enhance personalizes a message based on listing details. campaignPrompt
// overrides the default system prompt (empty → built-in default).
func (e *OpenAIEnhancer) Enhance(ctx context.Context, message string, listing *domain.Listing, campaignPrompt string) (string, error) {
	if !e.enabled || e.apiKey == "" {
		// Fallback: use generic details
		return e.fallbackEnhance(message, listing), nil
	}

	// Generate personalized details using GPT
	personalizedDetails, err := e.generatePersonalizedDetails(ctx, listing, campaignPrompt)
	if err != nil {
		// Fallback on error
		return e.fallbackEnhance(message, listing), nil
	}

	// Replace placeholder in message
	enhanced := strings.Replace(message, "{{.PersonalizedDetails}}", personalizedDetails, 1)
	return enhanced, nil
}

func (e *OpenAIEnhancer) generatePersonalizedDetails(ctx context.Context, listing *domain.Listing, campaignPrompt string) (string, error) {
	prompt := e.buildPrompt(listing)

	sysPrompt := systemPrompt
	if campaignPrompt != "" {
		sysPrompt = campaignPrompt
	}

	request := openAIRequest{
		Model: e.model,
		Messages: []openAIMessage{
			{
				Role:    "system",
				Content: sysPrompt,
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

	// IS24 expose without photos → AI must not pretend to have seen them.
	// We surface this as an explicit constraint in the prompt so phrasing like
	// "die Bilder zeigen…" gets swapped for "laut der Beschreibung…".
	sourcePhrasing := `Bezug auf die Inserats-Bilder ist erlaubt — z. B. "die Fotos zeigen", "auf den Bildern" — weil das Inserat Bilder hat.`
	if len(listing.ImageURLs) == 0 {
		sourcePhrasing = `Das Inserat hat KEINE Bilder. Beziehe dich daher AUSSCHLIESSLICH auf die Beschreibung — z. B. "laut der Beschreibung", "der Beschreibung nach". Erfinde keine sichtbaren Details, die nur auf Fotos zu sehen wären.`
	}

	return fmt.Sprintf(`
Wohnungsinserat:
- Titel: %s
- Adresse/Lage: %s %s
- Features: %s
- Beschreibung: %s

Schreibe 1-2 kurze, authentische Sätze darüber, was an dieser Wohnung
besonders ansprechend ist.

WICHTIG:
- Nenne 2-3 konkrete Aspekte aus dem Inserat (z.B. helle Räume, schönes
  Parkett, toller Balkon, moderne Küche, etc.)
- %s
- Erwähne KEINE Besichtigung - das kommt später im Text.
- Sei enthusiastisch aber nicht übertrieben. Schreibe auf Deutsch.
- Die Perspektive (ich vs. wir) und Tonalität sind im System-Prompt
  vorgegeben — halte dich strikt daran.
- Gib NUR die 1-2 Sätze zurück, keine Anführungszeichen, keine Erklärung.
`,
		listing.Title,
		listing.District,
		listing.City,
		strings.Join(features, ", "),
		truncate(listing.Description, 500),
		sourcePhrasing,
	)
}

func (e *OpenAIEnhancer) fallbackEnhance(message string, listing *domain.Listing) string {
	// When OpenAI is unavailable we cannot honor the campaign's perspective
	// (ich vs. wir) safely, and a wrong-perspective fallback ("Die Bilder haben
	// uns direkt angesprochen…" in a single-person letter) is worse than no
	// personalization at all. So emit a perspective-neutral one-liner that
	// mentions one concrete feature when present, and degrade to silence
	// otherwise — the surrounding template still reads fine.
	var feature string
	switch {
	case listing.HasBalcony:
		feature = "den Balkon"
	case listing.HasEBK:
		feature = "die Einbauküche"
	case listing.Area > 0:
		feature = fmt.Sprintf("die Wohnfläche von %d m²", listing.Area)
	case listing.District != "":
		feature = fmt.Sprintf("die Lage in %s", listing.District)
	}
	var personalizedDetails string
	if feature != "" {
		personalizedDetails = fmt.Sprintf("Besonders %s wirkt sehr ansprechend.", feature)
	}
	return strings.Replace(message, "{{.PersonalizedDetails}}", personalizedDetails, 1)
}

// IsEnabled returns whether the enhancer is enabled
func (e *OpenAIEnhancer) IsEnabled() bool {
	return e.enabled && e.apiKey != ""
}

// ClassifyGender returns SalutationMale / SalutationFemale based on the given
// person name (first or full name) using a light GPT call. Returns
// SalutationUnknown when the API is disabled, the call fails, or the model is
// uncertain — callers fall back to the gender-neutral salutation in that case.
func (e *OpenAIEnhancer) ClassifyGender(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.SalutationUnknown, nil
	}
	if !e.enabled || e.apiKey == "" {
		return domain.SalutationUnknown, nil
	}

	request := openAIRequest{
		Model: e.model,
		Messages: []openAIMessage{
			{
				Role: "system",
				Content: "You classify the likely gender of a German contact " +
					"person name. Answer with exactly one token: MALE, FEMALE " +
					"or UNKNOWN. UNKNOWN if the name is gender-neutral, " +
					"unisex, a company, or you are uncertain. No explanation.",
			},
			{
				Role:    "user",
				Content: name,
			},
		},
		MaxTokens:   3,
		Temperature: 0,
	}

	body, err := json.Marshal(request)
	if err != nil {
		return domain.SalutationUnknown, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIAPIURL, bytes.NewReader(body))
	if err != nil {
		return domain.SalutationUnknown, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return domain.SalutationUnknown, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return domain.SalutationUnknown, fmt.Errorf("OpenAI gender classify: %d - %s", resp.StatusCode, string(respBody))
	}

	var response openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return domain.SalutationUnknown, err
	}
	if len(response.Choices) == 0 {
		return domain.SalutationUnknown, nil
	}

	answer := strings.ToUpper(strings.TrimSpace(response.Choices[0].Message.Content))
	answer = strings.Trim(answer, ".,!? \"'")
	switch answer {
	case domain.SalutationMale, domain.SalutationFemale:
		return answer, nil
	default:
		return domain.SalutationUnknown, nil
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// DefaultSystemPrompt returns the built-in AI system prompt. The dashboard
// shows it as a baseline when a campaign has no ai_prompt override.
func DefaultSystemPrompt() string { return systemPrompt }

const systemPrompt = `Du bist ein Assistent, der personalisierte Sätze für Wohnungsbewerbungen schreibt.
Deine Aufgabe ist es, 1-2 authentische, enthusiastische Sätze zu schreiben, die zeigen, warum diese spezifische Wohnung interessant ist.
Nenne konkrete Details aus dem Inserat (Lage, Ausstattung, Räume, Bilder, etc.).
Schreibe natürlich und persönlich, nicht generisch.
WICHTIG: Erwähne KEINE Besichtigung - das kommt später im Text.
Vermeide Phrasen wie "Sehr geehrte", "Mit freundlichen Grüßen", "besichtigen", "Besichtigung" - nur den Mittelteil über die Wohnung selbst.`

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
