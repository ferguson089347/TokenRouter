-- Merge urgent priority into high while retaining conversations and request digests.
ALTER TABLE support_tickets DROP CONSTRAINT IF EXISTS support_tickets_priority_check;
UPDATE support_tickets SET priority = 'high' WHERE priority = 'urgent';
ALTER TABLE support_tickets ADD CONSTRAINT support_tickets_priority_check
    CHECK (priority IN ('low', 'normal', 'high'));

-- Normalize historical priority values written during rolling upgrades.
CREATE OR REPLACE FUNCTION normalize_support_ticket_priority() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.priority = 'urgent' THEN
        NEW.priority := 'high';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS support_ticket_normalize_priority ON support_tickets;
CREATE TRIGGER support_ticket_normalize_priority
    BEFORE INSERT OR UPDATE OF priority ON support_tickets
    FOR EACH ROW EXECUTE FUNCTION normalize_support_ticket_priority();
