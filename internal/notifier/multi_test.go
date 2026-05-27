package notifier

import (
	"context"
	"errors"
	"testing"

	"github.com/julianbeese/immo_bot/internal/domain"
)

// fakeNotifier records calls and can be disabled or made to fail.
type fakeNotifier struct {
	enabled bool
	err     error
	calls   int
}

func (f *fakeNotifier) NotifyNewListing(context.Context, *domain.Listing) error {
	f.calls++
	return f.err
}
func (f *fakeNotifier) NotifyContactSent(context.Context, *domain.Listing) error { f.calls++; return f.err }
func (f *fakeNotifier) NotifyContactFailed(context.Context, *domain.Listing, string) error {
	f.calls++
	return f.err
}
func (f *fakeNotifier) NotifyError(context.Context, string) error { f.calls++; return f.err }
func (f *fakeNotifier) NotifyMessagePreview(context.Context, *domain.Listing, string) error {
	f.calls++
	return f.err
}
func (f *fakeNotifier) SendRawMessage(context.Context, string) error { f.calls++; return f.err }
func (f *fakeNotifier) IsEnabled() bool                              { return f.enabled }

func TestMultiFiltersDisabled(t *testing.T) {
	on := &fakeNotifier{enabled: true}
	off := &fakeNotifier{enabled: false}
	m := NewMulti(on, off, nil)

	if !m.IsEnabled() {
		t.Fatal("multi with one enabled channel should be enabled")
	}
	if err := m.NotifyNewListing(context.Background(), &domain.Listing{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if on.calls != 1 {
		t.Errorf("enabled channel calls = %d, want 1", on.calls)
	}
	if off.calls != 0 {
		t.Errorf("disabled channel should not be called, got %d", off.calls)
	}
}

func TestMultiFanOutAllMethods(t *testing.T) {
	a := &fakeNotifier{enabled: true}
	b := &fakeNotifier{enabled: true}
	m := NewMulti(a, b)
	ctx := context.Background()
	l := &domain.Listing{}

	m.NotifyNewListing(ctx, l)
	m.NotifyContactSent(ctx, l)
	m.NotifyContactFailed(ctx, l, "x")
	m.NotifyError(ctx, "x")
	m.NotifyMessagePreview(ctx, l, "x")
	m.SendRawMessage(ctx, "x")

	if a.calls != 6 || b.calls != 6 {
		t.Errorf("each channel should get 6 calls, got a=%d b=%d", a.calls, b.calls)
	}
}

func TestMultiJoinsErrors(t *testing.T) {
	errA := errors.New("a failed")
	errB := errors.New("b failed")
	a := &fakeNotifier{enabled: true, err: errA}
	b := &fakeNotifier{enabled: true, err: errB}
	ok := &fakeNotifier{enabled: true}
	m := NewMulti(a, ok, b)

	err := m.NotifyError(context.Background(), "boom")
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Errorf("joined error should contain both, got %v", err)
	}
	if ok.calls != 1 {
		t.Error("a failing channel must not stop others")
	}
}

func TestMultiEmptyIsDisabled(t *testing.T) {
	m := NewMulti(&fakeNotifier{enabled: false}, nil)
	if m.IsEnabled() {
		t.Error("multi with no enabled channels should be disabled")
	}
	if err := m.SendRawMessage(context.Background(), "x"); err != nil {
		t.Errorf("no-op fan-out should return nil, got %v", err)
	}
}
