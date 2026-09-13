-- Two customers/suppliers in the same organisation must not share a phone
-- number (brief follow-up: sale-counter customer lookup relies on phone
-- being a unique handle). Partial index so multiple parties with no phone
-- on file (NULL or '') are still allowed.
CREATE UNIQUE INDEX idx_parties_org_phone_unique ON parties (organisation_id, phone)
    WHERE phone IS NOT NULL AND phone <> '';
