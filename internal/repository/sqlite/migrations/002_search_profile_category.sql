-- Per-campaign personalization: a search profile may reference a campaign
-- (see config.Campaigns) that selects its message template, AI prompt and
-- contact profile. NULL/empty falls back to the default campaign.
ALTER TABLE search_profiles ADD COLUMN category TEXT;
