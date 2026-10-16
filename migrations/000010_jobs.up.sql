CREATE TABLE email_outbox (
    id BIGSERIAL PRIMARY KEY,
    to_address TEXT NOT NULL,
    template TEXT NOT NULL,
    locale TEXT NOT NULL DEFAULT 'en',
    data JSONB NOT NULL DEFAULT '{}',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    failed_at TIMESTAMPTZ
);

CREATE INDEX idx_email_outbox_pending ON email_outbox(next_attempt_at) WHERE sent_at IS NULL AND failed_at IS NULL;
CREATE INDEX idx_email_outbox_finished ON email_outbox(coalesce(sent_at, failed_at)) WHERE sent_at IS NOT NULL OR failed_at IS NOT NULL;

CREATE TABLE blob_uploads_pending (
    blob_key TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE blob_deletions (
    id BIGSERIAL PRIMARY KEY,
    blob_key TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_blob_deletions_due ON blob_deletions(next_attempt_at);
