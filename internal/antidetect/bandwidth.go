package antidetect

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// ErrBandwidthExceeded is returned when a fetch would push the proxy usage past
// the configured cap. Callers should treat it as a temporary block until the
// next billing period.
var ErrBandwidthExceeded = errors.New("proxy bandwidth cap reached")

// MetaStore is the minimal persistence surface the guard needs. The bot's
// sqlite repository satisfies it.
type MetaStore interface {
	GetMeta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error
}

// Persisted state keys.
const (
	metaProxyBytesUsed   = "proxy.bytes_used"
	metaProxyPeriodStart = "proxy.period_start" // YYYY-MM, calendar-month rollover
)

// BandwidthGuard tracks how many bytes the upstream proxy has served in the
// current calendar month and refuses new requests once the cap is reached.
// Numbers come from Chrome's Network.loadingFinished events
// (EncodedDataLength) — what IPRoyal/Shifter/Smartproxy actually meter.
type BandwidthGuard struct {
	cap        int64 // bytes; 0 = unlimited (guard inert)
	mu         sync.Mutex
	used       int64
	period     string // YYYY-MM
	store      MetaStore
	warnedHigh bool // 80% warning latch
	logger     Logger
}

// Logger is the slim subset of slog used by the guard.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// NewBandwidthGuard constructs a guard. capMB == 0 disables enforcement (the
// guard still tracks usage so /stats can show it). Pass a non-nil store for
// monthly state to survive restarts.
func NewBandwidthGuard(capMB int, store MetaStore, logger Logger) *BandwidthGuard {
	g := &BandwidthGuard{
		cap:    int64(capMB) * 1024 * 1024,
		store:  store,
		logger: logger,
		period: currentPeriod(),
	}
	g.load()
	return g
}

func currentPeriod() string { return time.Now().UTC().Format("2006-01") }

func (g *BandwidthGuard) load() {
	if g.store == nil {
		return
	}
	ctx := context.Background()
	if v, _ := g.store.GetMeta(ctx, metaProxyPeriodStart); v != "" {
		g.period = v
	}
	if v, _ := g.store.GetMeta(ctx, metaProxyBytesUsed); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			g.used = n
		}
	}
	// Cross-month boot: roll over if the persisted period is stale.
	if cur := currentPeriod(); cur != g.period {
		g.period = cur
		g.used = 0
		g.warnedHigh = false
		g.persist()
	}
}

func (g *BandwidthGuard) persist() {
	if g.store == nil {
		return
	}
	ctx := context.Background()
	_ = g.store.SetMeta(ctx, metaProxyPeriodStart, g.period)
	_ = g.store.SetMeta(ctx, metaProxyBytesUsed, strconv.FormatInt(g.used, 10))
}

// rolloverIfNeeded resets used bytes when the calendar month flips. Caller
// holds g.mu.
func (g *BandwidthGuard) rolloverIfNeeded() {
	cur := currentPeriod()
	if cur == g.period {
		return
	}
	if g.logger != nil {
		g.logger.Info("proxy bandwidth: monthly rollover",
			"prev_period", g.period, "prev_used_mb", g.used/(1024*1024),
			"new_period", cur)
	}
	g.period = cur
	g.used = 0
	g.warnedHigh = false
	g.persist()
}

// Allowed reports whether a new request may proceed. Always true when no cap is
// configured.
func (g *BandwidthGuard) Allowed() bool {
	if g.cap == 0 {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rolloverIfNeeded()
	return g.used < g.cap
}

// Add records bytes consumed and persists. Triggers warn-at-80% / block-at-100%
// log messages exactly once per period.
func (g *BandwidthGuard) Add(bytes int64) {
	if bytes <= 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rolloverIfNeeded()
	prev := g.used
	g.used += bytes
	g.persist()
	if g.cap == 0 || g.logger == nil {
		return
	}
	if !g.warnedHigh && g.used >= g.cap*8/10 {
		g.warnedHigh = true
		g.logger.Warn("proxy bandwidth: 80% of monthly cap consumed",
			"used_mb", g.used/(1024*1024), "cap_mb", g.cap/(1024*1024))
	}
	if prev < g.cap && g.used >= g.cap {
		g.logger.Warn("proxy bandwidth: monthly cap reached — further fetches blocked until next month",
			"used_mb", g.used/(1024*1024), "cap_mb", g.cap/(1024*1024))
	}
}

// Used returns the bytes consumed in the current period.
func (g *BandwidthGuard) Used() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rolloverIfNeeded()
	return g.used
}

// Cap returns the configured byte cap (0 = unlimited).
func (g *BandwidthGuard) Cap() int64 { return g.cap }

// Period returns the current period stamp (YYYY-MM).
func (g *BandwidthGuard) Period() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rolloverIfNeeded()
	return g.period
}

// Reset clears the current-period counter. Use for manual /traffic reset.
func (g *BandwidthGuard) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.used = 0
	g.warnedHigh = false
	g.period = currentPeriod()
	g.persist()
}

// Summary returns a human-readable usage line for logs and chat commands.
func (g *BandwidthGuard) Summary() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rolloverIfNeeded()
	usedMB := float64(g.used) / (1024 * 1024)
	if g.cap == 0 {
		return fmt.Sprintf("proxy traffic %s: %.1f MB (no cap)", g.period, usedMB)
	}
	capMB := float64(g.cap) / (1024 * 1024)
	pct := usedMB / capMB * 100
	return fmt.Sprintf("proxy traffic %s: %.1f / %.0f MB (%.1f%%)", g.period, usedMB, capMB, pct)
}

// AttachByteCounter wires a chromedp listener that sums encodedDataLength for
// every finished network request on the given browser context, returning a
// snapshot accessor. Call it AFTER NewContext and BEFORE the first Run; pair
// the accessor with guard.Add at the end of the page lifecycle so the total is
// persisted exactly once per fetch.
func AttachByteCounter(ctx context.Context) func() int64 {
	var (
		mu    sync.Mutex
		total int64
	)
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if e, ok := ev.(*network.EventLoadingFinished); ok {
			mu.Lock()
			total += int64(e.EncodedDataLength)
			mu.Unlock()
		}
	})
	return func() int64 {
		mu.Lock()
		defer mu.Unlock()
		return total
	}
}

// NetworkEnable is a re-exported action so call sites don't need to import
// cdproto/network just to turn the domain on (events otherwise wouldn't fire).
func NetworkEnable() chromedp.Action { return network.Enable() }
