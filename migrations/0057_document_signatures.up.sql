-- Electronic signature requests for documents. A request is a link; the signer reads the document and signs by typing their
-- name (and optionally drawing a signature) and ticking a consent box. The record keeps what was shown (a fingerprint of the
-- document content), when, and the signer's network address and browser, as evidence. This is a simple electronic signature,
-- not a qualified one.
CREATE TABLE document_signatures (
    id                uuid PRIMARY KEY,
    organisation_id   uuid NOT NULL REFERENCES organisations(id),
    document_type     text NOT NULL,
    document_id       uuid NOT NULL,
    token_hash        text NOT NULL UNIQUE,
    status            text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'SIGNED', 'DECLINED', 'REVOKED')),
    requested_name    text,
    requested_email   text,
    document_hash     text NOT NULL CHECK (document_hash ~ '^[0-9a-f]{64}$'),
    signer_name       text,
    signature_png     bytea,
    decline_reason    text,
    signed_at         timestamptz,
    signer_ip         text,
    signer_agent      text,
    consent_text      text,
    expires_at        timestamptz NOT NULL,
    created_by        uuid NOT NULL REFERENCES users(id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'SIGNED') = (signed_at IS NOT NULL))
);
CREATE INDEX idx_document_signatures_doc ON document_signatures(organisation_id, document_type, document_id);
-- Like share_links (migration 0027), the public signer has only the bearer token, so token_hash is the access control and
-- the table is not row-level-secured; every authenticated query filters by organisation_id explicitly.
