-- 305_users_id_start_at_1000.sql raises the next generated user ID to at least
-- 1000 while keeping stored users and their references stable. Fresh databases
-- use that starting point, and existing databases retain a higher generator
-- position when one has already been allocated. Manually inserted IDs also
-- contribute to the required next value.
--
-- PostgreSQL maintains generator state separately from user rows. The migration
-- reads both while registration inserts are excluded, then uses transactional
-- sequence restart when an advance is needed. Replaying the script keeps the
-- generator moving forward. An application rollback can continue allocating
-- from the advanced sequence because the users table and its ID type stay
-- compatible with the previous binary.

-- Bound lock waits so startup can be retried when registrations are busy.
SET LOCAL lock_timeout = '2s';

-- Registration inserts take row-exclusive locks. Hold the table while checking
-- stored IDs and restarting its generator so inserts cannot race with selection.
LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE;

DO $$
DECLARE
    user_sequence regclass := pg_get_serial_sequence('users', 'id');
    sequence_value bigint;
    sequence_called boolean;
    sequence_next bigint;
    required_next bigint;
BEGIN
    IF user_sequence IS NULL THEN
        RAISE EXCEPTION 'users.id must have an owned sequence';
    END IF;

    EXECUTE format('SELECT last_value, is_called FROM %s', user_sequence)
        INTO sequence_value, sequence_called;
    sequence_next := sequence_value + CASE WHEN sequence_called THEN 1 ELSE 0 END;
    SELECT GREATEST(1000, sequence_next, COALESCE(MAX(id), 0) + 1)
        INTO required_next
        FROM users;

    IF required_next > sequence_next THEN
        EXECUTE format('ALTER SEQUENCE %s RESTART WITH %s', user_sequence, required_next);
    END IF;
END
$$;
