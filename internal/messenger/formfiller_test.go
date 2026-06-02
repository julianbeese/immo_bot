package messenger

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/julianbeese/immo_bot/internal/contact"
)

// TestMapFormFields drives the mapper against a fake OpenAI endpoint: verifies
// the request shape (json_object response_format, fields + applicant in the
// prompt) and that the {"actions":[...]} reply is parsed into FieldActions.
func TestMapFormFields(t *testing.T) {
	var gotReq openAIRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)
		reply := `{"actions":[` +
			`{"selector":"input[name=\"x_firstName\"]","value":"Max","kind":"type"},` +
			`{"selector":"select[name=\"x_salutation\"]","value":"MALE","kind":"select"},` +
			`{"selector":"input[name=\"x_pets\"][value=\"NO\"]","value":"","kind":"click"}` +
			`]}`
		resp := openAIResponse{Choices: []struct {
			Message openAIMessage `json:"message"`
		}{{Message: openAIMessage{Role: "assistant", Content: reply}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	f := NewOpenAIFormFiller("test-key", "gpt-test")
	f.url = srv.URL

	fields := []contact.FormField{
		{Selector: `input[name="x_firstName"]`, Name: "x_firstName", Type: "text", Label: "Vorname"},
		{Selector: `select[name="x_salutation"]`, Name: "x_salutation", Type: "select", Label: "Anrede",
			Options: []contact.FieldOption{{Value: "MALE", Text: "Herr"}, {Value: "FEMALE", Text: "Frau"}}},
		{Selector: `input[name="x_pets"][value="NO"]`, Name: "x_pets", Type: "radio", Label: "Haustiere Nein"},
	}
	profile := contact.Profile{Salutation: "Herr", FirstName: "Max", LastName: "Muster", Pets: false}

	actions, err := f.MapFormFields(context.Background(), fields, profile, "Hallo")
	if err != nil {
		t.Fatalf("MapFormFields: %v", err)
	}

	// Request shape.
	if gotReq.ResponseFormat == nil || gotReq.ResponseFormat.Type != "json_object" {
		t.Errorf("response_format = %+v, want json_object", gotReq.ResponseFormat)
	}
	if len(gotReq.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(gotReq.Messages))
	}
	userMsg := gotReq.Messages[1].Content
	if !strings.Contains(userMsg, "x_salutation") || !strings.Contains(userMsg, "Max") {
		t.Errorf("user prompt missing fields/applicant data: %q", userMsg)
	}

	// Parsed actions.
	if len(actions) != 3 {
		t.Fatalf("actions = %d, want 3: %+v", len(actions), actions)
	}
	if actions[0].Kind != "type" || actions[0].Value != "Max" {
		t.Errorf("action[0] = %+v", actions[0])
	}
	if actions[1].Kind != "select" || actions[1].Value != "MALE" {
		t.Errorf("action[1] = %+v", actions[1])
	}
	if actions[2].Kind != "click" || actions[2].Selector != `input[name="x_pets"][value="NO"]` {
		t.Errorf("action[2] = %+v", actions[2])
	}
}
