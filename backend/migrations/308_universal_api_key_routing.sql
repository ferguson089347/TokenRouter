-- 308_universal_api_key_routing.sql converts user credentials to ordered channel
-- routing and adds current administrator defaults as a selectable configuration.
-- Existing binding rows remain available for upgrade inspection. User credentials
-- inherit administrator defaults, while hidden execution
-- credentials continue to use their server-selected fixed groups.
--
-- The migration runner applies this file transactionally before requests begin.
-- Group relationships are inserted before clearing each user's fixed group so
-- its target remains available to the converted credential. Authentication
-- snapshot version 48 causes old cached shapes to reload. Subsequent inheritance
-- changes publish invalidation events, while changes to default contents are
-- read during credential reconstruction on the next request.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS use_default_groups BOOLEAN NOT NULL DEFAULT true;

-- Capture direct group references before replacing the Key's storage shape;
-- the later group_id clearing relies on these relationships to retain routing.
INSERT INTO api_key_composite_groups(api_key_id,group_id,prefix,normalized_prefix,sort_order,created_at,updated_at)
SELECT id,group_id,group_id::text,group_id::text,0,NOW(),NOW()
FROM api_keys k
WHERE managed_by IS NULL AND deleted_at IS NULL AND group_id IS NOT NULL
  AND NOT EXISTS(SELECT 1 FROM api_key_composite_groups b WHERE b.api_key_id=k.id)
ON CONFLICT DO NOTHING;

UPDATE api_keys k SET is_composite=true,group_id=NULL,
 use_default_groups=true,updated_at=NOW()
WHERE managed_by IS NULL AND deleted_at IS NULL;

-- Managed credentials execute server-selected tasks with a fixed group.
UPDATE api_keys SET use_default_groups=false WHERE managed_by IS NOT NULL;

-- A mode switch must invalidate snapshots across instances even when the saved
-- bindings stay equal. Inherited setting contents are read outside those caches.
CREATE OR REPLACE FUNCTION enqueue_api_key_routing_invalidation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.use_default_groups IS DISTINCT FROM NEW.use_default_groups THEN
        PERFORM enqueue_auth_cache_invalidation(NEW.key);
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS trg_api_key_routing_invalidation ON api_keys;
CREATE TRIGGER trg_api_key_routing_invalidation AFTER UPDATE OF use_default_groups
ON api_keys FOR EACH ROW EXECUTE FUNCTION enqueue_api_key_routing_invalidation();

INSERT INTO settings(key,value,updated_at)
VALUES('api_key_default_groups','[]',NOW()) ON CONFLICT(key) DO NOTHING;
