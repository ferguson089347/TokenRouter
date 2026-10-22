-- 309_restore_explicit_api_key_routing.sql restores explicit group selection for
-- credentials converted by administrator-default routing. A single numeric
-- binding identifies the fixed group captured by migration 308. Multiple bindings
-- retain their stored prefixes for the existing composite-key mode; an empty
-- inherited credential returns to the ordinary unbound state.
--
-- Deployments with an upgrade backup restore original routing fields before this
-- migration when an existing composite key used a single numeric prefix. Stored
-- bindings cannot distinguish that configuration from a converted ordinary key.
-- Credential strings, ownership, limits, usage and balances remain in place.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

WITH fixed_groups AS (
    SELECT k.id, MIN(b.group_id) AS group_id
    FROM api_keys k
    JOIN api_key_composite_groups b ON b.api_key_id = k.id
    WHERE k.managed_by IS NULL AND k.deleted_at IS NULL
      AND k.is_composite AND k.use_default_groups
    GROUP BY k.id
    HAVING COUNT(*) = 1
       AND BOOL_AND(b.prefix = b.group_id::text
                    AND b.normalized_prefix = b.group_id::text)
)
UPDATE api_keys k
SET is_composite = false, group_id = fixed_groups.group_id, updated_at = NOW()
FROM fixed_groups
WHERE k.id = fixed_groups.id;

-- Credentials with no saved channels become unbound so ordinary permission
-- checks reject consumption until the owner selects an authorized group.
UPDATE api_keys k
SET is_composite = false, updated_at = NOW()
WHERE k.managed_by IS NULL AND k.deleted_at IS NULL
  AND k.is_composite AND k.use_default_groups
  AND NOT EXISTS (
      SELECT 1 FROM api_key_composite_groups b WHERE b.api_key_id = k.id
  );

-- Fixed-group credentials resolve group_id directly. Remove captured relationships
-- so later composite edits start with the owner's submitted prefix mappings.
DELETE FROM api_key_composite_groups b
USING api_keys k
WHERE b.api_key_id = k.id AND k.managed_by IS NULL AND NOT k.is_composite;

DROP TRIGGER IF EXISTS trg_api_key_routing_invalidation ON api_keys;
DROP FUNCTION IF EXISTS enqueue_api_key_routing_invalidation();
ALTER TABLE api_keys DROP COLUMN IF EXISTS use_default_groups;

DELETE FROM settings
WHERE key IN ('api_key_default_groups', 'api_key_routing_cooldown_seconds');
