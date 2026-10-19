-- Normalize historical consultation categories while retaining record identities and retry digests.
ALTER TABLE support_tickets DROP CONSTRAINT IF EXISTS support_tickets_type_check;
UPDATE support_tickets SET type = 'consultation' WHERE type IN ('presales', 'aftersales', 'other');
ALTER TABLE support_tickets ADD CONSTRAINT support_tickets_type_check
    CHECK (type IN ('consultation', 'financial', 'technical'));

-- Accept historical writes during rolling upgrades and store canonical categories.
CREATE OR REPLACE FUNCTION normalize_support_ticket_type() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.type IN ('presales', 'aftersales', 'other') THEN
        NEW.type := 'consultation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS support_ticket_normalize_type ON support_tickets;
CREATE TRIGGER support_ticket_normalize_type
    BEFORE INSERT OR UPDATE OF type ON support_tickets
    FOR EACH ROW EXECUTE FUNCTION normalize_support_ticket_type();
