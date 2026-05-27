package scheduler

import (
	"context"
	"log/slog"
	"testing"

	"github.com/julianbeese/immo_bot/internal/domain"
)

// fakeNotifier records SendRawMessage calls for cookie-health assertions.
type fakeNotifier struct{ raw []string }

func (f *fakeNotifier) NotifyNewListing(context.Context, *domain.Listing) error  { return nil }
func (f *fakeNotifier) NotifyContactSent(context.Context, *domain.Listing) error { return nil }
func (f *fakeNotifier) NotifyContactFailed(context.Context, *domain.Listing, string) error {
	return nil
}
func (f *fakeNotifier) NotifyError(context.Context, string) error { return nil }
func (f *fakeNotifier) NotifyMessagePreview(context.Context, *domain.Listing, string) error {
	return nil
}
func (f *fakeNotifier) SendRawMessage(_ context.Context, text string) error {
	f.raw = append(f.raw, text)
	return nil
}
func (f *fakeNotifier) IsEnabled() bool { return true }

func TestCookieHealthWarnsOnceAndRecovers(t *testing.T) {
	fn := &fakeNotifier{}
	s := &Scheduler{notifier: fn, logger: slog.Default()}
	ctx := context.Background()

	// Below threshold: no warning yet.
	for i := 0; i < cookieWarnThreshold-1; i++ {
		s.checkCookieHealth(ctx, 2, 0, 1)
	}
	if len(fn.raw) != 0 {
		t.Fatalf("warned too early: %v", fn.raw)
	}

	// Reaching threshold triggers exactly one warning.
	s.checkCookieHealth(ctx, 2, 0, 1)
	if len(fn.raw) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(fn.raw))
	}

	// Continued emptiness must not spam.
	s.checkCookieHealth(ctx, 2, 0, 1)
	if len(fn.raw) != 1 {
		t.Fatalf("warning should not repeat, got %d", len(fn.raw))
	}

	// Listings coming back resets the state.
	s.checkCookieHealth(ctx, 2, 5, 0)
	if s.emptyPolls != 0 || s.cookieAlert {
		t.Fatalf("recovery should reset state: empty=%d alert=%v", s.emptyPolls, s.cookieAlert)
	}

	// After recovery, a fresh empty streak warns again.
	for i := 0; i < cookieWarnThreshold; i++ {
		s.checkCookieHealth(ctx, 2, 0, 1)
	}
	if len(fn.raw) != 2 {
		t.Fatalf("expected a 2nd warning after recovery, got %d", len(fn.raw))
	}
}

func TestCookieHealthNoProfilesNeverWarns(t *testing.T) {
	fn := &fakeNotifier{}
	s := &Scheduler{notifier: fn, logger: slog.Default()}
	for i := 0; i < 5; i++ {
		s.checkCookieHealth(context.Background(), 0, 0, 0)
	}
	if len(fn.raw) != 0 {
		t.Fatalf("no active profiles should never warn, got %v", fn.raw)
	}
}
