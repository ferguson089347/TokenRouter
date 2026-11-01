-- 315_recharge_bonus.sql records purchased base credit, cumulative campaign
-- identity, and allocated rewards on payment orders. Existing orders retain a
-- zero base and empty campaign identity so historical payments do not enter
-- newly configured activity totals.
ALTER TABLE payment_orders
    ADD COLUMN base_amount DECIMAL(20, 2) NOT NULL DEFAULT 0,
    ADD COLUMN recharge_campaign_id VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN bonus_amount DECIMAL(20, 2) NOT NULL DEFAULT 0,
    ADD COLUMN bonus_snapshot JSONB;

ALTER TABLE payment_orders
    ADD CONSTRAINT payment_orders_bonus_amount_check
    CHECK (bonus_amount >= 0 AND bonus_amount <= amount);

CREATE INDEX payment_orders_recharge_campaign_idx
    ON payment_orders (user_id, recharge_campaign_id)
    WHERE recharge_campaign_id <> '';
