DROP INDEX IF EXISTS idx_landlords_agent;
 
ALTER TABLE landlords
    DROP COLUMN IF EXISTS agent_id;
 
DROP TABLE IF EXISTS agents;