CREATE TABLE IF NOT EXISTS agents (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    whatsapp_phone  TEXT        NOT NULL UNIQUE,
    name            TEXT        NOT NULL DEFAULT '',
    unit_count      INTEGER     NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
 
ALTER TABLE landlords
    ADD COLUMN IF NOT EXISTS agent_id UUID REFERENCES agents(id);
 
CREATE INDEX IF NOT EXISTS idx_landlords_agent ON landlords (agent_id);