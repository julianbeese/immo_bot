package antidetect

import (
	"math/rand"
	"sync"
	"time"
)

// RateLimiter controls request rate to avoid detection
type RateLimiter struct {
	mu                   sync.Mutex
	maxRequestsPerMinute int
	requestTimes         []time.Time
	minDelay             time.Duration
	maxDelay             time.Duration
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(maxPerMinute int, minDelay, maxDelay time.Duration) *RateLimiter {
	return &RateLimiter{
		maxRequestsPerMinute: maxPerMinute,
		requestTimes:         make([]time.Time, 0, maxPerMinute),
		minDelay:             minDelay,
		maxDelay:             maxDelay,
	}
}

// Wait blocks until a request can be made within rate limits
func (rl *RateLimiter) Wait() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	// Remove old request times
	var filtered []time.Time
	for _, t := range rl.requestTimes {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	rl.requestTimes = filtered

	// If at limit, wait until oldest request expires
	if len(rl.requestTimes) >= rl.maxRequestsPerMinute {
		waitUntil := rl.requestTimes[0].Add(time.Minute)
		if waitUntil.After(now) {
			time.Sleep(waitUntil.Sub(now))
		}
	}

	// Add random delay for human-like behavior
	delay := rl.randomDelay()
	time.Sleep(delay)

	// Record this request
	rl.requestTimes = append(rl.requestTimes, time.Now())
}

// randomDelay returns a random duration between minDelay and maxDelay
func (rl *RateLimiter) randomDelay() time.Duration {
	if rl.maxDelay <= rl.minDelay {
		return rl.minDelay
	}
	diff := rl.maxDelay - rl.minDelay
	return rl.minDelay + time.Duration(rand.Int63n(int64(diff)))
}

// UserAgentRotator rotates through user agent strings
type UserAgentRotator struct {
	mu         sync.Mutex
	userAgents []string
	index      int
}

// NewUserAgentRotator creates a new user agent rotator
func NewUserAgentRotator(userAgents []string) *UserAgentRotator {
	if len(userAgents) == 0 {
		userAgents = defaultUserAgents()
	}
	// Shuffle the list initially
	shuffled := make([]string, len(userAgents))
	copy(shuffled, userAgents)
	rand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	return &UserAgentRotator{
		userAgents: shuffled,
		index:      0,
	}
}

// Next returns the next user agent in rotation
func (r *UserAgentRotator) Next() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	ua := r.userAgents[r.index]
	r.index = (r.index + 1) % len(r.userAgents)
	return ua
}

// Current returns the current user agent without advancing
func (r *UserAgentRotator) Current() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.userAgents[r.index]
}

func defaultUserAgents() []string {
	return []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0",
	}
}

// HumanBehavior provides human-like delays for browser automation
type HumanBehavior struct {
	TypeDelay   time.Duration
	ActionDelay time.Duration
}

// NewHumanBehavior creates a new human behavior simulator
func NewHumanBehavior(typeDelay, actionDelay time.Duration) *HumanBehavior {
	return &HumanBehavior{
		TypeDelay:   typeDelay,
		ActionDelay: actionDelay,
	}
}

// TypeChar returns a delay for typing a character (with variation)
func (h *HumanBehavior) TypeChar() time.Duration {
	// Add 0-50% variation
	variation := time.Duration(rand.Int63n(int64(h.TypeDelay / 2)))
	return h.TypeDelay + variation
}

// ActionPause returns a delay between actions
func (h *HumanBehavior) ActionPause() time.Duration {
	// Add 0-100% variation
	variation := time.Duration(rand.Int63n(int64(h.ActionDelay)))
	return h.ActionDelay + variation
}

// ScrollPause returns a delay for scrolling
func (h *HumanBehavior) ScrollPause() time.Duration {
	return time.Duration(200+rand.Intn(300)) * time.Millisecond
}

// ThinkPause returns a delay for "thinking" before action
func (h *HumanBehavior) ThinkPause() time.Duration {
	return time.Duration(500+rand.Intn(1500)) * time.Millisecond
}
