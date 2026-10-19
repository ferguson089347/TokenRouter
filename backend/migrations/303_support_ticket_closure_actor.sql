-- Record authenticated closure identities; historical unknown actors remain unrecorded.
ALTER TABLE support_tickets
    ADD COLUMN IF NOT EXISTS closed_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS closed_by_role VARCHAR(10) NOT NULL DEFAULT '';

-- Retain role snapshots when account deletion clears the actor reference.
ALTER TABLE support_tickets DROP CONSTRAINT IF EXISTS support_tickets_closed_by_role_check;
ALTER TABLE support_tickets ADD CONSTRAINT support_tickets_closed_by_role_check CHECK (
    closed_by_role IN ('', 'user', 'admin', 'system')
    AND (closed_by_role IN ('user', 'admin') OR closed_by IS NULL)
);
