// Package notifier provides Multi, a fan-out notifier that forwards every
// notification to all enabled channels (Telegram, WhatsApp, ...).
package notifier

import (
	"context"
	"errors"

	"github.com/julianbeese/immo_bot/internal/domain"
)

// Notifier is one notification channel. Matches scheduler.Notifier structurally,
// so a *Multi can be passed wherever a scheduler.Notifier is expected.
type Notifier interface {
	NotifyNewListing(ctx context.Context, l *domain.Listing) error
	NotifyContactSent(ctx context.Context, l *domain.Listing) error
	NotifyContactFailed(ctx context.Context, l *domain.Listing, errMsg string) error
	NotifyError(ctx context.Context, errMsg string) error
	NotifyMessagePreview(ctx context.Context, l *domain.Listing, message string) error
	SendRawMessage(ctx context.Context, text string) error
	IsEnabled() bool
}

// Multi fans every call out to all enabled channels and joins their errors.
type Multi struct {
	channels []Notifier
}

// NewMulti builds a Multi from the given channels, dropping disabled ones
// (and nil entries) so callers can pass channels unconditionally.
func NewMulti(channels ...Notifier) *Multi {
	enabled := make([]Notifier, 0, len(channels))
	for _, c := range channels {
		if c != nil && c.IsEnabled() {
			enabled = append(enabled, c)
		}
	}
	return &Multi{channels: enabled}
}

// fanOut delivers to every channel and treats the call as successful if at
// least one channel accepted it — this is what makes additional channels act
// as fallbacks (e.g. Telegram still "succeeds" while WhatsApp is down, so the
// scheduler marks the listing handled instead of resending it every cycle).
// An error is returned only when every channel failed (or none are configured
// but at least one was attempted).
func (m *Multi) fanOut(fn func(Notifier) error) error {
	var errs []error
	delivered := false
	for _, c := range m.channels {
		if err := fn(c); err != nil {
			errs = append(errs, err)
		} else {
			delivered = true
		}
	}
	if delivered {
		return nil
	}
	return errors.Join(errs...)
}

func (m *Multi) NotifyNewListing(ctx context.Context, l *domain.Listing) error {
	return m.fanOut(func(c Notifier) error { return c.NotifyNewListing(ctx, l) })
}

func (m *Multi) NotifyContactSent(ctx context.Context, l *domain.Listing) error {
	return m.fanOut(func(c Notifier) error { return c.NotifyContactSent(ctx, l) })
}

func (m *Multi) NotifyContactFailed(ctx context.Context, l *domain.Listing, errMsg string) error {
	return m.fanOut(func(c Notifier) error { return c.NotifyContactFailed(ctx, l, errMsg) })
}

func (m *Multi) NotifyError(ctx context.Context, errMsg string) error {
	return m.fanOut(func(c Notifier) error { return c.NotifyError(ctx, errMsg) })
}

func (m *Multi) NotifyMessagePreview(ctx context.Context, l *domain.Listing, message string) error {
	return m.fanOut(func(c Notifier) error { return c.NotifyMessagePreview(ctx, l, message) })
}

func (m *Multi) SendRawMessage(ctx context.Context, text string) error {
	return m.fanOut(func(c Notifier) error { return c.SendRawMessage(ctx, text) })
}

// IsEnabled reports whether at least one channel is active.
func (m *Multi) IsEnabled() bool {
	return len(m.channels) > 0
}
