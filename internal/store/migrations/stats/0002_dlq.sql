-- Dead-letter queue: parks Snapshot payloads that could not be
-- accepted (rate-limited, write failed, or malformed). Stored as raw
-- protobuf so a retry can re-decode without loss.

CREATE TABLE dlq (
    id           BIGSERIAL PRIMARY KEY,
    server_id    TEXT,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    reason       TEXT NOT NULL,
    raw          BYTEA NOT NULL
);
CREATE INDEX dlq_received_at_brin ON dlq USING brin (received_at);
