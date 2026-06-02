package is24

// stealthJS hides the obvious headless-Chrome tells before any page script
// (including IS24's AWS-WAF challenge JS) runs. Injected via
// Page.addScriptToEvaluateOnNewDocument, so it covers every navigation —
// including the WAF challenge page reload to the actual content.
//
// Scope: only the cheap, well-known overrides that don't require external
// libraries. If WAF still flags us after these, we need a fuller stealth
// solution (chromedp-undetected, scraping API) — not more JS hacks.
const stealthJS = `
(() => {
  // navigator.webdriver: the single most-checked signal. Headless Chrome sets
  // this to true; real Chrome leaves it undefined.
  Object.defineProperty(navigator, 'webdriver', { get: () => undefined });

  // chrome.runtime: missing in headless, present in real Chrome. Stubbed
  // minimally so fingerprinters that just check existence stop flagging us.
  if (!window.chrome) { window.chrome = {}; }
  if (!window.chrome.runtime) { window.chrome.runtime = {}; }

  // Languages: headless defaults to en-US only; align with the German locale
  // we want IS24 to see (matches the IS24 cookie's logged-in user).
  Object.defineProperty(navigator, 'languages', {
    get: () => ['de-DE', 'de', 'en-US', 'en'],
  });

  // Plugins length: headless reports 0, real browsers report 3+. Fake non-empty
  // array — fingerprinters typically only check .length.
  Object.defineProperty(navigator, 'plugins', {
    get: () => [1, 2, 3, 4, 5],
  });

  // Notification permission: headless returns 'denied' from a script context
  // when the document permission is 'default' — a known divergence.
  const originalQuery = window.navigator.permissions && window.navigator.permissions.query;
  if (originalQuery) {
    window.navigator.permissions.query = (params) =>
      params && params.name === 'notifications'
        ? Promise.resolve({ state: Notification.permission })
        : originalQuery(params);
  }
})();
`
