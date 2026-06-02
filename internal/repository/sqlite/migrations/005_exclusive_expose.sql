-- Track whether a listing is paywalled behind IS24's "Suchen+" subscription.
-- When true, the contact form submit on the live page fails for non-members,
-- so the scheduler refuses to send a Nachricht and the dashboard shows a
-- badge so the user knows the listing is unreachable without a sub.
ALTER TABLE listings ADD COLUMN exclusive_expose INTEGER NOT NULL DEFAULT 0;
