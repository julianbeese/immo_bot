package whatsapp

import (
	"context"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/julianbeese/immo_bot/internal/config"
	"github.com/julianbeese/immo_bot/internal/control"
	"github.com/julianbeese/immo_bot/internal/domain"
)

func TestOnlyDigits(t *testing.T) {
	cases := map[string]string{
		"+49 151 23456789": "4915123456789",
		"4915123456789":    "4915123456789",
		"(0151) 234-567":   "0151234567",
		"":                 "",
	}
	for in, want := range cases {
		if got := onlyDigits(in); got != want {
			t.Errorf("onlyDigits(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMessageText(t *testing.T) {
	if got := messageText(nil); got != "" {
		t.Errorf("nil message should yield empty, got %q", got)
	}
	conv := &waE2E.Message{Conversation: proto.String("hello")}
	if got := messageText(conv); got != "hello" {
		t.Errorf("conversation text = %q, want hello", got)
	}
	ext := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("/status")},
	}
	if got := messageText(ext); got != "/status" {
		t.Errorf("extended text = %q, want /status", got)
	}
}

func TestFormatListingContainsKeyFacts(t *testing.T) {
	l := &domain.Listing{
		Title:      "Schöne 3-Zimmer",
		Address:    "Musterstr. 1, Berlin",
		Price:      1500,
		Rooms:      3,
		Area:       80,
		HasBalcony: true,
		URL:        "https://is24.de/expose/123",
	}
	got := formatListing(l)
	for _, want := range []string{"Schöne 3-Zimmer", "1500 €", "3.0 Zimmer", "80 m²", "Balkon", l.URL} {
		if !strings.Contains(got, want) {
			t.Errorf("formatListing missing %q in:\n%s", want, got)
		}
	}
}

func TestDisabledClientIsNoOp(t *testing.T) {
	c, err := New(context.Background(), config.WhatsAppConfig{Enabled: false}, control.New(nil, nil, control.Defaults{QuietHoursEnabled: true, QuietHoursStart: "22:00", QuietHoursEnd: "07:00", Timezone: "Europe/Berlin"}), nil)
	if err != nil {
		t.Fatalf("disabled New should not error: %v", err)
	}
	if c.IsEnabled() {
		t.Error("client should be disabled")
	}
	// All notifier methods must be safe no-ops on a disabled client.
	ctx := context.Background()
	l := &domain.Listing{}
	if err := c.NotifyNewListing(ctx, l); err != nil {
		t.Errorf("disabled NotifyNewListing: %v", err)
	}
	if err := c.SendRawMessage(ctx, "x"); err != nil {
		t.Errorf("disabled SendRawMessage: %v", err)
	}
	c.Disconnect() // must not panic on disabled client
}

func TestEnabledWithoutTargetErrors(t *testing.T) {
	_, err := New(context.Background(), config.WhatsAppConfig{Enabled: true, StorePath: t.TempDir() + "/x.db"}, control.New(nil, nil, control.Defaults{QuietHoursEnabled: true, QuietHoursStart: "22:00", QuietHoursEnd: "07:00", Timezone: "Europe/Berlin"}), nil)
	if err == nil {
		t.Error("enabled client without target_phone should error")
	}
}
