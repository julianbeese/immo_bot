-- Contact person (Ansprechpartner) extracted from the IS24 expose, separate
-- from landlord_name (which holds the realtor company / agency). Salutation
-- caches the OpenAI gender classification: MALE, FEMALE or UNKNOWN.
ALTER TABLE listings ADD COLUMN contact_person TEXT;
ALTER TABLE listings ADD COLUMN contact_salutation TEXT;
