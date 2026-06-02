package antidetect

import (
	"context"
	"strings"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/chromedp"
)

// Proxy carries upstream proxy settings shared by the scraper and the contact
// submitter. URL is the scheme+host+port passed to Chrome's --proxy-server flag
// (e.g. "http://gw.example.com:7000"); Username/Password are answered via the
// fetch domain because Chrome's --proxy-server cannot take inline credentials.
type Proxy struct {
	URL      string
	Username string
	Password string
}

// Enabled reports whether a proxy URL is configured.
func (p Proxy) Enabled() bool { return strings.TrimSpace(p.URL) != "" }

// RequiresAuth reports whether credentials should be answered.
func (p Proxy) RequiresAuth() bool {
	return p.Enabled() && strings.TrimSpace(p.Username) != ""
}

// AttachAuthHandler registers a chromedp listener that answers proxy
// authentication challenges with the configured credentials. Call this on the
// browser context BEFORE running fetch.Enable(...).WithHandleAuthRequests(true)
// — otherwise auth-required events arrive with nothing listening and the
// request stalls until timeout.
func (p Proxy) AttachAuthHandler(ctx context.Context) {
	if !p.RequiresAuth() {
		return
	}
	user, pass := p.Username, p.Password
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *fetch.EventAuthRequired:
			// Must run on a separate goroutine: the event loop is blocked while
			// the handler runs, and ContinueWithAuth itself dispatches over it.
			go func() {
				c := chromedp.FromContext(ctx)
				execCtx := cdp.WithExecutor(ctx, c.Target)
				_ = fetch.ContinueWithAuth(e.RequestID, &fetch.AuthChallengeResponse{
					Response: fetch.AuthChallengeResponseResponseProvideCredentials,
					Username: user,
					Password: pass,
				}).Do(execCtx)
			}()
		case *fetch.EventRequestPaused:
			// Non-auth pauses (we only asked for auth) — let them through.
			go func() {
				c := chromedp.FromContext(ctx)
				execCtx := cdp.WithExecutor(ctx, c.Target)
				_ = fetch.ContinueRequest(e.RequestID).Do(execCtx)
			}()
		}
	})
}

// EnableAction returns the chromedp action that turns on the fetch domain with
// auth interception. Returns nil when no auth is required so callers can append
// it unconditionally.
func (p Proxy) EnableAction() chromedp.Action {
	if !p.RequiresAuth() {
		return nil
	}
	return fetch.Enable().WithHandleAuthRequests(true)
}
