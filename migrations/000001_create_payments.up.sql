CREATE TABLE payments (
    id              UUID            PRIMARY KEY,
    order_id        VARCHAR(255)    NOT NULL,
    customer_id     VARCHAR(255)    NOT NULL,
    amount_cents    BIGINT          NOT NULL CHECK (amount_cents > 0),
    currency        CHAR(3)         NOT NULL,
    status          VARCHAR(20)     NOT NULL
        CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED')),

    provider_ref    TEXT            NOT NULL DEFAULT '',
    failure_reason  TEXT            NOT NULL DEFAULT '',
    idempotency_key TEXT            NOT NULL,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    version         INT             NOT NULL DEFAULT 1
);

CREATE UNIQUE INDEX idx_payments_idempotency_key
    ON payments (idempotency_key);

CREATE INDEX idx_payments_customer_id
    ON payments (customer_id, created_at DESC);

CREATE INDEX idx_payments_active_status
    ON payments (status, created_at DESC)
    WHERE status IN ('PENDING', 'PROCESSING');

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER payments_set_updated_at
    BEFORE UPDATE ON payments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
