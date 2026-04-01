-- Track STK push requests so the callback can resolve landlord → ref.
CREATE TABLE stk_pushes (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    landlord_id      UUID        NOT NULL REFERENCES landlords(id),
    checkout_ref     TEXT        NOT NULL,  -- RENTLOOP-{id_prefix}
    mpesa_receipt    TEXT,                  -- filled in on success
    amount           INT         NOT NULL,
    status           TEXT        NOT NULL DEFAULT 'pending',  -- pending|success|failed
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX ON stk_pushes (mpesa_receipt) WHERE mpesa_receipt IS NOT NULL;