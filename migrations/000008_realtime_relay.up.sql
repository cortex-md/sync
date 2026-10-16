CREATE TABLE realtime_relay (
    id BIGSERIAL PRIMARY KEY,
    payload BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_realtime_relay_created_at ON realtime_relay(created_at);
